import { createHash } from "node:crypto";
import { createReadStream } from "node:fs";
import { mkdir, readFile, rename, rm, stat, writeFile } from "node:fs/promises";
import path from "node:path";
import readline from "node:readline";
import { fileURLToPath } from "node:url";
import { gunzipSync } from "node:zlib";
import {
  AutoModelForSequenceClassification,
  AutoTokenizer,
  env,
  pipeline,
} from "@huggingface/transformers";
import { createCanvas } from "@napi-rs/canvas";
import { getDocument } from "pdfjs-dist/legacy/build/pdf.mjs";
import { createWorker } from "tesseract.js";
import { toMarkdownBytes } from "@firecrawl/anydoc";

const args = process.argv.slice(2);
function argument(name, fallback = "") {
  const index = args.indexOf(name);
  return index >= 0 && args[index + 1] ? args[index + 1] : fallback;
}

const runtimeHome = path.resolve(argument("--runtime-home", path.dirname(fileURLToPath(import.meta.url))));
const knowledgeModelCache = path.resolve(argument("--model-cache", path.join(runtimeHome, "models")));
const offline = args.includes("--offline");
await mkdir(runtimeHome, { recursive: true });
await mkdir(knowledgeModelCache, { recursive: true });
// The Knowledge-owned cache is the actual Transformers.js cache. Keeping the
// state file in runtimeHome but the weights in this explicit directory makes
// cache migration, offline restart, and model removal use one path.
env.cacheDir = knowledgeModelCache;
env.allowLocalModels = true;
// Transformers.js resolves a pinned revision from this cache before making a
// network request. Keeping remote resolution enabled is important because its
// tokenizer loader needs the normal cache resolver during an offline restart;
// once the manifest is READY, no model download is required.
env.allowRemoteModels = !offline;

const DEFAULT_EMBEDDING = {
  id: "onnx-community/Qwen3-Embedding-0.6B-ONNX",
  revision: "c25a394dd583836952667c12f008335071b3f43d",
  dtype: "q4",
  files: {
    "config.json": "66a10929782f3c9a3cd5dec90e2a95c60e05736134a63cd54479eeae80bed175",
    "tokenizer_config.json": "977648852447cb6587327ff3205b0a84cf2fc9f05621d6c8e88a497caafab2e1",
    "tokenizer.json": "def76fb086971c7867b829c23a26261e38d9d74e02139253b38aeb9df8b4b50a",
    "onnx/model_q4.onnx": "8be554b37368134c3f38613c6f6ad0b7bb5f3a6465ab87574dbc0dcf24daa428",
  },
};
const DEFAULT_RERANK = {
  id: "Xenova/bge-reranker-base",
  revision: "280bcc27a84e0b898c251e06fddb25171bd9b101",
  dtype: "fp32",
  files: {
    "config.json": "b6575b9d5be20d6747417c8e20c5a0db1636356e0b6d422d7244c628423c4d4c",
    "tokenizer_config.json": "a1d6bc8734a6f635dc158508bef000f8e2e5a759c7d92f984b2c86e5ff53425b",
    "tokenizer.json": "48564c5c7d3fa64d85d95e65414a542385f88b0f128fd8d4163fd7a57f2be05c",
    "onnx/model.onnx": "15b9a8c3da82eddf263df571281166e00e9308fe19d077084b642ebfcaf06d2b",
  },
};

const OCR_LANGUAGE_DATA = {
  eng: {
    source: "https://cdn.jsdelivr.net/npm/@tesseract.js-data/eng/4.0.0_best_int/eng.traineddata.gz",
    compressedSha256: "45b4cb346724ac1774f1c36f42f182b887bcdb28ebe63e6fff90ac41f3fcff91",
    sha256: "5dc5d8d640a212c9d6184921ba103b186f50e0fed9ee716c53e6b312b400d747",
  },
  chi_sim: {
    source: "https://cdn.jsdelivr.net/npm/@tesseract.js-data/chi_sim/4.0.0_best_int/chi_sim.traineddata.gz",
    compressedSha256: "b8a23f10c7de500891eb458a8adc9cc58ab7f242f08b7d149f5e9aea4ad5db7c",
    sha256: "9784f7c917c546424b690fcde708ce1f604a4393d08bb51ddab146d7d7c794e6",
  },
};

const stateFile = path.join(runtimeHome, "runtime-state.json");
let state = await readState();
if (!state || typeof state !== "object" || !state.components || typeof state.components !== "object") {
  state = { components: {} };
}
state.knowledgeModelCache = knowledgeModelCache;
const embeddingModels = new Map();
const rerankModels = new Map();
let ocrWorkerPromise;

