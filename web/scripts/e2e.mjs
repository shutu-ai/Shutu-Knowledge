import { spawn, spawnSync } from "node:child_process";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import net from "node:net";
import { existsSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../..", import.meta.url));
const workRoot = path.join(root, ".tmp", "e2e");
const browserName = process.env.SHUTU_KNOWLEDGE_BROWSER ?? "";
const windows = process.platform === "win32";

function fail(message) {
  console.error(`web e2e: ${message}`);
  process.exit(1);
}

function freePort() {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const { port } = server.address();
      server.close(() => resolve(port));
    });
  });
}

async function waitFor(description, timeout, probe) {
  const deadline = Date.now() + timeout;
  for (;;) {
    try {
      if (await probe()) return;
    } catch (error) {
      if (Date.now() >= deadline) throw error;
    }
    if (Date.now() >= deadline) throw new Error(`timeout waiting for ${description}`);
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
}

class CDP {
  #nextId = 1;
  #callbacks = new Map();
  #waiters = [];

  constructor(url) {
    this.socket = new WebSocket(url);
    this.socket.addEventListener("message", (event) => this.#message(JSON.parse(event.data)));
    this.ready = new Promise((resolve, reject) => {
      this.socket.addEventListener("open", resolve, { once: true });
      this.socket.addEventListener("error", () => reject(new Error("browser WebSocket failed")), { once: true });
    });
  }

  #message(message) {
    if (message.id && this.#callbacks.has(message.id)) {
      const { resolve, reject } = this.#callbacks.get(message.id);
      this.#callbacks.delete(message.id);
      if (message.error) reject(new Error(message.error.message));
      else resolve(message.result);
      return;
    }
    this.#waiters = this.#waiters.filter((waiter) => {
      if (waiter.event !== message.method) return true;
      waiter.resolve(message.params);
      return false;
    });
  }

  send(method, params = {}) {
    const id = this.#nextId++;
    return new Promise((resolve, reject) => {
      this.#callbacks.set(id, { resolve, reject });
      this.socket.send(JSON.stringify({ id, method, params }));
    });
  }

  wait(event) {
    return new Promise((resolve) => this.#waiters.push({ event, resolve }));
  }

  close() {
    this.socket.close();
  }
}

async function evaluate(page, expression, awaitPromise = false) {
  const result = await page.send("Runtime.evaluate", {
    expression,
    awaitPromise,
    returnByValue: true,
  });
  if (result.exceptionDetails) {
    const detail = result.exceptionDetails.exception?.description ?? result.exceptionDetails.text;
    throw new Error(detail);
  }
  return result.result.value;
}

async function waitForPageValue(page, description, timeout, expression) {
  await waitFor(description, timeout, async () => await evaluate(page, expression, true));
}

async function browserExecutable() {
  const candidates = [
    browserName,
    process.env.CHROME,
    windows ? "C:/Program Files/Google/Chrome/Application/chrome.exe" : "/usr/bin/google-chrome",
    windows ? "C:/Program Files (x86)/Google/Chrome/Application/chrome.exe" : "/usr/bin/chromium-browser",
    windows ? "C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe" : "/usr/bin/chromium",
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  ].filter(Boolean);
  for (const candidate of candidates) {
    if (candidate.includes(path.sep) || candidate.includes("/")) {
      if (existsSync(candidate)) return candidate;
      continue;
    }
    const lookup = spawnSync(candidate, ["--version"], { stdio: "ignore" });
    if (!lookup.error) return candidate;
  }
  fail("Chrome/Chromium/Edge was not found; set SHUTU_KNOWLEDGE_BROWSER to its executable");
}

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

async function apiValue(port, path, init = {}) {
  const response = await fetch(`http://127.0.0.1:${port}${path}`, init);
  const payload = await response.json();
  assert(response.ok && payload.ok === true, `API ${path} failed: HTTP ${response.status}`);
  return payload.value;
}

