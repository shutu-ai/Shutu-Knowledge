import { strict as assert } from "node:assert";

globalThis.window = { location: { pathname: "/extensions/shutu-knowledge/index.html" } };

const calls = [];
let mode = "success";
globalThis.fetch = (input, options) => {
  calls.push({ input, options });
  if (mode === "success") {
    return Promise.resolve({ ok: true, status: 200, text: async () => "raw fixture", json: async () => ({}) });
  }
  return new Promise((resolve, reject) => {
    const abort = () => reject(Object.assign(new Error("aborted"), { name: "AbortError" }));
    if (options.signal.aborted) abort();
    else options.signal.addEventListener("abort", abort, { once: true });
  });
};

const { api, setRouteSignal } = await import("../src/api.js?api-test");

const successRoute = new AbortController();
setRouteSignal(successRoute.signal);
assert.equal(await api.rawText("doc-1"), "raw fixture");
assert.equal(calls[0].input, "/extensions/shutu-knowledge/api/documents/doc-1/raw?inline=1");
await api.semanticWiki("base-1");
await api.searchSemanticMemory("base-1", { query: "vector" });
await api.compileKnowledgeContext("base-1", { query: "vector" });
assert.equal(calls.at(-3).input, "/extensions/shutu-knowledge/api/bases/base-1/semantic/wiki");
assert.equal(calls.at(-2).input, "/extensions/shutu-knowledge/api/bases/base-1/semantic/search");
assert.equal(calls.at(-1).input, "/extensions/shutu-knowledge/api/bases/base-1/semantic/context");

mode = "pending";
const cancelledJSONRoute = new AbortController();
setRouteSignal(cancelledJSONRoute.signal);
const pendingJSON = api.status();
cancelledJSONRoute.abort();
await assert.rejects(pendingJSON, (error) => error.name === "AbortError" && error.code === "request_aborted");

const cancelledRoute = new AbortController();
setRouteSignal(cancelledRoute.signal);
const pending = api.rawText("doc-2");
cancelledRoute.abort();
await assert.rejects(pending, (error) => error.name === "AbortError" && error.code === "request_aborted");

console.log("web api: ok");