async function readState() {
  try {
    return JSON.parse(await readFile(stateFile, "utf8"));
  } catch {
    return { components: {} };
  }
}

async function saveState() {
  const temporary = `${stateFile}.tmp`;
  await writeFile(temporary, JSON.stringify(state, null, 2), { mode: 0o600 });
  await rename(temporary, stateFile);
}

async function verifyModel(model) {
  if (!model.files) return;
  for (const [relative, expected] of Object.entries(model.files)) {
    const filename = path.join(env.cacheDir, model.id, model.revision, relative);
    const hash = createHash("sha256");
    try {
      for await (const chunk of createReadStream(filename)) hash.update(chunk);
    } catch (error) {
      throw new Error(`model artifact ${relative} is missing: ${error.message}`);
    }
    const actual = hash.digest("hex");
    if (actual !== expected) throw new Error(`model artifact ${relative} checksum mismatch`);
  }
}

function bytesSha256(data) {
  return createHash("sha256").update(data).digest("hex");
}

async function ensureOCRLanguageData() {
  const cachePath = path.join(runtimeHome, "tesscache");
  await mkdir(cachePath, { recursive: true });
  for (const [language, spec] of Object.entries(OCR_LANGUAGE_DATA)) {
    const target = path.join(cachePath, `${language}.traineddata`);
    try {
      const cached = await readFile(target);
      if (bytesSha256(cached) === spec.sha256) continue;
      if (offline) throw new Error(`cached ${language}.traineddata checksum mismatch`);
    } catch (error) {
      if (offline && error?.message?.includes("checksum mismatch")) throw error;
      if (offline) throw new Error(`cached ${language}.traineddata is missing`);
    }
    const response = await fetch(spec.source);
    if (!response.ok) throw new Error(`download ${language}.traineddata failed: HTTP ${response.status}`);
    const compressed = Buffer.from(await response.arrayBuffer());
    if (bytesSha256(compressed) !== spec.compressedSha256) {
      throw new Error(`download ${language}.traineddata checksum mismatch`);
    }
    const decoded = gunzipSync(compressed);
    if (bytesSha256(decoded) !== spec.sha256) {
      throw new Error(`decoded ${language}.traineddata checksum mismatch`);
    }
    const temporary = `${target}.tmp`;
    await writeFile(temporary, decoded, { mode: 0o600 });
    await rename(temporary, target);
  }
}

async function modelFilesPresent(model) {
  if (!model.files) return false;
  for (const relative of Object.keys(model.files)) {
    try {
      const filename = path.join(env.cacheDir, model.id, model.revision, relative);
      const info = await stat(filename);
      if (!info.isFile()) return false;
    } catch (error) {
      return false;
    }
  }
  return true;
}

function localModelDirectory(model) {
  return path.join(env.cacheDir, model.id, model.revision);
}

function setComponent(capability, patch) {
  state.components[capability] = {
    ...(state.components[capability] ?? {}),
    ...patch,
    updatedAt: new Date().toISOString(),
  };
}

function diagnostic(capability, lifecycle, error = "") {
  const ready = lifecycle === "READY";
  let status = ready ? "ready" : "not_installed";
  if (["DOWNLOADING", "VERIFYING", "LOADING"].includes(lifecycle)) status = "loading";
  if (lifecycle === "INSTALLED") status = "installed";
  if (lifecycle === "FAILED") status = "failed";
  let remediation = "";
  if (!ready) {
    if (capability === "embedding") remediation = "select or download the pinned embedding model, then retry the runtime self-test";
    else if (capability === "rerank") remediation = "select or download the pinned reranker model, then retry the runtime self-test";
    else if (capability === "ocr") remediation = "allow one online start to cache OCR language data, or disable offline mode";
    else if (capability === "pdf_render") remediation = "verify the managed PDF runtime installation and rerun Doctor";
    else if (capability === "office") remediation = "verify the managed Office runtime installation and rerun Doctor";
  }
  return { status, lifecycle, lastError: error, remediation };
}

function spec(capability, requested) {
  const fallback = capability === "embedding" ? DEFAULT_EMBEDDING : DEFAULT_RERANK;
  const id = String(requested || fallback.id).replace(/^local:/, "");
  if (id === fallback.id) return fallback;
  // Custom models remain supported, but are never presented as the pinned
  // out-of-box model. A caller must provide a concrete revision in the model
  // string as id@revision to avoid a moving latest reference.
  const separator = id.lastIndexOf("@");
  if (separator <= 0 || separator === id.length - 1) {
    throw new Error(`${capability} model ${id} is not pinned; use id@revision`);
  }
  return { id: id.slice(0, separator), revision: id.slice(separator + 1), dtype: fallback.dtype };
}

