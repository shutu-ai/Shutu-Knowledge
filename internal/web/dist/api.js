class ApiError extends Error {
  constructor(message, status, code) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

// The standalone server is mounted at /, while Agent reverse-proxies the
// extension below /extensions/<id>/. Keep API requests on the same origin and
// proxy boundary in both modes.
const extensionPrefix = window.location.pathname.match(/^(.*\/extensions\/[^/]+)/)?.[1] ?? "";
const endpoint = (path) => `${extensionPrefix}${path}`;

async function request(path, options = {}) {
  const { timeoutMs = 15000, ...fetchOptions } = options;
  const externalSignal = fetchOptions.signal;
  if (!externalSignal && routeSignal && (!fetchOptions.method || fetchOptions.method === "GET")
    && !path.startsWith("/api/operations/") && !path.startsWith("/api/jobs/")) {
    fetchOptions.signal = routeSignal;
  }
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  const abortFromRoute = () => controller.abort();
  if (externalSignal) {
    if (externalSignal.aborted) controller.abort();
    else externalSignal.addEventListener("abort", abortFromRoute, { once: true });
  }
  try {
    const response = await fetch(endpoint(path), {
      ...fetchOptions,
      signal: controller.signal,
      headers: { "content-type": "application/json", ...(fetchOptions.headers ?? {}) },
    });
    const payload = await response.json().catch(() => ({}));
    if (payload.ok === undefined) return payload;
    if (!payload.ok) {
      throw new ApiError(payload.error?.message ?? `HTTP ${response.status}`, response.status, payload.error?.code);
    }
    return payload.value;
  } catch (error) {
    if (error?.name === "AbortError") {
      if (externalSignal?.aborted) {
        const aborted = new ApiError(`Request aborted: ${path}`, 499, "request_aborted");
        aborted.name = "AbortError";
        throw aborted;
      }
      throw new ApiError(`Request timed out: ${path}`, 408, "request_timeout");
    }
    throw error;
  } finally {
    clearTimeout(timer);
    if (externalSignal) externalSignal.removeEventListener("abort", abortFromRoute);
  }
}

let routeSignal;

export function setRouteSignal(signal) {
  routeSignal = signal;
}

const post = (path, body, options = {}) => request(path, { ...options, method: "POST", body: JSON.stringify(body) });

export const api = {
  request,
  status: (options = {}) => request("/api/status", options),
  stats: (baseID = "") => request(baseID ? `/api/bases/${baseID}/stats` : "/api/stats"),
  bases: (options = {}) => request("/api/bases", options),
  createBase: (body) => post("/api/bases", body),
  updateBase: (id, body) => request(`/api/bases/${id}`, { method: "PATCH", body: JSON.stringify(body) }),
  deleteBase: (id) => request(`/api/bases/${id}`, { method: "DELETE" }),
  groups: () => request("/api/groups"),
  createGroup: (name) => post("/api/groups", { name }),
  renameGroup: (from, to) => request("/api/groups", { method: "PATCH", body: JSON.stringify({ from, to }) }),
  deleteGroup: (name) => request("/api/groups", { method: "DELETE", body: JSON.stringify({ name }) }),
  documents: (id) => request(`/api/bases/${id}/documents`),
  documentChildren: (id, parentId = "", { limit = 50, offset = 0 } = {}) =>
    request(`/api/bases/${id}/documents/children?parentId=${encodeURIComponent(parentId)}&limit=${limit}&offset=${offset}`),
  document: (id) => request(`/api/documents/${id}?includeChunks=false`),
  documentWithChunks: (id) => request(`/api/documents/${id}`),
  updateDocument: (id, title) => request(`/api/documents/${id}`, { method: "PATCH", body: JSON.stringify({ title }) }),
  chunks: (id, limit = 20, offset = 0) => request(`/api/documents/${id}/chunks?limit=${limit}&offset=${offset}`),
  addText: (baseID, body) => post(`/api/bases/${baseID}/documents`, body),
  submitOperation: (body, idempotencyKey) => post("/api/operations", {
    ...body, idempotencyKey,
  }, { headers: { "Idempotency-Key": idempotencyKey } }),
  operation: (id) => request(`/api/operations/${id}`),
  operations: ({ states = [], baseId = "", documentId = "", limit = 100 } = {}) => {
    const query = new URLSearchParams();
    states.forEach((state) => query.append("state", state));
    if (baseId) query.set("baseId", baseId);
    if (documentId) query.set("documentId", documentId);
    query.set("limit", String(limit));
    return request(`/api/operations?${query.toString()}`);
  },
  cancelOperation: (id) => post(`/api/operations/${id}/cancel`, {}),
  retryOperation: (id) => post(`/api/operations/${id}/retry`, {}),
  createUpload: (body) => post("/api/uploads", body),
  putUploadContent: (id, file) => request(`/api/uploads/${id}/content`, {
    method: "PUT",
    body: file,
    headers: { "content-type": "application/octet-stream" },
    timeoutMs: 600000,
  }),
  completeUpload: (id) => post(`/api/uploads/${id}/complete`, {}),
  addURL: (baseID, body) => post(`/api/bases/${baseID}/url`, body),
  addFiles: (baseID, body) => post(`/api/bases/${baseID}/files`, body),
  importDirectory: (baseID, path) => post(`/api/bases/${baseID}/import-directory`, { path }),
  refreshURL: (id) => post(`/api/documents/${id}/refresh`, {}),
  rescanDirectory: (id) => post(`/api/documents/${id}/rescan`, {}),
  reindexDocument: (id) => post(`/api/documents/${id}/reindex`, {}),
  reindexBase: (id) => post(`/api/bases/${id}/reindex`, {}),
  deleteDocument: (id) => request(`/api/documents/${id}`, { method: "DELETE" }),
  deleteDocumentJob: (id) => post(`/api/documents/${id}/delete`, {}),
  deleteDirectoryJob: (id) => post(`/api/documents/${id}/delete-tree-job`, {}),
  deleteDocuments: (ids) => post("/api/documents/delete", { ids }),
  reindexDocuments: (ids) => post("/api/documents/reindex", { ids }),
  deleteDirectory: (id) => post(`/api/documents/${id}/delete-tree`, {}),
  rawText: async (id) => {
    const response = await fetch(`/api/documents/${id}/raw?inline=1`);
    if (!response.ok) throw new Error(`raw preview failed: HTTP ${response.status}`);
    return await response.text();
  },
  job: (id) => request(`/api/jobs/${id}`),
  cancelJob: (id) => post(`/api/jobs/${id}/cancel`, {}),
  search: (body) => post("/api/search", body),
  searchHistory: (limit = 20) => request(`/api/search-history?limit=${limit}`),
  deleteSearchHistory: (id) => request(`/api/search-history/${id}`, { method: "DELETE" }),
  clearSearchHistory: () => request("/api/search-history", { method: "DELETE" }),
  scope: () => request("/api/scope"),
  updateScope: (body) => request("/api/scope", { method: "PUT", body: JSON.stringify(body) }),
  config: () => request("/api/config"),
  updateConfig: (body) => request("/api/config", { method: "PUT", body: JSON.stringify(body) }),
  probeEmbedding: (body) => post("/api/probe-embedding-dimensions", body, { timeoutMs: 120000 }),
  probeRerank: (body) => post("/api/probe-rerank", body, { timeoutMs: 120000 }),
  probeCaption: (body) => post("/api/probe-caption", body, { timeoutMs: 180000 }),
  suggestions: () => request("/api/model-suggestions"),
  localModels: (baseID = "") => request(baseID ? `/api/local-models?baseId=${encodeURIComponent(baseID)}` : "/api/local-models"),
  ocrModel: () => request("/api/ocr/model"),
  downloadOCRModel: () => post("/api/ocr/model/download", {}),
  removeOCRModel: () => post("/api/ocr/model/remove", {}),
  runtimeStatus: () => request("/api/runtime-status"),
  downloadModel: (body) => post("/api/local-models/download", body),
  removeModel: (id) => post("/api/local-models/remove", { id }),
  registerReranker: (id) => post("/api/local-rerankers", { id }),
  selfTestReranker: (id) => post("/api/local-models/self-test", { id }),
  planCacheMigration: (targetDir) => request(`/api/local-models/cache-migration?targetDir=${encodeURIComponent(targetDir)}`),
  migrateCache: (body) => post("/api/local-models/cache-migration", body),
  ollamaModels: () => request("/api/ollama/models"),
  pullOllama: (model) => post("/api/ollama/pull", { model }),
  deleteOllama: (model) => post("/api/ollama/delete", { model }),
};

export function fileToBase64(file, onProgress = () => {}) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result).split(",")[1] ?? "");
    reader.onerror = () => reject(reader.error ?? new Error(`Cannot read ${file.name}`));
    reader.onprogress = (event) => {
      if (event.lengthComputable) onProgress(event.loaded, event.total);
    };
    reader.readAsDataURL(file);
  });
}

export async function fileSha256(file) {
  const digest = await crypto.subtle.digest("SHA-256", await file.arrayBuffer());
  return [...new Uint8Array(digest)].map((value) => value.toString(16).padStart(2, "0")).join("");
}
