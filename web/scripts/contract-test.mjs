import { access, readFile } from "node:fs/promises";

const SUPPORTED_IMPORT_ACCEPT = ".txt,.md,.markdown,.mdx,.csv,.html,.htm,.json,.log,.pdf,.docx,.doc,.pptx,.ppt,.xlsx,.xls,.epub";

for (const path of ["index.html", "styles.css", "app.js", "api.js", "build-info.json"]) {
  await access(new URL(`../../internal/web/dist/${path}`, import.meta.url));
}
const index = await readFile(new URL("../../internal/web/dist/index.html", import.meta.url), "utf8");
if (index.includes('href="/styles.css"') || index.includes('src="/api.js"') || index.includes('src="/app.js"')) {
  throw new Error("extension web assets must use relative URLs");
}
if (!index.includes('href="./styles.css"') || !index.includes('src="./api.js"') || !index.includes('src="./app.js"')) {
  throw new Error("missing relative extension web asset URLs");
}
if (index.includes("language-field") || index.includes('id="language"')) {
  throw new Error("Knowledge must not expose an independent language selector");
}
if (!index.includes('id="return-agent"') || !index.includes('href="/"') || !index.includes("Return to Agent")) {
  throw new Error("extension web must expose a return-to-Agent link");
}
const app = await readFile(new URL("../../internal/web/dist/app.js", import.meta.url), "utf8");
const api = await readFile(new URL("../../internal/web/dist/api.js", import.meta.url), "utf8");
if (!api.includes("extensionPrefix") || !api.includes("fetch(endpoint(path)")) {
  throw new Error("extension API requests must preserve the reverse-proxy prefix");
}
const i18n = await readFile(new URL("../src/i18n.js", import.meta.url), "utf8");
if (!i18n.includes("navigator.languages") || !i18n.includes("split(\"-\")[0] === \"zh\"") || !i18n.includes('addEventListener("storage"')) {
  throw new Error("initial locale must follow Agent browser-language negotiation");
}
new Function(
  "applyI18n", "currentLocale", "formatDate", "formatNumber", "initializeI18n", "localized", "observeI18n",
  app.replace(/^import .*$/gm, ""),
);
if (app.includes("setLocale") || i18n.includes('getElementById("language")')) {
  throw new Error("Knowledge must not own language switching UI");
}
for (const phrase of [
  "Knowledge service status", "Retrieval scope", "New knowledge base", "Import directory",
  "Final context", "OCR model", "Global settings", "Base settings", "Query history",
  "语言", "知识库", "检索范围", "新建知识库", "查询历史", "OCR 模型", "全局设置", "返回 Agent",
]) {
  if (!i18n.includes(phrase)) throw new Error(`missing localization phrase: ${phrase}`);
}
const requiredRoutes = ["overview", "bases", "documents", "import", "recall", "models", "settings"];
for (const route of requiredRoutes) {
  if (!app.includes(`route === "${route}"`)) throw new Error(`missing route: ${route}`);
}
if (!app.includes("MAX_IMPORT_FILES = 20") || !app.includes("files per import; split the selection")) {
  throw new Error("missing 20-file import preflight");
}
if (!app.includes(SUPPORTED_IMPORT_ACCEPT)) {
  throw new Error("missing supported import accept filter");
}

const ollamaStart = app.indexOf("function ollamaPanel");
const ollamaEnd = app.indexOf("\nasync function trackJob", ollamaStart);
if (ollamaStart < 0 || ollamaEnd < 0) throw new Error("missing isolated Ollama panel");
const ollamaPanel = app.slice(ollamaStart, ollamaEnd);
for (const forbidden of ["api.updateBase", "api.updateConfig", "embeddingProvider", "embeddingBaseUrl", "embeddingModel"]) {
  if (ollamaPanel.includes(forbidden)) {
    throw new Error(`Ollama browsing/pulling must not write ${forbidden}`);
  }
}
if (!app.includes('class: "toast-dismiss"') || !app.includes('"Dismiss notification"')) {
  throw new Error("missing dismissable toast control");
}
for (const required of ["function stableID", "function chunkBodyID", "function chunkIsExpanded", "data-chunk-toggle", "Expand all", "Collapse all"]) {
  if (!app.includes(required)) throw new Error(`missing chunk expansion behavior: ${required}`);
}
console.log("web contract: ok");