async function embeddingModel(modelName) {
  const model = spec("embedding", modelName);
  const key = `${model.id}@${model.revision}`;
  if (!embeddingModels.has(key)) {
    const localFiles = await modelFilesPresent(model);
    setComponent("embedding", {
      lifecycle: localFiles ? "VERIFYING" : "DOWNLOADING", ready: false, model: model.id,
      revision: model.revision, runtime: "transformers.js/onnxruntime-node",
    });
    await saveState();
    let source = model.id;
    const options = { dtype: model.dtype, revision: model.revision };
    if (localFiles) {
      await verifyModel(model);
      source = localModelDirectory(model);
      options.local_files_only = true;
      delete options.revision;
    }
    const loading = pipeline("feature-extraction", source, options).catch((error) => {
      embeddingModels.delete(key);
      throw error;
    });
    embeddingModels.set(key, loading);
  }
  const extractor = await embeddingModels.get(key);
  await verifyModel(model);
  setComponent("embedding", { lifecycle: "INSTALLED", ready: false, model: model.id, revision: model.revision });
  await saveState();
  return { model, extractor };
}

async function rerankModel(modelName) {
  const model = spec("rerank", modelName);
  const key = `${model.id}@${model.revision}`;
  if (!rerankModels.has(key)) {
    const localFiles = await modelFilesPresent(model);
    setComponent("rerank", {
      lifecycle: localFiles ? "VERIFYING" : "DOWNLOADING", ready: false, model: model.id,
      revision: model.revision, runtime: "transformers.js/onnxruntime-node",
    });
    await saveState();
    let source = model.id;
    const options = { revision: model.revision };
    if (localFiles) {
      await verifyModel(model);
      source = localModelDirectory(model);
      options.local_files_only = true;
      delete options.revision;
    }
    const loading = Promise.all([
      AutoTokenizer.from_pretrained(source, options),
      AutoModelForSequenceClassification.from_pretrained(source, { ...options, dtype: model.dtype }),
    ]).catch((error) => {
      rerankModels.delete(key);
      throw error;
    });
    rerankModels.set(key, loading);
  }
  const [tokenizer, classifier] = await rerankModels.get(key);
  await verifyModel(model);
  setComponent("rerank", { lifecycle: "INSTALLED", ready: false, model: model.id, revision: model.revision });
  await saveState();
  return { model, tokenizer, classifier };
}

async function health(capability) {
  if (capability === "pdf_render") {
    return { ready: true, capability, status: "ready", lifecycle: "READY", path: runtimeHome, version: "pdfjs-dist/6.3.289", details: { mode: "full-page" } };
  }
  if (capability === "office") {
    return { ready: true, capability, status: "ready", lifecycle: "READY", path: runtimeHome, version: "@firecrawl/anydoc/0.2.4", details: { formats: "doc,ppt,xls" } };
  }
  const item = state.components[capability];
  if (!item?.ready) {
    const stateInfo = diagnostic(capability, item?.lifecycle ?? "NOT_INSTALLED", item?.lastError ?? "model has not passed inference smoke");
    return {
      ready: false, capability, path: runtimeHome, version: "managed-node/22.14.0",
      model: item?.model, ...stateInfo, details: { lifecycle: stateInfo.lifecycle, error: stateInfo.lastError },
    };
  }
  try {
    if (capability === "embedding") {
      const { model, extractor } = await embeddingModel(item.model);
      await extractor(["runtime health smoke"], { pooling: "last_token", normalize: true });
      return { ready: true, capability, status: "ready", lifecycle: "READY", path: runtimeHome, version: "transformers.js/onnxruntime-node", model: model.id, details: { revision: model.revision, lifecycle: "READY" } };
    }
    if (capability === "rerank") {
      const { model, tokenizer, classifier } = await rerankModel(item.model);
      const inputs = tokenizer(["health"], { text_pair: ["health"], padding: true, truncation: true, max_length: 32 });
      await classifier(inputs);
      return { ready: true, capability, status: "ready", lifecycle: "READY", path: runtimeHome, version: "transformers.js/onnxruntime-node", model: model.id, details: { revision: model.revision, lifecycle: "READY" } };
    }
    if (capability === "ocr") {
      await getOCRWorker();
      return { ready: true, capability, status: "ready", lifecycle: "READY", path: runtimeHome, version: "tesseract.js/7.0.0", details: { lifecycle: "READY", languages: "eng+chi_sim" } };
    }
  } catch (error) {
    setComponent(capability, { lifecycle: "FAILED", ready: false, lastError: errorMessage(error) });
    await saveState();
    const stateInfo = diagnostic(capability, "FAILED", errorMessage(error));
    return { ready: false, capability, path: runtimeHome, version: "managed-node/22.14.0", model: item.model, ...stateInfo, details: { lifecycle: "FAILED", error: errorMessage(error) } };
  }
  return { ready: false, capability, details: { error: "unsupported capability" } };
}

