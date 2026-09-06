import { access, readFile } from "node:fs/promises";

const SUPPORTED_IMPORT_ACCEPT = ".txt,.md,.markdown,.mdx,.csv,.html,.htm,.json,.log,.pdf,.docx,.doc,.pptx,.ppt,.xlsx,.xls,.epub";

for (const path of ["index.html", "styles.css", "app.js", "api.js", "build-info.json"]) {
  await access(new URL(`../../internal/web/dist/${path}`, import.meta.url));
}
const app = await readFile(new URL("../../internal/web/dist/app.js", import.meta.url), "utf8");
const i18n = await readFile(new URL("../src/i18n.js", import.meta.url), "utf8");
new Function(
  "applyI18n", "currentLocale", "formatDate", "formatNumber", "initializeI18n", "localized", "observeI18n", "setLocale",
  app.replace(/^import .*$/gm, ""),
);
for (const phrase of [
  "Knowledge service status", "Retrieval scope", "New knowledge base", "Import directory",
  "Final context", "OCR model", "Global settings", "Base settings", "Query history",
  "语言", "知识库", "检索范围", "新建知识库", "查询历史", "OCR 模型", "全局设置",
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
