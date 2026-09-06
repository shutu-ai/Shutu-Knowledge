class ApiError extends Error {
  constructor(message, status, code) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

async function request(path, options = {}) {
  const response = await fetch(path, {
    ...options,
    headers: { "content-type": "application/json", ...(options.headers ?? {}) },
  });
  const payload = await response.json().catch(() => ({}));
  if (payload.ok === undefined) return payload;
  if (!payload.ok) {
    throw new ApiError(payload.error?.message ?? `HTTP ${response.status}`, response.status, payload.error?.code);
  }
  return payload.value;
}

const post = (path, body) => request(path, { method: "POST", body: JSON.stringify(body) });

export const api = {
  request,
  status: () => request("/api/status"),
  stats: (baseID = "") => request(baseID ? `/api/bases/${baseID}/stats` : "/api/stats"),
  bases: () => request("/api/bases"),
  createBase: (body) => post("/api/bases", body),
  updateBase: (id, body) => request(`/api/bases/${id}`, { method: "PATCH", body: JSON.stringify(body) }),
  deleteBase: (id) => request(`/api/bases/${id}`, { method: "DELETE" }),
  groups: () => request("/api/groups"),
  createGroup: (name) => post("/api/groups", { name }),
  renameGroup: (from, to) => request("/api/groups", { method: "PATCH", body: JSON.stringify({ from, to }) }),
  deleteGroup: (name) => request("/api/groups", { method: "DELETE", body: JSON.stringify({ name }) }),
  documents: (id) => request(`/api/bases/${id}/documents`),
  document: (id) => request(`/api/documents/${id}?includeChunks=false`),
  documentWithChunks: (id) => request(`/api/documents/${id}`),
  updateDocument: (id, title) => request(`/api/documents/${id}`, { method: "PATCH", body: JSON.stringify({ title }) }),
  chunks: (id, limit = 20, offset = 0) => request(`/api/documents/${id}/chunks?limit=${limit}&offset=${offset}`),
  addText: (baseID, body) => post(`/api/bases/${baseID}/documents`, body),
  addURL: (baseID, body) => post(`/api/bases/${baseID}/url`, body),
  addFiles: (baseID, body) => post(`/api/bases/${baseID}/files`, body),
  importDirectory: (baseID, path) => post(`/api/bases/${baseID}/import-directory`, { path }),
  refreshURL: (id) => post(`/api/documents/${id}/refresh`, {}),
  rescanDirectory: (id) => post(`/api/documents/${id}/rescan`, {}),
  reindexDocument: (id) => post(`/api/documents/${id}/reindex`, {}),
  reindexBase: (id) => post(`/api/bases/${id}/reindex`, {}),
  deleteDocument: (id) => request(`/api/documents/${id}`, { method: "DELETE" }),
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
  suggestions: () => request("/api/model-suggestions"),
  localModels: () => request("/api/local-models"),
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

export function fileToBase64(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result).split(",")[1] ?? "");
    reader.onerror = () => reject(reader.error ?? new Error(`Cannot read ${file.name}`));
    reader.readAsDataURL(file);
  });
}