async function embed(params) {
  const texts = Array.isArray(params.texts) ? params.texts.map((value) => String(value)) : [];
  if (texts.length === 0) return { vectors: [] };
  const { model, extractor } = await embeddingModel(params.model);
  setComponent("embedding", { lifecycle: "LOADING", ready: false, model: model.id, revision: model.revision });
  await saveState();
  const output = await extractor(texts, { pooling: "last_token", normalize: true });
  const vectors = output.tolist();
  if (!Array.isArray(vectors) || vectors.length !== texts.length || vectors.some((row) => !Array.isArray(row) || row.length === 0)) {
    throw new Error("embedding runtime returned an invalid vector batch");
  }
  setComponent("embedding", {
    lifecycle: "READY", ready: true, model: model.id, revision: model.revision,
    dimension: vectors[0].length, lastError: undefined,
  });
  await saveState();
  return { vectors };
}

async function rerank(params) {
  const query = String(params.query ?? "");
  const documents = Array.isArray(params.documents) ? params.documents.map((value) => String(value)) : [];
  if (documents.length === 0) return { scores: [] };
  const { model, tokenizer, classifier } = await rerankModel(params.model);
  setComponent("rerank", { lifecycle: "LOADING", ready: false, model: model.id, revision: model.revision });
  await saveState();
  const inputs = tokenizer(documents.map(() => query), {
    text_pair: documents, padding: true, truncation: true, max_length: 512,
  });
  const output = await classifier(inputs);
  const logits = output.logits.tolist();
  const scores = logits.map((row) => {
    const value = Number(row[0]);
    return 1 / (1 + Math.exp(-Math.max(-60, Math.min(60, value))));
  });
  setComponent("rerank", { lifecycle: "READY", ready: true, model: model.id, revision: model.revision, lastError: undefined });
  await saveState();
  return { scores };
}

async function getOCRWorker() {
  if (!ocrWorkerPromise) {
    await ensureOCRLanguageData();
    setComponent("ocr", { lifecycle: "LOADING", ready: false, runtime: "tesseract.js/7.0.0" });
    await saveState();
    ocrWorkerPromise = createWorker("eng+chi_sim", undefined, {
      cachePath: path.join(runtimeHome, "tesscache"),
      cacheMethod: offline ? "readOnly" : "write",
    });
  }
  return ocrWorkerPromise;
}

async function ocr(params) {
  const encoded = String(params.data ?? "");
  if (!encoded) throw new Error("OCR input is empty");
  const worker = await getOCRWorker();
  const result = await worker.recognize(Buffer.from(encoded, "base64"));
  setComponent("ocr", { lifecycle: "READY", ready: true, runtime: "tesseract.js/7.0.0", lastError: undefined });
  await saveState();
  return {
    text: String(result.data?.text ?? "").trim(),
    confidence: Number(result.data?.confidence ?? 0),
  };
}

async function renderPDF(params) {
  const encoded = String(params.data ?? "");
  if (!encoded) throw new Error("PDF input is empty");
  const document = await getDocument({ data: new Uint8Array(Buffer.from(encoded, "base64")), useSystemFonts: true }).promise;
  const pages = [];
  let totalPixels = 0;
  const dpi = Math.max(72, Math.min(300, Number(params.dpi ?? 150)));
  for (let index = 1; index <= Math.min(document.numPages, 100); index += 1) {
    const page = await document.getPage(index);
    let viewport = page.getViewport({ scale: dpi / 72 });
    const maxPixels = 32 * 1024 * 1024;
    if (viewport.width * viewport.height > maxPixels) {
      const scale = Math.sqrt(maxPixels / (viewport.width * viewport.height));
      viewport = page.getViewport({ scale: (dpi / 72) * scale });
    }
    const width = Math.max(1, Math.ceil(viewport.width));
    const height = Math.max(1, Math.ceil(viewport.height));
    totalPixels += width * height;
    if (totalPixels > 128 * 1024 * 1024) throw new Error("PDF render exceeds the total pixel safety limit");
    const canvas = createCanvas(width, height);
    await page.render({ canvasContext: canvas.getContext("2d"), viewport }).promise;
    pages.push({ page: index, png: canvas.toBuffer("image/png").toString("base64") });
  }
  if (pages.length === 0) throw new Error("PDF contains no renderable pages");
  return { pages };
}