async function waitForOperation(port, operationId) {
  const deadline = Date.now() + 20_000;
  for (;;) {
    const operation = await apiValue(port, `/api/operations/${operationId}`);
    if (["succeeded", "failed", "cancelled"].includes(operation.state)) {
      assert(operation.state === "succeeded", `operation ${operationId}: ${operation.state} ${operation.errorMessage ?? ""}`);
      return operation;
    }
    if (Date.now() >= deadline) throw new Error(`operation ${operationId} did not finish`);
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
}

function exited(child) {
  return new Promise((resolve) => child.once("exit", resolve));
}

async function run() {
  const browserPath = await browserExecutable();
  await mkdir(workRoot, { recursive: true });
  const work = await mkdtemp(path.join(workRoot, "run-"));
  const home = path.join(work, "knowledge-home");
  const profile = path.join(work, "browser-profile");
  const binary = path.join(work, `shutu-knowledge${windows ? ".exe" : ""}`);
  await mkdir(home, { recursive: true });
  await mkdir(profile, { recursive: true });

  const webBuild = spawnSync(process.execPath, ["scripts/build.mjs"], {
    cwd: path.join(root, "web"),
    stdio: "inherit",
  });
  if (webBuild.status !== 0) fail("Web build failed");
  const build = spawnSync("go", ["build", "-o", binary, "./cmd/shutu-knowledge"], {
    cwd: root,
    stdio: "inherit",
  });
  if (build.status !== 0) fail("Go backend build failed");

  const [apiPort, browserPort] = await Promise.all([freePort(), freePort()]);
  await writeFile(path.join(home, "config.yaml"), [
    "server:",
    `  addr: 127.0.0.1:${apiPort}`,
    "",
  ].join("\n"));

  let backendLog = "";
  const startBackend = () => {
    const child = spawn(binary, ["serve"], {
      env: { ...process.env, SHUTU_KNOWLEDGE_HOME: home },
      stdio: ["ignore", "pipe", "pipe"],
    });
    child.stdout.on("data", () => {});
    child.stderr.on("data", (chunk) => {
      const text = chunk.toString();
      backendLog = `${backendLog}${text}`.slice(-8000);
      process.stderr.write(text);
    });
    return child;
  };
  let backend = startBackend();

  let browser;
  let page;
  let succeeded = false;
  try {
    await waitFor("backend health", 20_000, async () => {
      const response = await fetch(`http://127.0.0.1:${apiPort}/healthz`);
      if (!response.ok) throw new Error(`backend health HTTP ${response.status}`);
      const value = await response.json();
      assert(value.ready === true, "backend did not become ready");
      return true;
    });

    // C13 uses a real process boundary, not just a new handler: capture the
    // deployed build identity and cache revalidation result, terminate the
    // backend, then boot the same binary again on the same data home.
    const versionResponse = await fetch(`http://127.0.0.1:${apiPort}/api/version`);
    const versionBefore = await versionResponse.json();
    const assetResponse = await fetch(`http://127.0.0.1:${apiPort}/app.js`);
    const etagBefore = assetResponse.headers.get("etag");
    assert(typeof versionBefore?.webBuild === "string" && /^[0-9a-f]{64}$/.test(versionBefore.webBuild),
      "backend did not expose a SHA-256 web build identity");
    assert(Boolean(etagBefore), "deployed web assets have no build ETag");
    const revalidatedBefore = await fetch(`http://127.0.0.1:${apiPort}/app.js`, {
      headers: { "if-none-match": etagBefore },
    });
    assert(revalidatedBefore.status === 304, "unchanged web asset did not revalidate as 304");
    backend.kill();
    await exited(backend);
    backend = startBackend();
    await waitFor("restarted backend health", 20_000, async () => {
      const response = await fetch(`http://127.0.0.1:${apiPort}/healthz`);
      if (!response.ok) throw new Error(`restarted backend health HTTP ${response.status}`);
      const value = await response.json();
      assert(value.ready === true, "restarted backend did not become ready");
      return true;
    });
    const versionAfter = await (await fetch(`http://127.0.0.1:${apiPort}/api/version`)).json();
    const assetAfter = await fetch(`http://127.0.0.1:${apiPort}/app.js`);
    assert(versionAfter.webBuild === versionBefore.webBuild, "restart changed web build identity");
    assert(assetAfter.headers.get("etag") === etagBefore, "restart changed web asset ETag");
    const revalidatedAfter = await fetch(`http://127.0.0.1:${apiPort}/app.js`, {
      headers: { "if-none-match": etagBefore },
    });
    assert(revalidatedAfter.status === 304, "restarted backend did not honor cached build identity");

    browser = spawn(browserPath, [
      "--headless=new",
      "--disable-gpu",
      "--no-first-run",
      "--no-default-browser-check",
      "--remote-debugging-port=" + browserPort,
      `--user-data-dir=${profile}`,
      "about:blank",
    ], { stdio: ["ignore", "ignore", "pipe"] });
    browser.stderr.on("data", () => {});

    await waitFor("browser DevTools endpoint", 20_000, async () => {
      const response = await fetch(`http://127.0.0.1:${browserPort}/json/list`);
      assert(response.ok, "DevTools endpoint unavailable");
      const targets = await response.json();
      assert(targets.some((target) => target.type === "page"), "no browser page target");
      return true;
    });
    const targets = await (await fetch(`http://127.0.0.1:${browserPort}/json/list`)).json();
    const target = targets.find((item) => item.type === "page");
    page = new CDP(target.webSocketDebuggerUrl);
    await page.ready;
    await page.send("Page.enable");
    await page.send("Runtime.enable");
    await page.send("Emulation.setDeviceMetricsOverride", {
      width: 1440, height: 900, deviceScaleFactor: 1, mobile: false,
    });

    // The lifecycle assertions use English as their stable baseline. Keep
    // that explicit so the test does not depend on the runner's browser
    // language now that the UI follows Agent's browser-locale negotiation.
    // Inject it before the first navigation so the app has no locale flash or
    // extra reload race in the CDP harness.
    await page.send("Page.addScriptToEvaluateOnNewDocument", {
      source: `if (!localStorage.getItem("knowledge-language")) localStorage.setItem("knowledge-language", "en");`,
    });

    let firstNavigation = true;
    const navigate = async (route) => {
      if (firstNavigation) {
        const loaded = page.wait("Page.loadEventFired");
        await page.send("Page.navigate", { url: `http://127.0.0.1:${apiPort}/#/${route}` });
      await loaded;
      firstNavigation = false;
      } else {
        await evaluate(page, `location.hash = "#/${route}"`);
      }
      try {
        await waitForPageValue(page, `${route} screen`, 10_000,
        `document.querySelector("#navigation a.active")?.getAttribute("href") === "#/${route}"`);
      } catch (error) {
      const state = await evaluate(page, `({href: location.href, active: document.querySelector("#navigation a.active")?.outerHTML, body: document.body.innerText.slice(0, 200)})`);
      console.error("navigation debug:", state);
      const forms = await evaluate(page, `[...document.querySelectorAll("#screen form h2")].map((item) => item.textContent)`);
      console.error("screen forms:", forms);
      throw error;
      }
      await waitForPageValue(page, `${route} loading cleared`, 10_000,
        `!document.querySelector("#screen .loading-state")`);
    };
    const screenshot = async (name) => {
      const result = await page.send("Page.captureScreenshot", { format: "png", fromSurface: true });
      await writeFile(path.join(work, name), Buffer.from(result.data, "base64"));
    };

    const evaluateWithArgs = async (args, body, awaitPromise = true) =>
      evaluate(page, `(() => { const args = ${JSON.stringify(args)}; return (async () => { ${body} })(); })()`, awaitPromise);

    await navigate("import");
    await waitForPageValue(page, "import without selected base", 10_000, `
      document.querySelector('select[aria-label="Knowledge base"]') &&
      document.body.innerText.includes("Select a knowledge base before importing.")
    `);
    await navigate("bases");
    await waitForPageValue(page, "base creation form", 10_000,
      `[...document.querySelectorAll("#screen form h2")].some((item) => item.textContent === "New knowledge base")`);
    await evaluateWithArgs({
      name: "E2E Operations",
      group: "E2E",
      description: "browser lifecycle corpus",
    }, `
      const form = [...document.querySelectorAll("form")].find((item) =>
        [...item.querySelectorAll("h2")].some((heading) => heading.textContent === "New knowledge base"));
      if (!form) throw new Error("base form missing");
      form.querySelector('[name="name"]').value = args.name;
      form.querySelector('[name="group"]').value = args.group;
      form.querySelector('[name="description"]').value = args.description;
      form.requestSubmit();
    `);
    await waitForPageValue(page, "created base", 10_000,
      `document.body.innerText.includes("E2E Operations")`);

    await navigate("import");
    await waitForPageValue(page, "text import form", 10_000,
      `[...document.querySelectorAll("#screen form h2")].some((item) => item.textContent === "Text")`);
    await evaluateWithArgs({ base: "E2E Operations" }, `
      const picker = document.querySelector('select[aria-label="Knowledge base"]');
      picker.value = [...picker.options].find((option) => option.text === args.base).value;
      picker.dispatchEvent(new Event("change", { bubbles: true }));
    `);
    await waitForPageValue(page, "import base selection", 10_000,
      `document.querySelector('select[aria-label="Knowledge base"]')?.selectedOptions?.[0]?.text === "E2E Operations"`);
    await evaluateWithArgs({
      title: "Browser lifecycle document",
      content: "Chrome E2E imported this operational recovery procedure. The release token is E2E-7788.",
    }, `
      const form = [...document.querySelectorAll("form")].find((item) =>
        [...item.querySelectorAll("h2")].some((heading) => heading.textContent === "Text"));
      if (!form) throw new Error("text import form missing");
      form.title.value = args.title;
      form.content.value = args.content;
      form.requestSubmit();
    `);

    // A directory path is validated by the worker, not by the receiver, so
    // this exercises the browser operation card across a real 202-to-failure
    // lifecycle without stubbing the OperationStore.
    const missingDirectory = "definitely-missing-e2e-directory";
    await evaluateWithArgs({ path: missingDirectory }, `
      const form = [...document.querySelectorAll("form")].find((item) =>
        [...item.querySelectorAll("h2")].some((heading) => heading.textContent === "Directory"));
      if (!form) throw new Error("directory import form missing");
      form.querySelector('[name="path"]').value = args.path;
      form.requestSubmit();
    `);
    await waitForPageValue(page, "failed import operation card", 10_000, `
      [...document.querySelectorAll("[data-document-job-id]")].some((card) =>
        card.textContent.includes("${missingDirectory}") &&
        card.querySelector("[data-document-job-status]")?.textContent.trim().toLowerCase() !== "succeeded")
    `);
    await waitForPageValue(page, "failed import terminal state", 10_000, `
      [...document.querySelectorAll("[data-document-job-id]")].some((card) =>
        card.textContent.includes("${missingDirectory}") &&
        card.querySelector("[data-document-job-status]")?.textContent.trim().toLowerCase() === "failed" &&
        [...card.querySelectorAll("button")].some((button) => button.textContent.trim() === "Retry"))
    `);
    await waitForPageValue(page, "failed import active count", 10_000,
      `document.querySelector("[data-document-job-summary]")?.textContent === "0 active tasks"`);

    await navigate("documents");
    await waitForPageValue(page, "ready imported document", 20_000, `
      document.body.innerText.includes("Browser lifecycle document") &&
      document.body.innerText.toLowerCase().includes("ready")
    `);
    await waitForPageValue(page, "imported document row", 10_000, `
      [...document.querySelectorAll("tbody tr")]
        .some((item) => item.textContent.includes("Browser lifecycle document"))
    `);
    await evaluate(page, `
      const row = [...document.querySelectorAll("tbody tr")]
        .find((item) => item.textContent.includes("Browser lifecycle document"));
      if (!row) throw new Error("document row missing");
      [...row.querySelectorAll("button")].find((item) => item.textContent === "Chunks").click();
      true
    `);
    await waitForPageValue(page, "chunk preview", 10_000,
      `Boolean(document.querySelector("[data-chunk-toggle]"))`);
    await evaluate(page, `
      const toggle = document.querySelector("[data-chunk-toggle]");
      const body = document.getElementById(toggle.getAttribute("aria-controls"));
      if (!body || body.textContent.length === 0) throw new Error("chunk body missing");
      toggle.click();
      true
    `);
    await waitForPageValue(page, "expanded chunk", 10_000,
      `document.querySelector("[data-chunk-toggle]")?.getAttribute("aria-expanded") === "true"`);
    await evaluate(page, `
      document.querySelector("[data-chunk-toggle]").click();
      true
    `);
    await waitForPageValue(page, "collapsed chunk", 10_000,
      `document.querySelector("[data-chunk-toggle]")?.getAttribute("aria-expanded") === "false"`);

    // P5 uses two isolated bases and real durable tasks. The stress marker
    // lives outside #screen, so a full-page replacement fails the assertion.
    const stressBases = [];
    for (const [name, documentTitle] of [
      ["E2E Stress Alpha", "Stress document Alpha"],
      ["E2E Stress Beta", "Stress document Beta"],
    ]) {
      const base = await apiValue(apiPort, "/api/bases", {
        method: "POST", headers: { "content-type": "application/json" },
        body: JSON.stringify({ name, group: "E2E Stress", description: "two-base frontend stress" }),
      });
      for (let documentIndex = 0; documentIndex < 3; documentIndex += 1) {
        const title = `${documentTitle} ${documentIndex + 1}`;
        const accepted = await apiValue(apiPort, `/api/bases/${base.id}/documents`, {
          method: "POST", headers: { "content-type": "application/json" },
          body: JSON.stringify({ title, content: `${title} durable stress content.` }),
        });
        const operationId = accepted.operation?.operationId ?? accepted.operationId;
        assert(Boolean(operationId), "text import did not return an operation");
        await waitForOperation(apiPort, operationId);
      }
      stressBases.push(base);
    }

    await evaluate(page, `document.body.dataset.e2eStressMarker = "retained"; true`);
    await navigate("bases");
    await waitForPageValue(page, "stress bases listed", 10_000, `
      document.body.innerText.includes("E2E Stress Alpha") &&
      document.body.innerText.includes("E2E Stress Beta")
    `);
    await navigate("documents");
    await waitForPageValue(page, "stress base picker", 10_000,
      `Boolean(document.querySelector('select[aria-label="Knowledge base"]'))`);
    let stressOperations = 0;
    for (const base of stressBases) {
      await waitForPageValue(page, `${base.name} base option`, 10_000,
        `[...document.querySelectorAll('select[aria-label="Knowledge base"] option')].some((option) => option.text === "${base.name}")`);
      await evaluateWithArgs({ baseName: base.name }, `
        const picker = [...document.querySelectorAll('select[aria-label="Knowledge base"]')][0];
        picker.value = [...picker.options].find((option) => option.text === args.baseName).value;
        picker.dispatchEvent(new Event("change", { bubbles: true }));
      `);
      await waitForPageValue(page, `${base.name} stress document`, 10_000,
        `document.body.innerText.includes("${base.name === stressBases[0].name ? "Stress document Alpha" : "Stress document Beta"}")`);
      stressOperations += await evaluate(page, `
        (() => {
          const rows = [...document.querySelectorAll('[data-document-table] tbody tr')]
            .filter((item) => item.textContent.includes("Stress document"));
          for (const row of rows) {
            [...row.querySelectorAll("button")].find((button) => button.textContent === "Reindex").click();
          }
          return rows.length;
        })()
      `);
    }

    for (let round = 0; round < 30; round += 1) {
      await navigate(["overview", "bases", "import", "documents"][round % 4]);
    }
    await evaluate(page, `
      if (document.body.dataset.e2eStressMarker !== "retained") throw new Error("full-page replacement detected");
      const errorToast = document.querySelector(".toast.error");
      if (errorToast && errorToast.style.display !== "none") throw new Error("stress produced an error toast");
      true
    `);
    const deadline = Date.now() + 20_000;
    let completedStress;
    for (;;) {
      const page1 = await apiValue(apiPort, "/api/operations?limit=100");
      completedStress = page1.operations.filter((item) => item.type === "reindex_document"
        && stressBases.some((base) => item.baseId === base.id));
      if (completedStress.length >= stressOperations
        && completedStress.every((item) => item.state === "succeeded")) break;
      if (Date.now() >= deadline) {
        throw new Error(`stress operations incomplete: ${JSON.stringify(completedStress.map(({ state, type }) => ({ state, type })))}`);
      }
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
    assert(completedStress.length >= 6, `expected six stress reindex operations, got ${completedStress.length}`);
    await navigate("documents");
    await waitForPageValue(page, "completed stress task cards", 10_000,
      `document.querySelector("[data-document-job-summary]")?.textContent === "0 active tasks"`);
    for (const base of stressBases) {
      const documents = await apiValue(apiPort, `/api/bases/${base.id}/documents/children?parentId=&limit=50&offset=0`);
      assert(documents.documents.length === 3
        && documents.documents.every((item) => item.chunkCount === 1),
        `${base.name} did not retain exactly three indexed documents`);
    }

    // Force the older Alpha response to resolve after the current Beta
    // response. The base-bound generation must discard Alpha rather than let
    // it replace Beta's rendered document rows.
    await navigate("documents");
    await waitForPageValue(page, "stale-request base picker", 10_000,
      `Boolean(document.querySelector('select[aria-label="Knowledge base"]'))`);
    await evaluate(page, `
      window.__nativeFetch = window.fetch;
      const originalFetch = window.fetch.bind(window);
      window.fetch = async (input, init) => {
        const url = String(input instanceof Request ? input.url : input);
        const response = await originalFetch(input, init);
        if (url.includes("/api/bases/") && url.includes("/documents/children")) {
          const baseId = decodeURIComponent(url.match(/\\/api\\/bases\\/([^/]+)\\/documents/)[1]);
          const delay = baseId === ${JSON.stringify(stressBases[0].id)} ? 250 : 0;
          await new Promise((resolve) => setTimeout(resolve, delay));
        }
        return response;
      };
      true
    `);
    await evaluate(page, `
      const picker = [...document.querySelectorAll('select[aria-label="Knowledge base"]')][0];
      const choose = (name) => {
        picker.value = [...picker.options].find((option) => option.text === name).value;
        picker.dispatchEvent(new Event("change", { bubbles: true }));
      };
      choose("E2E Stress Alpha");
      choose("E2E Stress Beta");
      true
    `);
    await waitForPageValue(page, "stress Beta remains selected after delayed Alpha", 10_000,
      `[...document.querySelectorAll("[data-document-table] tbody tr")]
        .some((item) => item.textContent.includes("Stress document Beta"))`);
    await new Promise((resolve) => setTimeout(resolve, 350));
    await evaluate(page, `
      const rows = [...document.querySelectorAll("[data-document-table] tbody tr")];
      if (!rows.some((item) => item.textContent.includes("Stress document Beta"))) throw new Error("Beta response lost");
      if (rows.some((item) => item.textContent.includes("Stress document Alpha"))) throw new Error("stale Alpha response overwrote Beta");
      if (document.body.dataset.e2eStressMarker !== "retained") throw new Error("full-page replacement detected");
      true
    `);
    await evaluate(page, `(() => { window.fetch = window.__nativeFetch; return true; })()`);

    await navigate("recall");
    await waitForPageValue(page, "recall form", 10_000,
      `Boolean(document.querySelector('form input[name="query"]'))`);
    await evaluateWithArgs({ query: "E2E-7788 release token" }, `
      const form = document.querySelector('form input[name="query"]').closest("form");
      const picker = form.querySelector('select[aria-label="Knowledge base"]');
      picker.value = [...picker.options].find((option) => option.text === "E2E Operations").value;
      picker.dispatchEvent(new Event("change", { bubbles: true }));
    `);
    await waitForPageValue(page, "recall base selection", 10_000,
      `document.querySelector('form input[name="query"]')?.closest("form")?.querySelector('select[aria-label="Knowledge base"]')?.selectedOptions?.[0]?.text === "E2E Operations"`);
    await evaluate(page, `
      const form = document.querySelector('form input[name="query"]').closest("form");
      form.querySelector('input[name="query"]').value = "E2E-7788 release token";
      form.querySelector('select[name="mode"]').value = "lexical";
      form.requestSubmit();
    `, true);
    await waitForPageValue(page, "recall results", 20_000,
      `document.body.innerText.includes("Final context") && document.body.innerText.includes("E2E-7788")`);
    await waitForPageValue(page, "recall history", 10_000,
      `Boolean(document.querySelector(".recall-history .history-replay"))`);
    await evaluate(page, `
      const history = document.querySelector(".recall-history .history-replay");
      if (!history || !history.textContent.includes("E2E-7788")) throw new Error("history replay missing");
      document.querySelector('.recall-history [aria-label="Delete query history item"]').click();
    `, true);
    await waitForPageValue(page, "history deletion", 10_000,
      `document.body.innerText.includes("Run a query to create replayable history")`);

    await navigate("models");
    await waitForPageValue(page, "OCR model workflow", 90_000, `
      document.body.innerText.includes("OCR model") &&
      document.body.innerText.includes("PaddlePaddle/PP-OCRv5-mobile") &&
      document.body.innerText.includes("Runtime helper: not ready")
    `);
    await evaluate(page, `(() => {
      const form = [...document.querySelectorAll("#screen form")]
        .find((item) => item.querySelector('select[name="provider"]'));
      if (!form) throw new Error("provider form missing");
      form.querySelector('select[name="rerankMode"]').value = "remote";
      form.querySelector('select[name="rerankMode"]').dispatchEvent(new Event("change", { bubbles: true }));
      window.fetch = async () => new Response(
        JSON.stringify({ ok: false, error: { message: "Dismissable UI-policy toast" } }),
        { status: 500, headers: { "content-type": "application/json" } },
      );
      [...form.querySelectorAll("button")].find((item) => item.textContent.includes("Save providers")).click();
      return true;
    })()`);
    await waitForPageValue(page, "dismissable toast", 10_000, `
      document.querySelector(".toast .toast-message")?.textContent === "Dismissable UI-policy toast"
    `);
    await evaluate(page, `
      document.querySelector(".toast-dismiss").click();
      true
    `);
    await waitForPageValue(page, "dismissed toast", 10_000,
      `document.querySelector(".toast").style.display === "none"`);
    await evaluate(page, `(() => { delete window.fetch; return true; })()`);

    await navigate("overview");
    await evaluate(page, `
      localStorage.setItem("knowledge-language", "zh");
      location.reload();
      true;
    `, true);
    await waitForPageValue(page, "Chinese overview", 10_000, `
      document.documentElement.lang === "zh-CN" &&
      document.getElementById("page-title").textContent === "总览" &&
      document.documentElement.scrollWidth <= window.innerWidth &&
      document.body.innerText.includes("检索范围")
    `);
    await navigate("documents");
    await waitForPageValue(page, "Chinese documents", 10_000,
      `document.body.innerText.includes("Browser lifecycle document")`);
    await navigate("import");
    await waitForPageValue(page, "Chinese import", 10_000,
      `document.body.innerText.includes("导入文本")`);
    await navigate("settings");
    await waitForPageValue(page, "Chinese settings", 10_000,
      `[...document.querySelectorAll("#screen h2")].some((item) => item.textContent === "全局设置")`);
    await evaluate(page, `
      localStorage.setItem("knowledge-language", "en");
      location.reload();
      true;
    `, true);
    await waitForPageValue(page, "English settings", 10_000,
      `[...document.querySelectorAll("#screen h2")].some((item) => item.textContent === "Global settings")`);

    await page.send("Emulation.setDeviceMetricsOverride", {
      width: 390, height: 844, deviceScaleFactor: 2, mobile: true,
    });
    await navigate("documents");
    await waitForPageValue(page, "mobile documents", 10_000,
      `document.body.innerText.includes("Browser lifecycle document")`);
    await navigate("import");
    await waitForPageValue(page, "mobile import", 10_000,
      `document.body.innerText.includes("Import text")`);
    await page.send("Emulation.setDeviceMetricsOverride", {
      width: 1440, height: 900, deviceScaleFactor: 1, mobile: false,
    });
    await screenshot("lifecycle.png");
    const listedBases = await apiValue(apiPort, "/api/bases");
    const cleanupBase = listedBases.find((item) => item.name === "E2E Operations");
    assert(Boolean(cleanupBase), "E2E Operations base missing before cleanup");
    const cleanupAccepted = await apiValue(apiPort, `/api/bases/${cleanupBase.id}`, { method: "DELETE" });
    const cleanupOperationId = cleanupAccepted.operationId ?? cleanupAccepted.jobId;
    assert(Boolean(cleanupOperationId), "base cleanup did not return an operation");
    await waitForOperation(apiPort, cleanupOperationId);
    for (const base of stressBases) {
      const accepted = await apiValue(apiPort, `/api/bases/${base.id}`, { method: "DELETE" });
      const operationId = accepted.operationId ?? accepted.jobId;
      assert(Boolean(operationId), "stress base cleanup did not return an operation");
      await waitForOperation(apiPort, operationId);
    }
    const health = await (await fetch(`http://127.0.0.1:${apiPort}/api/stats`)).json();
    assert(health.ok === true, "final API health contract failed");
    succeeded = true;
    console.log("web e2e: Chrome/CDP lifecycle passed");
  } catch (error) {
    if (page) {
      try {
        const shot = await page.send("Page.captureScreenshot", { format: "png" });
        await writeFile(path.join(work, "failure.png"), Buffer.from(shot.data, "base64"));
      } catch {}
    }
    console.error(`backend log tail: ${backendLog || "stderr written above"}`);
    fail(error.stack ?? error.message);
  } finally {
    page?.close();
    browser?.kill();
    backend.kill();
    await Promise.allSettled([browser ? exited(browser) : null, exited(backend)]);
    if (succeeded) await rm(work, { recursive: true, force: true }).catch(() => {});
  }
}

await run();
