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

  const backend = spawn(binary, ["serve"], {
    env: { ...process.env, SHUTU_KNOWLEDGE_HOME: home },
    stdio: ["ignore", "pipe", "pipe"],
  });
  backend.stdout.on("data", () => {});
  let backendLog = "";
  backend.stderr.on("data", (chunk) => {
    const text = chunk.toString();
    backendLog = `${backendLog}${text}`.slice(-8000);
    process.stderr.write(text);
  });

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
    };
    const screenshot = async (name) => {
      const result = await page.send("Page.captureScreenshot", { format: "png", fromSurface: true });
      await writeFile(path.join(work, name), Buffer.from(result.data, "base64"));
    };

    const evaluateWithArgs = async (args, body, awaitPromise = true) =>
      evaluate(page, `(() => { const args = ${JSON.stringify(args)}; return (async () => { ${body} })(); })()`, awaitPromise);

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

    await navigate("documents");
    await waitForPageValue(page, "ready imported document", 20_000, `
      document.body.innerText.includes("Browser lifecycle document") &&
      document.body.innerText.toLowerCase().includes("ready")
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
    await waitForPageValue(page, "OCR model workflow", 10_000, `
      document.body.innerText.includes("OCR model") &&
      document.body.innerText.includes("PaddlePaddle/PP-OCRv5-mobile") &&
      document.body.innerText.includes("Runtime helper: not configured")
    `);
    await evaluate(page, `(() => {
      const form = [...document.querySelectorAll("#screen form")]
        .find((item) => item.querySelector('select[name="provider"]'));
      if (!form) throw new Error("provider form missing");
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
      const language = document.getElementById("language");
      language.value = "zh";
      language.dispatchEvent(new Event("change", { bubbles: true }));
    `, true);
    await waitForPageValue(page, "Chinese overview", 10_000, `
      document.documentElement.lang === "zh-CN" &&
      document.getElementById("page-title").textContent === "总览" &&
      document.documentElement.scrollWidth <= window.innerWidth &&
      document.body.innerText.includes("检索范围")
    `);
    await navigate("settings");
    await waitForPageValue(page, "Chinese settings", 10_000,
      `[...document.querySelectorAll("#screen h2")].some((item) => item.textContent === "全局设置")`);
    await evaluate(page, `
      const language = document.getElementById("language");
      language.value = "en";
      language.dispatchEvent(new Event("change", { bubbles: true }));
    `, true);
    await waitForPageValue(page, "English settings", 10_000,
      `[...document.querySelectorAll("#screen h2")].some((item) => item.textContent === "Global settings")`);

    await screenshot("lifecycle.png");
    await navigate("bases");
    await waitForPageValue(page, "base row before delete", 10_000,
      `[...document.querySelectorAll(".list-row")].some((item) => item.textContent.includes("E2E Operations"))`);
    await evaluate(page, `
      window.confirm = () => true;
      const row = [...document.querySelectorAll(".list-row")].find((item) => item.textContent.includes("E2E Operations"));
      if (!row) throw new Error("base row missing before delete");
      [...row.querySelectorAll("button")].find((button) => button.textContent === "Delete").click();
    `, true);
    await waitForPageValue(page, "base deletion", 10_000,
      `!document.querySelector(".list-row")?.textContent.includes("E2E Operations")`);

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