async function office(params) {
  const format = String(params.format ?? "").toLowerCase();
  if (!["doc", "ppt", "xls"].includes(format)) throw new Error(`legacy Office format ${format} is unsupported`);
  const encoded = String(params.data ?? "");
  if (!encoded) throw new Error("legacy Office input is empty");
  // anydoc names the OOXML spreadsheet variant `xlsx`; legacy OLE `.xls` is
  // detected from its compound-file signature when no explicit enum is sent.
  const anydocFormat = format === "xls" ? null : format;
  const markdown = await toMarkdownBytes(Buffer.from(encoded, "base64"), anydocFormat);
  if (!String(markdown).trim()) throw new Error("legacy Office conversion returned empty Markdown");
  return { text: String(markdown).trim() };
}

async function loadModel(params) {
  const capability = String(params.capability ?? "");
  if (capability === "embedding") {
    await embed({ model: params.model, texts: ["Knowledge managed runtime smoke"] });
    return health(capability);
  }
  if (capability === "rerank") {
    await rerank({ model: params.model, query: "Knowledge managed runtime smoke", documents: ["Knowledge managed runtime smoke"] });
    return health(capability);
  }
  if (capability === "ocr") {
    await getOCRWorker();
    setComponent("ocr", { lifecycle: "READY", ready: true, runtime: "tesseract.js/7.0.0", lastError: undefined });
    await saveState();
    return health(capability);
  }
  throw new Error(`cannot load model for capability ${capability}`);
}

async function removeModel(params) {
  const capability = String(params.capability ?? "");
  const model = spec(capability, params.model);
  await rm(path.join(env.cacheDir, model.id), { recursive: true, force: true });
  if (state.components[capability]?.model === model.id) {
    setComponent(capability, { lifecycle: "NOT_INSTALLED", ready: false, model: model.id, revision: model.revision, lastError: undefined });
    await saveState();
  }
  if (capability === "embedding") embeddingModels.delete(`${model.id}@${model.revision}`);
  if (capability === "rerank") rerankModels.delete(`${model.id}@${model.revision}`);
  return { removed: true };
}

function errorMessage(error) {
  return error instanceof Error ? error.message : String(error);
}

async function dispatch(request) {
  const method = request.method;
  const params = request.params ?? {};
  if (method === "initialize") {
    return {
      protocol: 1, version: "managed-runtime/1.0.0",
      capabilities: ["embedding", "rerank", "ocr", "pdf_render", "office"],
    };
  }
  if (method === "load") return loadModel(params);
  if (method === "remove") return removeModel(params);
  if (method === "health") return health(String(params.capability ?? ""));
  if (method === "embed" || method === "embedding") return embed(params);
  if (method === "rerank") return rerank(params);
  if (method === "ocr") return ocr(params);
  if (method === "render" || method === "pdf_render") return renderPDF(params);
  if (method === "office") return office(params);
  throw new Error(`unknown managed runtime method ${method}`);
}

function capabilityForRequest(request) {
  const declared = String(request?.params?.capability ?? "");
  if (["embedding", "rerank", "ocr", "pdf_render", "office"].includes(declared)) return declared;
  if (request?.method === "embedding") return "embedding";
  if (request?.method === "rerank") return "rerank";
  if (request?.method === "ocr") return "ocr";
  if (request?.method === "render" || request?.method === "pdf_render") return "pdf_render";
  if (request?.method === "office") return "office";
  return "";
}

const input = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
for await (const line of input) {
  if (!line.trim()) continue;
  let request;
  try {
    request = JSON.parse(line);
    const result = await dispatch(request);
    process.stdout.write(`${JSON.stringify({ id: request.id, result })}\n`);
  } catch (error) {
    const capability = capabilityForRequest(request);
    if (capability) {
      setComponent(capability, { lifecycle: "FAILED", ready: false, lastError: errorMessage(error) });
      await saveState();
    }
    process.stdout.write(`${JSON.stringify({ id: request?.id ?? 0, error: { code: "runtime_failed", message: errorMessage(error) } })}\n`);
  }
}
