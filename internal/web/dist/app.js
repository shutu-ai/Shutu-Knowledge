import { api, fileSha256, setRouteSignal } from "./api.js";
import { applyI18n, currentLocale, formatDate, formatNumber, initializeI18n, localized, observeI18n } from "./i18n.js";

const navigation = [
  ["overview", "Overview"],
  ["bases", "Knowledge Bases"],
  ["documents", "Documents"],
  ["import", "Import"],
  ["knowledge", "Knowledge"],
  ["recall", "Recall Test"],
  ["models", "Models"],
  ["settings", "Settings"],
];

const state = {
  route: "overview", bases: [], selectedBaseId: localStorage.getItem("knowledge-base") ?? "", docFolder: "",
  docOffset: 0, docLimit: 50,
  modelJobs: {}, documentJobs: {}, chunkExpansionAll: false, expandedChunks: new Set(), docExpandedFolders: new Set(),
};
const screen = document.getElementById("screen");
const toastNode = document.getElementById("toast");
let toastTimer;
let documentRefreshPromise;
let documentListRefresh;
let routeGeneration = 0;
let routeAbortController;
const MAX_IMPORT_FILES = 20;
const SUPPORTED_IMPORT_EXTENSIONS = ".txt,.md,.markdown,.mdx,.csv,.html,.htm,.json,.log,.pdf,.docx,.doc,.pptx,.ppt,.xlsx,.xlsm,.xls,.epub";

function h(tag, attributes = {}, ...children) {
  const element = document.createElement(tag);
  for (const [key, value] of Object.entries(attributes)) {
    if (value == null || value === false) continue;
    if (key === "class") element.className = value;
    else if (key === "html") element.innerHTML = value;
    else if (key.startsWith("on") && typeof value === "function") element.addEventListener(key.slice(2), value);
    else element.setAttribute(key, value === true ? "" : String(value));
  }
  for (const child of children.flat(Infinity)) {
    if (child == null || child === false) continue;
    element.append(child.nodeType ? child : document.createTextNode(child));
  }
  return element;
}

const icon = (path) => h("span", { class: "icon", "aria-hidden": "true", html: `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">${path}</svg>` });
const icons = {
  plus: `<path d="M12 5v14M5 12h14"/>`,
  refresh: `<path d="M21 12a9 9 0 1 1-3-6.7L21 8M21 3v5h-5"/>`,
  trash: `<path d="M4 7h16M9 7V5h6v2m-8 0 1 13h8l1-13"/>`,
  search: `<circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/>`,
  copy: `<rect width="13" height="13" x="8" y="8" rx="2"/><path d="M4 16V6a2 2 0 0 1 2-2h10"/>`,
  history: `<path d="M3 12a9 9 0 1 0 9-9 9 9 0 0 0-7.5 4"/><path d="M3 4v4h4M12 7v5l4 2"/>`,
  save: `<path d="M5 4h11l4 4v12H5z"/><path d="M8 4v5h7V4M8 20v-6h8v6"/>`,
};

function showToast(message, error = false) {
  const dismiss = () => {
    toastNode.style.display = "none";
    clearTimeout(toastTimer);
  };
  toastNode.replaceChildren(
    h("span", { class: "toast-message" }, localized(message)),
    h("button", {
      class: "toast-dismiss",
      type: "button",
      "aria-label": "Dismiss notification",
      onclick: dismiss,
    }, "×"),
  );
  toastNode.className = `toast${error ? " error" : ""}`;
  toastNode.style.display = "flex";
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { toastNode.style.display = "none"; }, 4200);
}

async function guard(action, successMessage) {
  try {
    const value = await action();
    if (successMessage) showToast(successMessage);
    return value;
  } catch (error) {
    showToast(error.message, true);
    throw error;
  }
}

const selectedBase = () => state.bases.find((base) => base.id === state.selectedBaseId);
const operationStore = new Map();
const number = formatNumber;
const date = formatDate;
const chip = (value = "unknown") => h("span", { class: `chip ${String(value).toLowerCase().replace(/\s+/g, "-")}` }, localized(value));

function pageTitle(route) {
  return navigation.find(([id]) => id === route)?.[1] ?? "Overview";
}

function setHeader(subtitle, actions = []) {
  document.getElementById("page-title").textContent = localized(pageTitle(state.route));
  document.getElementById("page-subtitle").textContent = subtitle;
  const topActions = document.getElementById("top-actions");
  topActions.replaceChildren(...actions);
}

function renderBasePickerOptions(select) {
  select.replaceChildren(
    h("option", { value: "" }, "All knowledge bases"),
    ...state.bases.map((base) => h("option", { value: base.id, selected: base.id === state.selectedBaseId }, base.name)),
  );
  select.value = state.selectedBaseId || "";
}

function basePicker(onChange = () => {}) {
  const select = h("select", { onchange: (event) => onChange(event.target.value), "aria-label": "Knowledge base" });
  renderBasePickerOptions(select);
  const refresh = h("button", {
    class: "button small",
    type: "button",
    title: "Refresh bases",
    onclick: async () => {
      try {
        state.bases = await api.bases();
        if (!state.bases.some((base) => base.id === state.selectedBaseId)) syncBasePicker("");
        renderBasePickerOptions(select);
        showToast("Knowledge bases refreshed");
      } catch (error) {
        showToast(error.message, true);
      }
    },
  }, icon(icons.refresh), "Refresh bases");
  return h("div", { class: "toolbar base-picker" }, select, refresh);
}

function syncBasePicker(value) {
  state.selectedBaseId = value ?? state.selectedBaseId;
  localStorage.setItem("knowledge-base", state.selectedBaseId);
}

function metric(label, value) {
  return h("div", { class: "metric" }, [h("strong", {}, number(value)), h("span", {}, label)]);
}

function documentJobPercent(job) {
  const progress = Number(job.progress ?? 0);
  const total = Number(job.total ?? 0);
  if (total <= 0) return 0;
  return Math.max(0, Math.min(100, progress / total * 100));
}

function documentJobPercentText(percent) {
  if (percent > 0 && percent < 0.1) return "<0.1%";
  if (percent < 10) return `${percent.toFixed(1)}%`;
  if (percent < 100) return `${Math.round(percent)}%`;
  return `${percent}%`;
}

function documentJobIndeterminate(job) {
  return !["done", "failed", "cancelled"].includes(job.status) && Number(job.total ?? 0) <= 0;
}

function documentJobCountText(job) {
  if (Number(job.total ?? 0) > 0) return `${Number(job.progress ?? 0)} / ${Number(job.total ?? 0)}`;
  return localized(["importing", "submitting"].includes(job.phase) ? "Waiting for import" : "Waiting for scan count");
}

function documentJobPhaseText(job) {
  if (job.phase === "queued") return localized("Queued for disk");
  if (job.phase === "scanning") return localized("Scanning directory");
  if (job.phase === "loading") return localized("Loading file");
  if (job.phase === "submitting") return localized("Submitting files");
  if (job.phase === "importing") return localized("Importing document");
  if (job.phase === "parsing") return localized("Parsing file");
  if (job.phase === "embedding") return localized("Embedding file");
  if (job.phase === "reindexing") return localized("Reindexing file");
  if (job.phase === "deleting") return localized("Deleting document");
  if (job.kind === "delete_directory") return localized("Deleting directory");
  if (job.kind === "rescan_directory") return localized("Rescanning directory");
  if (job.kind === "import_directory") return localized("Importing directory");
  if (job.kind === "reindex_documents") return localized("Reindexing files");
  return localized("Processing");
}

function documentJobCard(id, job) {
  const percent = documentJobPercent(job);
  const indeterminate = documentJobIndeterminate(job);
  const operation = operationStore.get(id)?.operation;
  const action = documentJobAction(id, operation);
  return h("article", { class: "document-job", "data-document-job-id": id }, [
    h("div", { class: "document-job-head" }, [
      h("div", { class: "document-job-copy" }, [
        h("strong", { class: "truncate" }, job.label || id),
        h("span", { class: "muted truncate", "data-document-job-file": "" }, job.file || documentJobPhaseText(job)),
      ]),
      h("span", { class: `chip document-job-status ${job.status || "pending"}`, "data-document-job-status": "" }, localized(job.status || "pending")),
    ]),
    h("div", { class: "document-job-progress-row" }, [
      h("span", { class: "muted", "data-document-job-phase": "" }, documentJobPhaseText(job)),
      h("strong", { class: "document-job-percent", "data-document-job-percent": "" }, indeterminate ? localized("In progress") : documentJobPercentText(percent)),
    ]),
    h("div", {
      class: `progress document-job-progress${indeterminate ? " indeterminate" : ""}`,
      role: "progressbar", "aria-label": "Document processing progress", "aria-valuemin": "0", "aria-valuemax": "100",
      ...(indeterminate ? { "aria-valuetext": documentJobPhaseText(job) } : { "aria-valuenow": String(percent), "aria-valuetext": documentJobPercentText(percent) }),
    }, h("span", { class: `document-job-fill${indeterminate ? " indeterminate" : ""}`, style: `width:${percent}%` })),
    h("div", { class: "document-job-foot" }, [
      h("span", { class: "muted", "data-document-job-count": "" }, documentJobCountText(job)),
      h("span", { class: "document-job-actions", "data-document-job-actions": "" }, action),
    ]),
  ]);
}

function documentJobAction(id, operation) {
  if (operation?.state === "failed" && operation.retryable) {
    return h("button", { class: "button small", onclick: () => guard(async () => {
      const retry = await api.retryOperation(id);
      await trackOperation(retry, operationStore.get(id)?.label);
    }) }, [icon(icons.refresh), localized("Retry")]);
  }
  if (["failed", "cancelled"].includes(operation?.state)) {
    return h("span", { class: "muted" }, localized(operation.state));
  }
  return h("button", { class: "button small danger", onclick: () => guard(async () => {
    await (operation ? api.cancelOperation(id) : api.cancelJob(id));
  }) }, localized("Cancel"));
}

function updateDocumentJobCard(id, job, label) {
  const card = [...document.querySelectorAll("[data-document-job-id]")]
    .find((node) => node.getAttribute("data-document-job-id") === id);
  if (!card) return;
  const percent = documentJobPercent(job);
  const indeterminate = documentJobIndeterminate(job);
  const statusNode = card.querySelector("[data-document-job-status]");
  const fileNode = card.querySelector("[data-document-job-file]");
  const phaseNode = card.querySelector("[data-document-job-phase]");
  const percentNode = card.querySelector("[data-document-job-percent]");
  const countNode = card.querySelector("[data-document-job-count]");
  const progressNode = card.querySelector(".document-job-progress");
  const fillNode = card.querySelector(".document-job-fill");
  const actionNode = card.querySelector("[data-document-job-actions]");
  if (statusNode) {
    statusNode.textContent = localized(job.status || "pending");
    statusNode.className = `chip document-job-status ${job.status || "pending"}`;
  }
  if (fileNode) fileNode.textContent = job.file || documentJobPhaseText(job);
  if (phaseNode) phaseNode.textContent = documentJobPhaseText(job);
  if (percentNode) percentNode.textContent = indeterminate ? localized("In progress") : documentJobPercentText(percent);
  if (countNode) countNode.textContent = documentJobCountText(job);
  if (progressNode) {
    progressNode.classList.toggle("indeterminate", indeterminate);
    if (indeterminate) {
      progressNode.removeAttribute("aria-valuenow");
      progressNode.setAttribute("aria-valuetext", documentJobPhaseText(job));
    } else {
      progressNode.setAttribute("aria-valuenow", String(percent));
      progressNode.setAttribute("aria-valuetext", documentJobPercentText(percent));
    }
  }
  if (fillNode) {
    fillNode.classList.toggle("indeterminate", indeterminate);
    fillNode.style.width = `${percent}%`;
  }
  if (actionNode) {
    const operation = operationStore.get(id)?.operation;
    actionNode.replaceChildren(documentJobAction(id, operation));
  }
  syncDocumentJobSection();
  if (label) {
    const title = card.querySelector(".document-job-copy strong");
    if (title) title.textContent = label;
  }
}

function documentJobSection() {
  const entries = Object.entries(state.documentJobs);
  const activeCount = entries.filter(([, job]) => !["done", "failed", "cancelled", "succeeded", "interrupted"].includes(job.status)).length;
  return h("section", { class: "section", "data-document-jobs-section": "", hidden: !entries.length }, [
    h("div", { class: "section-head" }, [
      h("h2", {}, localized("Document tasks")),
      h("span", { class: "chip processing", "data-document-job-summary": "" }, `${activeCount} ${localized("active tasks")}`),
    ]),
    h("div", { class: "document-jobs", "data-document-jobs-container": "" }, entries.map(([id, job]) => documentJobCard(id, job))),
  ]);
}

function operationPhaseText(operation) {
  if (operation.state === "queued") return localized("Queued for disk");
  if (operation.phase === "fetching") return localized("Loading file");
  if (operation.phase === "scanning") return localized("Scanning directory");
  if (operation.phase === "deleting") return localized("Deleting document");
  if (operation.phase === "importing") return localized("Importing document");
  if (operation.phase === "parsing") return localized("Parsing file");
  if (operation.phase === "embedding") return localized("Embedding file");
  if (operation.phase === "reindexing") return localized("Reindexing file");
  if (operation.phase === "deleting") return localized("Deleting document");
  return localized("Processing");
}

function rememberOperation(operation, label = "") {
  const previous = operationStore.get(operation.operationId) || {};
  const entry = {
    ...previous,
    operation,
    label: label || previous.label || operation.operationId,
    stateRevision: Math.max(Number(previous.stateRevision || 0), Number(operation.stateRevision || 0)),
  };
  operationStore.set(operation.operationId, entry);
  const terminal = ["succeeded", "failed", "cancelled", "interrupted"].includes(operation.state);
  if (!terminal) {
    state.documentJobs[operation.operationId] = {
      label: entry.label, kind: operation.type, status: operation.state,
      progress: operation.completedUnits || 0, total: operation.totalUnits || 0,
      phase: operation.phase || operationPhaseText(operation),
      file: "",
    };
  } else if (["failed", "cancelled", "interrupted"].includes(operation.state)) {
    state.documentJobs[operation.operationId] = {
      label: entry.label, kind: operation.type, status: operation.state,
      progress: operation.completedUnits || 0, total: operation.totalUnits || 0,
      phase: operation.phase || operationPhaseText(operation), file: "",
    };
  }
  return entry;
}

function operationJobView(operation) {
  return {
    status: operation.state, progress: operation.completedUnits || 0,
    total: operation.totalUnits || 0, phase: operation.phase || operationPhaseText(operation),
  };
}

async function trackOperation(operation, label) {
  let remembered = rememberOperation(operation, label);
  if (state.route === "documents" || state.route === "import") mountDocumentJobSection();
  if (remembered.pollPromise) return remembered.pollPromise;
  remembered.pollPromise = (async () => {
    let current = operation;
    for (;;) {
      if (Number(current.stateRevision) >= remembered.stateRevision) {
        remembered = rememberOperation(current, label);
        updateDocumentJobCard(current.operationId, operationJobView(current), label);
      }
      if (["succeeded", "failed", "cancelled", "interrupted"].includes(current.state)) {
        refreshDocumentViewInBackground();
        if (current.state === "succeeded") {
          delete state.documentJobs[current.operationId];
          removeDocumentJobCard(current.operationId);
        }
        if (current.state !== "succeeded") {
          throw new Error(current.errorMessage || `Operation ${current.state}`);
        }
        return current;
      }
      await new Promise((resolve) => setTimeout(resolve, 300));
      current = await api.operation(current.operationId);
    }
  })();
  try {
    return await remembered.pollPromise;
  } finally {
    const stored = operationStore.get(operation.operationId);
    if (stored === remembered) remembered.pollPromise = null;
    if (remembered.operation?.state === "succeeded") {
      delete state.documentJobs[operation.operationId];
      removeDocumentJobCard(operation.operationId);
    }
  }
}

async function recoverOperations() {
  const { operations } = await api.operations({ states: ["queued", "running", "cancelling"] });
  for (const operation of operations ?? []) {
    trackOperation(operation, operation.type).catch((error) => showToast(error.message, true));
  }
}

function isAbortError(error) {
  return error?.name === "AbortError" || error?.code === "request_aborted";
}

function syncDocumentJobSection() {
  const section = document.querySelector("[data-document-jobs-section]");
  if (!section) return;
  const count = Object.values(state.documentJobs)
    .filter((job) => !["done", "failed", "cancelled", "succeeded", "interrupted"].includes(job.status)).length;
  section.hidden = Object.keys(state.documentJobs).length === 0;
  const summary = section.querySelector("[data-document-job-summary]");
  if (summary) summary.textContent = `${count} ${localized("active tasks")}`;
}

function removeDocumentJobCard(id) {
  const card = [...document.querySelectorAll("[data-document-job-id]")]
    .find((node) => node.getAttribute("data-document-job-id") === id);
  if (card) card.remove();
  syncDocumentJobSection();
}

function refreshDocumentViewInBackground() {
  if (state.route !== "documents" || !documentListRefresh) return;
  if (documentRefreshPromise) return;
  documentRefreshPromise = documentListRefresh()
    .catch((error) => {
      if (!isAbortError(error)) showToast(error.message, true);
    })
    .finally(() => { documentRefreshPromise = null; });
}

// Mount task UI synchronously. A document import/reindex can hold SQLite while
// it parses or embeds; waiting for a full page render here would hide the task
// card until that work has already finished.
function mountDocumentJobSection() {
  let section = document.querySelector("[data-document-jobs-section]");
  if (!section) {
    section = documentJobSection();
    screen.prepend(section);
    return;
  }
  const container = section.querySelector("[data-document-jobs-container]");
  if (!container) return;
  for (const [id, job] of Object.entries(state.documentJobs)) {
    const mounted = [...container.querySelectorAll("[data-document-job-id]")]
      .some((node) => node.getAttribute("data-document-job-id") === id);
    if (!mounted) container.append(documentJobCard(id, job));
  }
  syncDocumentJobSection();
}

async function trackDocumentJob(id, label, metadata = {}) {
  state.documentJobs[id] = { label, status: "pending", progress: 0, total: 0, ...metadata };
  if (state.route === "documents" || state.route === "import") mountDocumentJobSection();
  for (;;) {
    const job = await api.job(id);
    state.documentJobs[id] = {
      ...state.documentJobs[id], status: job.status, progress: job.progress, total: job.total,
      phase: job.phase || state.documentJobs[id].phase,
      file: job.file || state.documentJobs[id].file,
    };
    updateDocumentJobCard(id, state.documentJobs[id], label);
    if (["done", "failed", "cancelled"].includes(job.status)) {
      const error = job.status === "failed" ? job.error : job.status === "cancelled" ? "Download cancelled" : "";
      delete state.documentJobs[id];
      removeDocumentJobCard(id);
      refreshDocumentViewInBackground();
      if (error) throw new Error(error);
      return job;
    }
    await new Promise((resolve) => setTimeout(resolve, 300));
  }
}

async function runLocalImportTask(label, { total = 0, phase = "importing" } = {}, runner) {
  const id = `file-import-${Date.now()}`;
  const job = { id, label, status: "running", progress: 0, total, phase, file: "" };
  state.documentJobs[id] = job;
  mountDocumentJobSection();
  const update = (patch) => {
    Object.assign(job, patch);
    updateDocumentJobCard(id, job);
  };
  try {
    await runner(update);
    delete state.documentJobs[id];
    removeDocumentJobCard(id);
    refreshDocumentViewInBackground();
  } catch (error) {
    delete state.documentJobs[id];
    removeDocumentJobCard(id);
    refreshDocumentViewInBackground();
    throw error;
  }
}

function table(headers, rows) {
  return h("div", { class: "panel table-wrap" }, h("table", {},
    h("thead", {}, h("tr", {}, headers.map((header) => h("th", { scope: "col" }, header)))),
    h("tbody", {}, rows.length ? rows : h("tr", {}, h("td", { colspan: headers.length, class: "empty" }, "No items yet"))),
  ));
}

function isCurrentRoute(generation) {
  return generation === routeGeneration;
}

function showRouteLoading() {
  setHeader(localized("Loading"));
  showContentLoading();
}

function showContentLoading() {
  screen.replaceChildren(h("section", { class: "section loading-state", "aria-live": "polite" }, [
    h("div", { class: "loading-state-mark", "aria-hidden": "true" }, "···"),
    h("h2", {}, localized("Loading")),
    h("p", { class: "muted" }, localized("Loading page data")),
  ]));
}

function showRouteError(error) {
  screen.replaceChildren(h("section", { class: "section", role: "alert" }, [
    h("h2", {}, localized("Unable to load page")),
    h("p", { class: "muted" }, error?.message || localized("Request failed")),
  ]));
}

async function render(initialQuery = "", generation = ++routeGeneration) {
  const status = await api.status({ signal: routeAbortController?.signal });
  if (!isCurrentRoute(generation)) return;
  const healthChip = chip(status.ready ? "Ready" : "Degraded");
  const searchButton = state.route !== "recall"
    ? h("a", { class: "button", href: "#/recall" }, [icon(icons.search), "Recall Test"]) : null;
  setHeader(`${localized(status.status)} · v${status.version}`, [healthChip, searchButton].filter(Boolean));
  screen.replaceChildren();
  try {
    if (state.route === "models") {
      await renderModels();
      return;
    }
    if (state.route === "overview") await renderOverview();
    if (state.route === "bases") await renderBases();
if (state.route === "documents") await renderDocuments(generation, state.selectedBaseId);
    if (state.route === "import") await renderImport();
    if (state.route === "knowledge") await renderKnowledge();
    if (state.route === "recall") await renderRecall(initialQuery);
    if (state.route === "settings") await renderSettings();
  } catch (error) {
    if (isCurrentRoute(generation)) showRouteError(error);
    throw error;
  }
}

async function renderOverview() {
  const [stats, scope] = await Promise.all([api.stats(), api.scope()]);
  screen.append(
    h("section", { class: "section" }, h("div", { class: "metrics" }, [
      metric("Documents", stats.documentCount), metric("Chunks", stats.chunkCount),
      metric("Characters", stats.charCount), metric("Tokens", stats.tokenCount),
    ])),
    h("section", { class: "section grid-2" }, [
      h("div", {}, [
        h("div", { class: "section-head" }, h("h2", {}, "Knowledge bases")),
        table(["Name", "Documents", "Chunks", "Status"], state.bases.map((base) => h("tr", {},
          h("td", { class: "truncate" }, base.name), h("td", {}, number(base.documentCount)),
          h("td", {}, number(base.chunkCount)), h("td", {}, chip(base.documentCount ? "Ready" : "Empty")),
        ))),
      ]),
      h("div", {}, [
        h("div", { class: "section-head" }, h("h2", {}, "Retrieval scope")),
        h("div", { class: "panel panel-body" }, [
          h("div", { class: "switch" }, h("input", { id: "scope-enabled", type: "checkbox", checked: scope.enabled })),
          h("label", { for: "scope-enabled" }, "Automatic retrieval enabled"),
          h("p", { class: "muted" }, scope.enabledBaseIds?.length ? `${scope.enabledBaseIds.length} bases pinned` : "No pinned scope; all bases are available."),
          h("button", { class: "button primary", onclick: () => guard(async () => {
            const enabled = document.getElementById("scope-enabled").checked;
            const pinned = window.confirm("Pin the current selection? Cancel keeps all bases.");
            await api.updateScope({ enabled, enabledBaseIds: pinned ? [state.selectedBaseId].filter(Boolean) : null });
            await render();
          }, "Scope saved") }, [icon(icons.save), "Save scope"]),
        ]),
      ]),
  ]));
}

async function renderBases() {
  const groups = await api.groups();
  const groupPanel = h("aside", { class: "panel panel-body" }, [
    h("h2", {}, "Groups"),
    h("form", { class: "toolbar", onsubmit: (event) => { event.preventDefault(); guard(async () => {
      await api.createGroup(event.target.name.value); event.target.reset(); await render();
    }, "Group created"); } }, [
      h("input", { name: "name", required: true, placeholder: "New group", "aria-label": "New group" }),
      h("button", { class: "button primary" }, [icon(icons.plus), "Add"]),
    ]),
    h("form", { class: "form-grid", style: "margin-top:12px", onsubmit: (event) => { event.preventDefault(); guard(async () => {
      await api.renameGroup(event.target.from.value, event.target.to.value); await loadBases(); await render();
    }, "Group renamed"); } }, [
      h("label", { class: "field" }, "Group", h("select", { name: "from" }, groups.map((group) => h("option", { value: group }, group)))),
      h("label", { class: "field" }, "New name", h("input", { name: "to", required: true })),
      h("button", { class: "button" }, "Rename"),
    ]),
    h("form", { class: "toolbar", style: "margin-top:12px", onsubmit: (event) => { event.preventDefault(); guard(async () => {
      const name = event.target.name.value;
      if (!window.confirm(`Delete group "${name}"? Member bases become ungrouped.`)) return;
      await api.deleteGroup(name); await loadBases(); await render();
    }, "Group deleted"); } }, [
      h("select", { name: "name", "aria-label": "Delete group" }, groups.map((group) => h("option", { value: group }, group))),
      h("button", { class: "button danger" }, "Delete"),
    ]),
  ]);
  const grouped = new Map([["", []]]);
  groups.forEach((group) => grouped.set(group, []));
  state.bases.forEach((base) => {
    const key = grouped.has(base.group ?? "") ? base.group ?? "" : "";
    grouped.get(key).push(base);
  });
  const baseRows = [...grouped.entries()].flatMap(([group, bases]) => [
    h("div", { class: "group-heading muted mono" }, group || "Ungrouped"),
    ...bases.map((base) => h("div", { class: "list-row" }, [
      h("div", { class: "truncate" }, [
        h("strong", {}, base.name),
        h("div", { class: "muted" }, `${number(base.documentCount)} docs · ${number(base.chunkCount)} chunks${base.description ? ` · ${base.description}` : ""}`),
      ]),
      h("div", { class: "toolbar" }, [
        h("button", { class: "button small", onclick: () => guard(async () => {
          const name = window.prompt("Rename knowledge base", base.name);
          if (!name || name === base.name) return;
          await api.updateBase(base.id, { name }); await loadBases(); await render();
        }, "Base renamed") }, "Rename"),
        h("button", { class: "button small danger", onclick: () => guard(async () => {
          if (!window.confirm(`Delete "${base.name}" and all documents?`)) return;
          const operation = await api.submitOperation({
            type: "delete_base", commandSchemaVersion: 1,
            target: { baseId: base.id }, input: {},
          }, crypto.randomUUID());
          await trackOperation(operation, `delete ${base.name}`);
          if (state.selectedBaseId === base.id) syncBasePicker("");
          await loadBases(); await render();
        }, "Base deleted") }, "Delete"),
      ]),
    ])),
  ]);
    screen.append(h("section", { class: "section split" }, [
      h("div", {}, [
        h("div", { class: "section-head" }, h("h2", {}, "Bases")),
        h("div", { class: "panel list" }, baseRows),
      ]),
      groupPanel,
      h("form", { class: "panel panel-body", onsubmit: (event) => { event.preventDefault(); guard(async () => {
      const form = event.target;
      const base = await api.createBase({ name: form.name.value, description: form.description.value, group: form.group.value, config: {} });
      syncBasePicker(base.id);
      form.reset(); await loadBases(); await render();
    }, "Base created"); } }, [
      h("h2", {}, "New knowledge base"),
      h("div", { class: "form-grid" }, [
        h("label", { class: "field" }, "Name", h("input", { name: "name", required: true })),
        h("label", { class: "field" }, "Group", h("input", { name: "group" })),
      ]),
      h("label", { class: "field" }, "Description", h("textarea", { name: "description" })),
      h("button", { class: "button primary" }, [icon(icons.plus), "Create base"]),
    ]),
  ]));
}

async function renderDocuments(generation = routeGeneration, baseId = state.selectedBaseId) {
  if (!state.selectedBaseId || !state.bases.some((base) => base.id === state.selectedBaseId)) {
    screen.append(h("section", { class: "section" }, [
      h("div", { class: "section-head" }, [
        h("h2", {}, "Documents"),
        basePicker(async (value) => {
          syncBasePicker(value);
          state.docFolder = "";
          state.docPreview = null;
          await render();
        }),
      ]),
      h("div", { class: "empty panel" }, "Select a knowledge base to manage its documents."),
    ]));
    return;
  }
  screen.append(h("section", { class: "section loading-state", "aria-live": "polite" }, [
    h("div", { class: "section-head" }, [h("h2", {}, localized("Documents")), chip("loading")]),
    h("p", { class: "muted" }, localized("Loading document list")),
  ]));
  const page = await api.documentChildren(state.selectedBaseId, state.docFolder, {
    limit: state.docLimit, offset: state.docOffset,
  });
  if (!isCurrentRoute(generation) || baseId !== state.selectedBaseId) return;
  screen.replaceChildren();
  let currentPage = page;
  const rows = page.documents;
  const visibleRows = rows.map((doc) => ({ doc, depth: 0 }));
  const openFolder = (folderID) => {
    state.docFolder = folderID;
    state.docPreview = null;
    state.docOffset = 0;
    render();
  };
  const breadcrumbs = [h("button", { class: "button small", onclick: () => { state.docFolder = ""; state.docPreview = null; state.docOffset = 0; render(); } }, "Root")];
  page.breadcrumbs.forEach((item, index) => breadcrumbs.push(h("button", {
    class: `button small${index === page.breadcrumbs.length - 1 ? " primary" : ""}`,
    onclick: () => { state.docFolder = item.id; state.docPreview = null; state.docOffset = 0; render(); },
  }, item.title)));

  const statusText = (doc) => h("div", {}, [
    chip(doc.status),
    doc.qualityStatus && doc.sourceType !== "directory" ? chip(doc.qualityStatus) : null,
    doc.sourceType !== "directory" ? chip(doc.embeddingReady
      ? "Embedding ready"
      : (doc.status === "processing" && doc.phase === "embedding" ? "Embedding pending" : "Lexical only")) : null,
    h("div", { class: "muted mono" }, `${doc.sourceType === "directory" && doc.status === "processing" ? localized("Scanning directory") : (doc.phase || doc.status)}${doc.status === "processing" ? ` · ${doc.progress}%` : ""}`),
  ]);
  const documentTitle = (doc, depth) => {
    return h("div", { class: "document-tree-title", style: `--tree-depth:${depth}` }, [
      h("span", { class: "document-tree-toggle", "aria-hidden": "true" }),
      h("span", { class: "document-tree-glyph", "aria-hidden": "true" }, doc.sourceType === "directory" ? "▰" : ""),
      h("div", { class: "truncate" }, [
        h("strong", {}, doc.title),
        h("div", { class: "muted" }, doc.sourcePath || doc.fileName || doc.url || `${number(doc.charCount)} chars`),
        doc.errorMessage ? h("div", { class: "muted" }, `${doc.errorCode || "error"}: ${doc.errorMessage}`) : null,
      ]),
    ]);
  };
  const previewPanel = h("section", { class: "section", "aria-live": "polite" });
  const setPreview = (doc, mode) => { state.docPreview = { id: doc.id, mode }; renderPreview(previewPanel, doc, mode); };
  const documentActions = (doc) => h("div", { class: "toolbar" }, [
    doc.sourceType === "directory" ? h("button", { class: "button small", onclick: () => openFolder(doc.id) }, "Open") : null,
    doc.sourceType === "directory" ? h("button", { class: "button small", onclick: () => guard(async () => {
      const operation = await api.submitOperation({
        type: "rescan_directory", commandSchemaVersion: 1,
        target: { baseId: doc.baseId, documentId: doc.id }, input: {},
      }, crypto.randomUUID());
      await trackOperation(operation, `rescan ${doc.title}`);
    }, "Directory rescanned") }, "Rescan") : null,
    doc.sourceType === "url" ? h("button", { class: "button small", onclick: () => guard(async () => {
      const operation = await api.submitOperation({
        type: "refresh_url", commandSchemaVersion: 1,
        target: { baseId: doc.baseId, documentId: doc.id }, input: {},
      }, crypto.randomUUID());
      await trackOperation(operation, `refresh ${doc.title}`);
    }) }, "Refresh") : null,
    doc.sourceType !== "directory" ? h("button", { class: "button small", onclick: () => setPreview(doc, "text") }, "Text") : null,
    doc.sourceType !== "directory" ? h("button", { class: "button small", onclick: () => setPreview(doc, "chunks") }, "Chunks") : null,
    doc.sourceType !== "directory" ? h("button", { class: "button small", onclick: () => setPreview(doc, "structure") }, "Structure") : null,
    h("button", { class: "button small", onclick: () => guard(async () => {
      const title = window.prompt("Rename document", doc.title);
      if (!title || title === doc.title) return;
      await api.updateDocument(doc.id, title); await render();
    }, "Document renamed") }, "Rename"),
    doc.sourceType !== "directory" ? h("button", { class: "button small", onclick: () => guard(async () => {
      const operation = await api.submitOperation({
        type: "reindex_document", commandSchemaVersion: 1,
        target: { baseId: doc.baseId, documentId: doc.id }, input: {},
      }, crypto.randomUUID());
      await trackOperation(operation, `reindex ${doc.title}`);
    }, "Document reindexed") }, "Reindex") : null,
    h("button", { class: "button small danger", onclick: () => guard(async () => {
      const message = doc.sourceType === "directory" ? `Delete folder "${doc.title}" and all nested documents?` : `Delete "${doc.title}"?`;
      if (!window.confirm(message)) return;
      if (doc.sourceType === "directory") {
        const operation = await api.submitOperation({
          type: "delete_directory", commandSchemaVersion: 1,
          target: { baseId: doc.baseId, documentId: doc.id }, input: {},
        }, crypto.randomUUID());
        await trackOperation(operation, `delete ${doc.title}`);
        return;
      }
      const operation = await api.submitOperation({
        type: "delete_document", commandSchemaVersion: 1,
        target: { baseId: doc.baseId, documentId: doc.id }, input: {},
      }, crypto.randomUUID());
      await trackOperation(operation, `delete ${doc.title}`);
    }, "Document deleted") }, "Delete"),
  ]);

  const form = h("form", { class: "section", onsubmit: (event) => { event.preventDefault(); guard(async () => {
    const ids = [...event.target.querySelectorAll("input[name='select']:checked")].map((input) => input.value);
    if (!ids.length) return;
    const operation = await api.submitOperation({
      type: "reindex_documents", commandSchemaVersion: 1,
      target: { baseId: state.selectedBaseId },
      input: { documentIds: ids },
    }, crypto.randomUUID());
    await trackOperation(operation, `reindex ${ids.length} files`);
  }, "Selected documents reindexed"); } }, [
    h("section", { class: "section toolbar" }, [
      basePicker(async (value) => { syncBasePicker(value); state.docFolder = ""; state.docPreview = null; state.docOffset = 0; await render(); }),
      ...breadcrumbs,
      h("button", { class: "button" }, [icon(icons.refresh), "Rebuild selected"]),
      h("button", { class: "button danger", type: "button", onclick: () => guard(async () => {
      const ids = [...document.querySelectorAll("input[name='select']:checked")].map((input) => input.value);
      if (!ids.length || !window.confirm(`Delete ${ids.length} selected documents?`)) return;
        const operation = await api.submitOperation({
          type: "delete_documents", commandSchemaVersion: 1,
          target: { baseId: state.selectedBaseId },
          input: { documentIds: ids },
        }, crypto.randomUUID());
        await trackOperation(operation, `delete ${ids.length} documents`);
      }) }, "Delete selected"),
    ]),
  ]);
  const documentTable = table(["", "Title", "Status", "Source", "Chunks", "Updated", "Actions"], visibleRows.map(({ doc, depth }) => h("tr", {},
      h("td", {}, doc.sourceType !== "directory" ? h("input", { name: "select", type: "checkbox", value: doc.id }) : null),
      h("td", { "data-label": localized("Title") }, documentTitle(doc, depth)),
      h("td", { "data-label": localized("Status") }, statusText(doc)),
      h("td", { "data-label": localized("Source") }, doc.sourceType),
      h("td", { "data-label": localized("Chunks") }, number(doc.chunkCount)),
      h("td", { class: "muted", "data-label": localized("Updated") }, date(doc.updatedAt || doc.createdAt)),
      h("td", { "data-label": localized("Actions") }, documentActions(doc)),
    )));
  documentTable.setAttribute("data-document-table", "");
  const documentRows = documentTable.querySelector("tbody");
  const documentPageSummary = h("span", { class: "muted" },
    `${number(page.offset + 1)}–${number(page.offset + page.documents.length)} / ${number(page.total)}`);
  const previousPage = h("button", {
    class: "button small", disabled: page.offset === 0,
    onclick: () => { state.docOffset = Math.max(0, state.docOffset - state.docLimit); render(); },
  }, "Previous");
  const nextPage = h("button", {
    class: "button small", disabled: !page.hasMore,
    onclick: () => { state.docOffset = state.docOffset + page.documents.length; render(); },
  }, "Next");
  form.append(h("section", { class: "section toolbar" }, [
    documentPageSummary, previousPage, nextPage,
  ]));
  form.append(h("section", { class: "section" }, documentTable));

  const applyDocumentPage = (next) => {
    currentPage = next;
    const selected = new Set([...documentRows.querySelectorAll("input[name='select']:checked")].map((item) => item.value));
    documentRows.replaceChildren(...next.documents.map((doc) => h("tr", {},
      h("td", {}, doc.sourceType !== "directory" ? h("input", {
        name: "select", type: "checkbox", value: doc.id, checked: selected.has(doc.id),
      }) : null),
      h("td", { "data-label": localized("Title") }, documentTitle(doc, 0)),
      h("td", { "data-label": localized("Status") }, statusText(doc)),
      h("td", { "data-label": localized("Source") }, doc.sourceType),
      h("td", { "data-label": localized("Chunks") }, number(doc.chunkCount)),
      h("td", { class: "muted", "data-label": localized("Updated") }, date(doc.updatedAt || doc.createdAt)),
      h("td", { "data-label": localized("Actions") }, documentActions(doc)),
    )));
    documentPageSummary.textContent =
      `${number(next.offset + 1)}–${number(next.offset + next.documents.length)} / ${number(next.total)}`;
    previousPage.disabled = next.offset === 0;
    nextPage.disabled = !next.hasMore;
  };

  const refreshDocumentPage = async () => {
    const baseId = state.selectedBaseId;
    const folderId = state.docFolder;
    const offset = state.docOffset;
    const next = await api.documentChildren(baseId, folderId, {
      limit: state.docLimit, offset,
    });
    if (!isCurrentRoute(routeGeneration) || baseId !== state.selectedBaseId
      || folderId !== state.docFolder || offset !== state.docOffset) return;
    applyDocumentPage(next);
    if (state.docPreview) {
      const previewDoc = next.documents.find((item) => item.id === state.docPreview.id);
      if (previewDoc) renderPreview(previewPanel, previewDoc, state.docPreview.mode);
      else {
        state.docPreview = null;
        previewPanel.replaceChildren();
      }
    }
  };
  documentListRefresh = refreshDocumentPage;
  applyDocumentPage(page);

  screen.append(documentJobSection());
  screen.append(form);
  screen.append(previewPanel);
  if (state.docPreview) {
    const doc = page.documents.find((item) => item.id === state.docPreview.id);
    if (doc) renderPreview(previewPanel, doc, state.docPreview.mode);
  }
}

function stableID(value) {
  let hash = 2166136261;
  for (const byte of String(value)) {
    hash ^= byte.codePointAt(0);
    hash = Math.imul(hash, 16777619);
  }
  return (hash >>> 0).toString(36);
}

function chunkBodyID(doc, chunk) {
  return `chunk-body-${stableID(doc.id)}-${stableID(chunk.id ?? chunk.index)}`;
}

function chunkKey(doc, chunk) {
  return `${doc.id}:${chunk.id ?? chunk.index}`;
}

function chunkIsExpanded(doc, chunk) {
  return state.chunkExpansionAll || state.expandedChunks.has(chunkKey(doc, chunk));
}

function sourceAnchorLabel(anchor = {}) {
  const parts = [];
  if (anchor.page) parts.push(`page ${anchor.page}`);
  if (anchor.slide) parts.push(`slide ${anchor.slide}`);
  if (anchor.sheet) parts.push(`sheet ${anchor.sheet}`);
  if (anchor.cell_range) parts.push(anchor.cell_range);
  if (anchor.section) parts.push(anchor.section);
  if (anchor.paragraph) parts.push(`paragraph ${anchor.paragraph}`);
  if (anchor.block) parts.push(`block ${anchor.block}`);
  if (anchor.bbox) {
    const { x1, y1, x2, y2 } = anchor.bbox;
    parts.push(`bbox ${[x1, y1, x2, y2].map((value) => Number(value).toFixed(1)).join(",")}`);
  }
  return parts.join(" · ");
}

function structureTree(nodes) {
  const children = new Map();
  for (const node of nodes) {
    const parent = node.parent_id || "";
    if (!children.has(parent)) children.set(parent, []);
    children.get(parent).push(node);
  }
  for (const values of children.values()) values.sort((a, b) => (a.order ?? 0) - (b.order ?? 0));
  const renderNode = (node, depth) => {
    const nested = children.get(node.id) ?? [];
    const anchor = sourceAnchorLabel(node.source_anchor);
    const details = [node.type, anchor, node.confidence ? `confidence ${Math.round(node.confidence * 100)}%` : ""].filter(Boolean).join(" · ");
    return h("div", { class: "panel panel-body", style: `margin:8px 0 8px ${Math.min(depth, 8) * 16}px` }, [
      h("div", { class: "toolbar", style: "justify-content:space-between" }, [
        h("strong", {}, node.text || node.type),
        h("span", { class: "muted mono" }, details),
      ]),
      node.text ? h("div", { class: "muted context", style: "margin-top:6px; white-space:pre-wrap" }, node.text.slice(0, 300)) : null,
      nested.length ? h("div", {}, nested.map((child) => renderNode(child, depth + 1))) : null,
    ]);
  };
  return (children.get("") ?? []).map((node) => renderNode(node, 0));
}

function derivedCard(item) {
  const value = item.value ?? item.text ?? item.content ?? "";
  return h("div", { class: "panel panel-body" }, [
    h("div", { class: "toolbar", style: "justify-content:space-between" }, [
      h("strong", {}, item.kind || item.type || "derived knowledge"),
      h("span", { class: "muted mono" }, `${item.model || "unknown"} · ${item.modelVersion || ""}`),
    ]),
    h("div", { class: "context", style: "white-space:pre-wrap; margin-top:6px" }, typeof value === "string" ? value : JSON.stringify(value, null, 2)),
  ]);
}

async function renderPreview(container, doc, mode) {
  container.replaceChildren(h("div", { class: "section-head" }, [
    h("h2", {}, `Preview · ${doc.title}`),
    h("button", { class: "button small", onclick: () => { state.docPreview = null; container.replaceChildren(); } }, "Close"),
  ]));
  try {
    if (mode === "chunks") {
      const result = await api.documentWithChunks(doc.id);
      const chunks = result.chunks ?? [];
      container.append(h("div", { class: "toolbar", style: "justify-content:flex-end; margin:10px 0" }, [
        h("button", {
          class: "button small", "data-chunk-all": "true",
          onclick: () => { state.chunkExpansionAll = !state.chunkExpansionAll; return renderPreview(container, doc, mode); },
        }, state.chunkExpansionAll ? "Collapse all" : "Expand all"),
      ]));
      container.append(h("div", { class: "panel list" }, chunks.length ? chunks.map((chunk) => {
        const expanded = chunkIsExpanded(doc, chunk);
        const bodyID = chunkBodyID(doc, chunk);
        const text = String(chunk.text ?? "");
        const visible = expanded || text.length <= 320 ? text : `${text.slice(0, 320)}…`;
        return h("div", { class: "panel panel-body", style: "margin:10px" }, [
          h("div", { class: "toolbar", style: "justify-content:space-between" }, [
            h("div", { class: "muted mono" }, `#${chunk.index}${chunk.heading ? ` · ${chunk.heading}` : ""}`),
            h("button", {
              class: "button small", "data-chunk-toggle": "true", "aria-controls": bodyID,
              "aria-expanded": String(expanded),
              onclick: () => {
                const key = chunkKey(doc, chunk);
                if (state.expandedChunks.has(key)) state.expandedChunks.delete(key);
                else state.expandedChunks.add(key);
                state.chunkExpansionAll = false;
                return renderPreview(container, doc, mode);
              },
            }, expanded ? "Collapse" : "Expand"),
          ]),
          h("pre", { class: "context", id: bodyID }, visible),
        ]);
      }) : h("div", { class: "empty" }, "No chunks")));
      return;
    }
    if (mode === "structure") {
      const [ir, understanding] = await Promise.all([api.structure(doc.id), api.understanding(doc.id)]);
      const nodes = ir.nodes ?? [];
      const derived = understanding.items ?? [];
      container.append(h("div", { class: "toolbar", style: "justify-content:space-between; margin:10px 0" }, [
        h("span", { class: "muted" }, `${ir.ir_version || "document IR"} · ${nodes.length} nodes · ${ir.parser || "parser unknown"}`),
        h("span", { class: "muted mono" }, ir.parser_version || ""),
      ]));
      if (derived.length) {
        container.append(h("div", { class: "section" }, [h("h3", {}, "Understanding"), h("div", { class: "list" }, derived.map(derivedCard))]));
      }
      container.append(h("div", { class: "section" }, [
        h("h3", {}, "Document structure"),
        nodes.length ? h("div", {}, structureTree(nodes)) : h("div", { class: "empty" }, "No structured nodes"),
      ]));
      return;
    }
    const text = await api.rawText(doc.id);
    container.append(h("pre", { class: "panel panel-body context" }, text.slice(0, 40_000) || "Empty document"));
  } catch (error) {
    container.append(h("div", { class: "panel empty" }, error.message));
  }
}

function importExtension(fileName) {
  const dot = String(fileName).lastIndexOf(".");
  return dot < 0 ? "" : String(fileName).slice(dot + 1).toLowerCase();
}

function isBrowserImportFile(file) {
  if (!file || String(file.name).startsWith("~$")) return false;
  return SUPPORTED_IMPORT_EXTENSIONS.split(",").includes(`.${importExtension(file.name)}`);
}

function browserRelativeParts(file) {
  const relative = String(file.webkitRelativePath || file.name || "").replaceAll("\\", "/");
  return relative.split("/").filter(Boolean);
}

async function importSelectedDirectory(baseID, form, update) {
  const selected = [...form.directoryFiles.files];
  const files = selected.filter(isBrowserImportFile).sort((a, b) => {
    const left = String(a.webkitRelativePath || a.name);
    const right = String(b.webkitRelativePath || b.name);
    return left.localeCompare(right);
  });
  if (!files.length) {
    throw new Error("Select a folder containing supported documents");
  }
  const skipped = selected.length - files.length;
  if (skipped > 0) {
    showToast(`${skipped} unsupported or temporary files skipped`, false);
  }

  // Browser folder pickers expose relative paths, not absolute paths. Build a
  // logical directory tree and upload files beneath it, preserving the folder
  // navigation users expect without requiring access to the local path.
  const directories = new Map();
  const parentForFile = new Map();
  for (const file of files) {
    const parts = browserRelativeParts(file);
    let parentID = "";
    for (let index = 0; index < Math.max(0, parts.length - 1); index += 1) {
      const path = parts.slice(0, index + 1).join("/");
      if (!directories.has(path)) {
        const directory = await api.createDirectory(baseID, {
          title: parts[index], parentDirectoryId: parentID, sourcePath: "",
        });
        directories.set(path, directory.id);
      }
      parentID = directories.get(path);
    }
    parentForFile.set(file, parentID);
  }

  for (const [index, file] of files.entries()) {
    update({ phase: "loading", file: file.webkitRelativePath || file.name, progress: index });
    const checksum = await fileSha256(file);
    const upload = await api.createUpload({
      baseId,
      fileName: file.name,
      expectedSize: file.size,
      expectedSha256: checksum,
    });
    await api.putUploadContent(upload.uploadId, file);
    await api.completeUpload(upload.uploadId);
    update({ phase: "submitting", file: file.webkitRelativePath || file.name, progress: index + 1 });
    const operation = await api.submitOperation({
      type: "import_file",
      commandSchemaVersion: 1,
      target: { baseId },
      input: {
        uploadId: upload.uploadId,
        fileName: file.name,
        conflict: form.conflict.value,
        parentDirectoryId: parentForFile.get(file) || "",
      },
    }, crypto.randomUUID());
    await trackOperation(operation, `import ${file.webkitRelativePath || file.name}`);
  }
}

function browserDirectoryImportForm() {
  const summary = h("p", { class: "muted", "aria-live": "polite" }, "No folder selected");
  const picker = h("input", {
    name: "directoryFiles", type: "file", multiple: true,
    webkitdirectory: true, directory: true, accept: SUPPORTED_IMPORT_EXTENSIONS,
    onchange: (event) => {
      const files = [...event.target.files];
      const supported = files.filter(isBrowserImportFile).length;
      summary.textContent = files.length
        ? `${supported} supported files selected${files.length > supported ? ` · ${files.length - supported} skipped` : ""}`
        : "No folder selected";
    },
  });
  return h("form", { class: "panel panel-body", onsubmit: async (event) => {
    event.preventDefault();
    await guard(async () => {
      const files = [...event.target.directoryFiles.files].filter(isBrowserImportFile);
      await runLocalImportTask(`import folder (${files.length} files)`, { total: files.length, phase: "preparing" },
        (update) => importSelectedDirectory(state.selectedBaseId, event.target, update));
      event.target.reset();
      summary.textContent = "No folder selected";
    }, "Folder imported");
  } }, [
    h("h2", {}, localized("Frontend directory")),
    h("p", { class: "muted" }, "Select the SmartCare product dictionary or Suite folder. Folder structure is preserved."),
    h("label", { class: "field" }, "Local folder", picker),
    summary,
    h("label", { class: "field" }, "Conflict strategy", h("select", { name: "conflict" },
      ["rename", "replace", "keep", "detect"].map((value) => h("option", { value, selected: value === "rename" }, value)))),
    h("button", { class: "button primary" }, localized("Import frontend directory")),
  ]);
}

async function renderImport() {
  if (!state.selectedBaseId) {
    screen.append(h("section", { class: "section" }, [
      h("div", { class: "section-head" }, [
        h("h2", {}, localized("Import")),
        basePicker(async (value) => {
          syncBasePicker(value);
          await render();
        }),
      ]),
      h("div", { class: "empty panel" }, localized("Select a knowledge base before importing.")),
    ]));
    if (!state.bases.length) {
      screen.append(h("section", { class: "section" }, h("div", { class: "empty panel" }, [
        h("p", {}, localized("No knowledge bases are available yet.")),
        h("button", {
          class: "button primary",
          type: "button",
          onclick: () => { location.hash = "#/bases"; },
        }, [icon(icons.plus), localized("Create base")]),
      ])));
    }
    return;
  }
  const base = selectedBase();
  screen.append(h("section", { class: "section toolbar" }, [basePicker(async (value) => { syncBasePicker(value); await render(); })]));
  screen.append(documentJobSection());
  screen.append(h("section", { class: "section grid-2" }, [
    h("form", { class: "panel panel-body", onsubmit: (event) => { event.preventDefault(); guard(async () => {
      const form = event.target;
      const title = form.title.value;
      const content = form.content.value;
      const operation = await api.submitOperation({
        type: "import_text", commandSchemaVersion: 1,
        target: { baseId: state.selectedBaseId },
        input: { title, content },
      }, crypto.randomUUID());
      await trackOperation(operation, `import ${title || "text"}`);
      form.reset(); await render();
    }, "Text imported"); } }, [
      h("h2", {}, "Text"), h("label", { class: "field" }, "Title", h("input", { name: "title", required: true })),
      h("label", { class: "field" }, "Content", h("textarea", { name: "content", required: true })),
      h("button", { class: "button primary" }, "Import text"),
    ]),
    h("form", { class: "panel panel-body", onsubmit: (event) => { event.preventDefault(); guard(async () => {
      const form = event.target;
      const url = form.url.value;
      const title = form.title.value;
      const operation = await api.submitOperation({
        type: "import_url", commandSchemaVersion: 1,
        target: { baseId: state.selectedBaseId },
        input: { url, title },
      }, crypto.randomUUID());
      await trackOperation(operation, `import ${title || url}`);
      form.reset(); await render();
    }, "URL imported"); } }, [
      h("h2", {}, "URL"), h("label", { class: "field" }, "Page URL", h("input", { name: "url", type: "url", required: true })),
      h("label", { class: "field" }, "Title", h("input", { name: "title" })),
      h("button", { class: "button primary" }, "Import URL"),
    ]),
    h("form", { class: "panel panel-body", onsubmit: async (event) => { event.preventDefault(); await guard(async () => {
      const form = event.target;
      const selectedFiles = [...form.files.files];
      if (selectedFiles.length > MAX_IMPORT_FILES) {
        showToast(`At most ${MAX_IMPORT_FILES} files per import; split the selection`, true);
        return;
      }
      const conflict = form.conflict.value;
      await runLocalImportTask(`import ${selectedFiles.length} files`, { total: selectedFiles.length, phase: "loading" }, async (update) => {
        for (const [index, file] of selectedFiles.entries()) {
          update({ phase: "loading", file: file.name, progress: index });
          const checksum = await fileSha256(file);
          const upload = await api.createUpload({
            baseId: state.selectedBaseId,
            fileName: file.name,
            expectedSize: file.size,
            expectedSha256: checksum,
          });
          await api.putUploadContent(upload.uploadId, file);
          await api.completeUpload(upload.uploadId);
          update({ phase: "submitting", file: file.name, progress: index + 1 });
          const operation = await api.submitOperation({
            type: "import_file",
            commandSchemaVersion: 1,
            target: { baseId: state.selectedBaseId },
            input: {
              uploadId: upload.uploadId,
              fileName: file.name,
              conflict,
            },
          }, crypto.randomUUID());
          await trackOperation(operation, `import ${file.name}`);
        }
      });
      form.reset();
    }, "Files imported"); } }, [
      h("h2", {}, "Files"), h("input", { name: "files", type: "file", multiple: true, required: true, accept: SUPPORTED_IMPORT_EXTENSIONS }),
      h("label", { class: "field" }, "Conflict strategy", h("select", { name: "conflict" },
        ["rename", "replace", "keep", "detect"].map((value) => h("option", { value, selected: value === "rename" }, value)))),
      h("button", { class: "button primary" }, "Import files"),
    ]),
    browserDirectoryImportForm(),
    h("form", { class: "panel panel-body", onsubmit: (event) => { event.preventDefault(); guard(async () => {
      const form = event.target;
      const operation = await api.submitOperation({
        type: "import_directory", commandSchemaVersion: 1,
        target: { baseId: state.selectedBaseId },
        input: { path: form.path.value },
      }, crypto.randomUUID());
      await trackOperation(operation, `import ${form.path.value}`);
      form.reset();
    }, "Directory imported"); } }, [
      h("h2", {}, localized("Backend directory")), h("label", { class: "field" }, "Absolute local path", h("input", { name: "path", required: true })),
      h("button", { class: "button primary" }, localized("Import backend directory")),
    ]),
  ]));
}

async function renderKnowledge() {
  if (!state.selectedBaseId) {
    screen.append(h("section", { class: "section" }, [
      h("div", { class: "section-head" }, h("h2", {}, "Knowledge")),
      basePicker(async (value) => { syncBasePicker(value); await render(); }),
      h("div", { class: "empty panel" }, "Select a knowledge base to compile semantic memory."),
    ]));
    return;
  }
  screen.append(h("section", { class: "section toolbar" }, [
    basePicker(async (value) => { syncBasePicker(value); await render(); }),
    h("button", { class: "button primary", onclick: () => guard(async () => {
      await api.compileSemanticMemory(state.selectedBaseId);
      await render();
    }, "Semantic memory compiled") }, [icon(icons.refresh), "Compile semantic memory"]),
  ]));

  let compilation = null;
  try { compilation = await api.semanticCompilation(state.selectedBaseId); }
  catch (error) {
    if (error.status !== 404) throw error;
  }
  const counts = { fact: 0, concept: 0, topic: 0, summary: 0, knowledge_page: 0 };
  (compilation?.units ?? []).forEach((unit) => { counts[unit.type] = (counts[unit.type] ?? 0) + 1; });
  screen.append(h("section", { class: "section" }, [
    h("div", { class: "section-head" }, h("h2", {}, "Compilation status")),
    h("div", { class: "panel panel-body" }, compilation ? [
      h("div", { class: "metrics" }, [
        metric("Generation", compilation.generation), metric("Facts", counts.fact),
        metric("Concepts", counts.concept), metric("Topics", counts.topic),
        metric("Summaries", counts.summary), metric("Relations", compilation.relations?.length ?? 0),
      ]),
      h("p", { class: "muted mono" }, `${compilation.compiler || "compiler"} ${compilation.compilerVersion || ""} · ${compilation.model || ""} ${compilation.modelVersion || ""} · prompt ${compilation.promptVersion || "none"}`),
    ] : h("div", { class: "empty" }, "No active semantic compilation. Existing 0.3 retrieval remains available.")),
  ]));

  if (!compilation) return;
  screen.append(h("section", { class: "section" }, [
    h("div", { class: "section-head" }, h("h2", {}, "Knowledge explorer")),
    table(["Type", "Title", "Version", "Status", "Confidence", "Evidence", "Derived from"], (compilation.units ?? []).slice(0, 100).map((unit) => h("tr", {},
      h("td", {}, chip(unit.type)), h("td", { class: "truncate" }, unit.title),
      h("td", { class: "mono" }, unit.version || "unknown"),
      h("td", {}, chip(unit.metadata?.temporal_status || unit.status || "unknown")),
      h("td", {}, Number(unit.confidence ?? 0).toFixed(2)),
      h("td", {}, number(unit.sources?.length ?? 0)), h("td", {}, number(unit.derivedFrom?.length ?? 0)),
    ))),
  ]));

  const semanticResults = h("section", { class: "section", "aria-live": "polite" });
  const contextResult = h("section", { class: "section", "aria-live": "polite" });
  const queryForm = h("form", { class: "section panel panel-body", onsubmit: (event) => {
    event.preventDefault();
    guard(async () => {
      const query = event.target.query.value;
      const [semantic, context] = await Promise.all([
        api.searchSemanticMemory(state.selectedBaseId, { query, topK: 8 }),
        api.compileKnowledgeContext(state.selectedBaseId, { query, tokenBudget: 2048 }),
      ]);
      semanticResults.replaceChildren(h("div", { class: "section-head" }, h("h2", {}, "Semantic activation")),
        table(["Type", "Unit", "Version", "Temporal status", "Score", "Terms", "Evidence"], semantic.hits.map((hit) => h("tr", {},
          h("td", {}, chip(hit.unit.type)), h("td", { class: "truncate" }, hit.unit.title),
          h("td", { class: "mono" }, hit.unit.version || "unknown"),
          h("td", {}, chip(hit.unit.metadata?.temporal_status || hit.unit.status || "unknown")),
          h("td", {}, Number(hit.score ?? 0).toFixed(2)), h("td", { class: "truncate mono" }, (hit.matchedTerms ?? []).join(", ")),
          h("td", {}, number(hit.evidence?.length ?? 0)),
        ))));
      contextResult.replaceChildren(h("div", { class: "section-head" }, h("h2", {}, "Compiled context")),
        h("div", { class: "panel panel-body" }, [
          h("div", { class: "toolbar" }, [
            chip(context.routing?.intent ?? "fact"),
            chip(context.temporalIntent || "NONE"),
            context.resolvedVersion ? chip(`version ${context.resolvedVersion}`) : "",
            h("span", { class: "muted" }, `${context.estimatedTokens ?? 0}/${context.tokenBudget ?? 0} tokens`),
            h("span", { class: "muted" }, `${context.evidence?.length ?? 0} evidence · ${context.citations?.length ?? 0} citations`),
          ]),
          h("pre", { class: "mono semantic-context" }, context.renderedContext || ""),
        ]));
    });
  } }, [
    h("h2", {}, "Semantic query"),
    h("div", { class: "toolbar" }, [
      h("input", { name: "query", required: true, style: "max-width:520px", placeholder: "Ask global, comparison, multi-hop, temporal, or fact question" }),
      h("button", { class: "button primary" }, [icon(icons.search), "Activate knowledge"]),
    ]),
  ]);
  screen.append(queryForm, semanticResults, contextResult);

  const wikiPanel = h("section", { class: "section" }, h("div", { class: "section-head" }, h("h2", {}, "Living Wiki"),
    h("button", { class: "button small", onclick: () => guard(async () => {
      const wiki = await api.semanticWiki(state.selectedBaseId);
      wikiPanel.replaceChildren(h("div", { class: "section-head" }, h("h2", {}, "Living Wiki")),
        h("div", { class: "panel list" }, (wiki.pages ?? []).map((page) => h("details", { class: "list-row semantic-wiki-page" },
          h("summary", {}, [h("strong", {}, page.title), h("span", { class: "muted" }, ` · ${page.evidence?.length ?? 0} evidence pointers`)]),
          h("pre", { class: "mono semantic-context" }, page.markdown || ""),
        ))));
    }) }, "Regenerate view")));
  screen.append(wikiPanel);
}

async function renderRecall(initialQuery = "") {
    const results = h("section", { class: "section", "aria-live": "polite" });
    const historyPanel = h("section", { class: "section recall-history" });
  const form = h("form", { class: "section panel panel-body", onsubmit: (event) => {
      event.preventDefault();
      runRecall(event.target, results);
    } }, [
      h("div", { class: "toolbar" }, [
        basePicker((value) => syncBasePicker(value)),
        h("input", { name: "query", placeholder: "Ask the imported corpus", required: true, style: "max-width:420px" }),
        h("select", { name: "mode", "aria-label": "Search mode" }, ["auto", "hybrid", "vector", "lexical"].map((mode) => h("option", { value: mode }, mode))),
        h("input", { name: "topK", type: "number", min: "1", max: "50", value: "4", "aria-label": "Top K", style: "width:86px" }),
        h("label", { class: "switch" }, h("input", { name: "mmr", type: "checkbox" }), "MMR"),
        h("button", { class: "button primary" }, [icon(icons.search), "Run retrieval"]),
      ]),
  ]);
    screen.append(form);
    screen.append(historyPanel);
    screen.append(results);
    renderRecallHistory(historyPanel, form, results);
  if (initialQuery.trim()) {
    form.query.value = initialQuery;
    runRecall(form, results);
  }
}

  async function runRecall(form, container) {
    return await guard(async () => {
      const result = await api.search({
      query: form.query.value, baseId: state.selectedBaseId, topK: Number(form.topK.value || 0),
      mode: form.mode.value, mmr: form.mmr.checked,
        });
        renderResults(container, result);
        const historyPanel = document.querySelector(".recall-history");
        if (historyPanel?.reload) await historyPanel.reload();
        return result;
    });
  }

  async function renderRecallHistory(container, form, results) {
    const load = async () => {
      let history = [];
      try {
        history = await api.searchHistory();
      } catch (error) {
        container.replaceChildren(
          h("div", { class: "section-head" }, h("h2", {}, "Query history")),
          h("div", { class: "panel empty" }, error.message),
        );
        return;
      }
      const replay = async (item) => {
        syncBasePicker(item.baseId ?? "");
        form.querySelector('select[aria-label="Knowledge base"]').value = item.baseId ?? "";
        form.query.value = item.query;
        form.mode.value = item.mode;
        form.topK.value = item.topK;
        form.mmr.checked = item.mmr;
        await runRecall(form, results);
        await load();
      };
      container.reload = load;
      container.replaceChildren(
        h("div", { class: "section-head" }, [
          h("h2", {}, [icon(icons.history), " Query history"]),
          h("button", { class: "button small danger", onclick: () => guard(async () => {
            if (!window.confirm("Clear recall query history?")) return;
            await api.clearSearchHistory(); await load();
          }, "Query history cleared") }, "Clear"),
        ]),
        h("div", { class: "panel list" }, history.length ? history.map((item) => h("div", { class: "list-row" }, [
          h("button", { class: "history-replay", onclick: () => guard(() => replay(item)) }, [
            h("strong", { class: "truncate" }, item.query),
            h("span", { class: "muted mono" },
              `${item.mode} · top ${item.topK}${item.mmr ? " · MMR" : ""} · ${item.totalHits} hits · ${date(item.createdAt)}`),
          ]),
          h("button", { class: "button small danger", "aria-label": "Delete query history item", onclick: () => guard(async () => {
            await api.deleteSearchHistory(item.id); await load();
          }) }, "Delete"),
        ])) : h("div", { class: "empty" }, "Run a query to create replayable history")),
      );
    };
    await load();
  }

  function citationText(hit, index) {
    const heading = hit.heading ? ` — ${hit.heading}` : "";
    const citation = hit.citation || {};
    const location = [
      citation.section ? `section=${citation.section}` : "",
      citation.page ? `page=${citation.page}` : "",
      citation.slide ? `slide=${citation.slide}` : "",
      citation.sheet ? `sheet=${citation.sheet}` : "",
      citation.cellRange ? `cell=${citation.cellRange}` : "",
    ].filter(Boolean).join("; ");
    return [
      `[${index + 1}] ${hit.documentTitle || hit.docId}${heading}`,
      `source: baseId=${hit.baseId}; docId=${hit.docId}; chunkId=${hit.chunkId}; chunkIndex=${hit.index}${location ? `; ${location}` : ""}`,
      hit.text,
    ].join("\n");
  }

  async function copyCitation(text) {
    await navigator.clipboard.writeText(text);
  }

function renderResults(container, result) {
  const excerpt = (hit) => {
    const window = hit.contextWindow;
    if (!window) return hit.text;
    const beforeParts = window.before ?? [];
    const afterParts = window.after ?? [];
    const parts = [...beforeParts.map((part) => part.text), window.anchor.text, ...afterParts.map((part) => part.text)];
    const anchorOffset = parts.slice(0, beforeParts.length + 1).reduce((total, text) => total + text.length + 2, 0);
    const text = parts.join("\n\n");
    const before = text.slice(0, anchorOffset);
    const anchor = text.slice(anchorOffset, anchorOffset + window.anchor.text.length);
    const after = text.slice(anchorOffset + window.anchor.text.length);
    return h("pre", { class: "context" }, [before, h("span", { class: "anchor" }, `>>> ${anchor}`), after]);
  };
  container.replaceChildren(
      h("div", { class: "section-head" }, [
        h("h2", {}, `Final context · ${result.total} hits`),
        h("div", { class: "toolbar" }, [
          h("span", { class: "muted mono" }, `${result.mode} · ${result.elapsedMS} ms`),
          h("button", { class: "button small", onclick: () => guard(async () => {
            await copyCitation(result.hits.map(citationText).join("\n\n"));
          }, "All citations copied") }, [icon(icons.copy), "Copy citations"]),
        ]),
      ]),
    result.hits.map((hit, index) => h("article", { class: "panel panel-body", style: "margin-bottom:12px" }, [
        h("div", { class: "hit-head" }, [
          h("strong", {}, `${index + 1}. ${hit.documentTitle || hit.docId}`),
          h("div", { class: "toolbar" }, [
            (() => {
            const scores = [
              `score ${hit.score.toFixed(3)} · lexical ${hit.lexicalScore.toFixed(3)}`,
            hit.fusionScore ? ` · fusion ${hit.fusionScore.toFixed(3)}` : "",
            hit.vectorScore ? ` · vector ${hit.vectorScore.toFixed(3)}` : "",
            hit.rerankScore ? ` · rerank ${hit.rerankScore.toFixed(3)}` : "",
            ].join("");
            return h("span", { class: "mono" }, scores);
            })(),
            h("button", { class: "button small", onclick: () => guard(async () => {
              await copyCitation(citationText(hit, index));
            }, "Citation copied") }, [icon(icons.copy), "Copy"]),
          ]),
        ]),
      excerpt(hit),
        hit.citation ? h("div", { class: "muted mono", "data-search-provenance": "true" }, [
          "provenance: ",
          hit.citation.section ? `section=${hit.citation.section} · ` : "",
          hit.citation.page ? `page=${hit.citation.page} · ` : "",
          hit.citation.slide ? `slide=${hit.citation.slide} · ` : "",
          hit.citation.sheet ? `sheet=${hit.citation.sheet} · ` : "",
          hit.citation.cellRange ? `cell=${hit.citation.cellRange}` : "",
        ]) : null,
        h("div", { class: "muted mono" }, `base ${hit.baseId} · doc ${hit.docId} · chunk ${hit.chunkId}`),
    ])),
    result.rerank ? h("div", { class: "muted mono", style: "margin-bottom:12px" },
      `rerank ${result.rerank.status} · ${result.rerank.model} · ${result.rerank.candidateCount} candidates · ${result.rerank.elapsedMs || 0} ms`) : null,
  );
}

function modelJobPercent(job) {
  const progress = Number(job.progress ?? 0);
  const total = Number(job.total ?? 100);
  if (total <= 0) return Math.max(0, Math.min(100, progress));
  return Math.round(Math.max(0, Math.min(100, progress / total * 100)));
}

function modelJobIsIndeterminate(job) {
  return job.progressMode === "indeterminate" && !["done", "failed", "cancelled"].includes(job.status);
}

function modelJobProgressText(job) {
  return modelJobIsIndeterminate(job) ? localized("In progress") : `${modelJobPercent(job)}%`;
}

function modelJobPhaseText(job) {
  return localized(job.phase || (job.kind === "self-test" ? "Self-test" : "Download progress"));
}

function modelJobTaskText(job) {
  return localized(job.kind === "self-test" ? "Self-test" : "Model download");
}

function modelJobBytes(value) {
  const bytes = Math.max(0, Number(value) || 0);
  if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(1)} GiB`;
  if (bytes >= 1024 ** 2) return `${(bytes / 1024 ** 2).toFixed(1)} MiB`;
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${Math.round(bytes)} B`;
}

function modelJobCountText(job) {
  return modelJobIsIndeterminate(job)
    ? localized("No exact percentage available")
    : Number(job.totalBytes) > 0
      ? `${modelJobBytes(job.completedBytes)} / ${modelJobBytes(job.totalBytes)}`
    : `${Number(job.progress ?? 0)} / ${Number(job.total ?? 100)}`;
}

function modelJobCard(id, job) {
  const status = job.status || "pending";
  const percent = modelJobPercent(job);
  const indeterminate = modelJobIsIndeterminate(job);
  return h("article", { class: "model-job", "data-model-job-id": id }, [
    h("div", { class: "model-job-head" }, [
      h("div", { class: "model-job-title" }, [
        h("span", { class: "model-job-icon", "aria-hidden": "true" }, icon(icons.refresh)),
        h("div", { class: "model-job-copy" }, [
          h("strong", { class: "truncate" }, job.label || id),
          h("span", { class: "muted truncate", "data-model-job-file": "" }, job.file || modelJobTaskText(job)),
        ]),
      ]),
      h("span", { class: `chip model-job-chip ${status}`, "data-model-job-status": "" }, localized(status)),
    ]),
    h("div", { class: "model-job-progress-row" }, [
      h("span", { class: "muted", "data-model-job-phase": "" }, modelJobPhaseText(job)),
      h("strong", { class: "model-job-percent", "data-model-job-percent": "" }, modelJobProgressText(job)),
    ]),
    h("div", {
      class: "progress model-job-progress",
      role: "progressbar",
      "aria-label": "Download progress",
      "aria-valuemin": "0",
      "aria-valuemax": "100",
      "aria-valuetext": indeterminate ? modelJobPhaseText(job) : `${percent}%`,
      ...(indeterminate ? {} : { "aria-valuenow": String(percent) }),
    }, h("span", { class: `model-job-fill${indeterminate ? " indeterminate" : ""}`, style: `width:${percent}%` })),
    h("div", { class: "model-job-foot" }, [
      h("span", { class: "muted", "data-model-job-count": "" }, modelJobCountText(job)),
      h("button", { class: "button small danger", onclick: () => guard(async () => {
        state.modelJobs[id].cancelling = true;
        await api.cancelJob(id);
      }) }, "Cancel"),
    ]),
  ]);
}

function updateModelJobCard(id, job, label) {
  const card = [...document.querySelectorAll("[data-model-job-id]")]
    .find((node) => node.getAttribute("data-model-job-id") === id);
  if (!card) return;
  const status = job.status || "pending";
  const percent = modelJobPercent(job);
  const indeterminate = modelJobIsIndeterminate(job);
  const statusNode = card.querySelector("[data-model-job-status]");
  const percentNode = card.querySelector("[data-model-job-percent]");
  const phaseNode = card.querySelector("[data-model-job-phase]");
  const fileNode = card.querySelector("[data-model-job-file]");
  const countNode = card.querySelector("[data-model-job-count]");
  const progressNode = card.querySelector(".model-job-progress");
  const fillNode = card.querySelector(".model-job-fill");
  if (statusNode) {
    statusNode.textContent = localized(status);
    statusNode.className = `chip model-job-chip ${status}`;
  }
  if (percentNode) percentNode.textContent = modelJobProgressText(job);
  if (phaseNode) phaseNode.textContent = modelJobPhaseText(job);
  if (fileNode) fileNode.textContent = job.file || modelJobTaskText(job);
  if (countNode) countNode.textContent = modelJobCountText(job);
  if (progressNode) {
    progressNode.classList.toggle("indeterminate", indeterminate);
    if (indeterminate) {
      progressNode.removeAttribute("aria-valuenow");
      progressNode.setAttribute("aria-valuetext", modelJobPhaseText(job));
    } else {
      progressNode.setAttribute("aria-valuenow", String(percent));
      progressNode.setAttribute("aria-valuetext", `${percent}%`);
    }
  }
  if (fillNode) {
    fillNode.classList.toggle("indeterminate", indeterminate);
    fillNode.style.width = `${percent}%`;
  }
  if (label) {
    const title = card.querySelector(".model-job-copy strong");
    if (title) title.textContent = label;
  }
}

function syncModelJobSection() {
  const section = document.querySelector("[data-model-jobs-section]");
  const container = document.querySelector("[data-model-jobs-container]");
  if (!section || !container) return;
  const count = Object.keys(state.modelJobs).length;
  section.hidden = count === 0;
  const summary = section.querySelector("[data-model-job-summary]");
  if (summary) summary.textContent = localized(`${count} active task${count === 1 ? "" : "s"}`);
}

function mountModelJobCard(id, job) {
  const container = document.querySelector("[data-model-jobs-container]");
  if (!container) return;
  const existing = [...container.querySelectorAll("[data-model-job-id]")]
    .find((node) => node.getAttribute("data-model-job-id") === id);
  if (existing) {
    updateModelJobCard(id, job, job.label);
  } else {
    container.append(modelJobCard(id, job));
  }
  syncModelJobSection();
}

function removeModelJobCard(id) {
  const card = [...document.querySelectorAll("[data-model-job-id]")]
    .find((node) => node.getAttribute("data-model-job-id") === id);
  card?.remove();
  syncModelJobSection();
}

function modelJobRow(id, job) {
  const percent = modelJobPercent(job);
  const indeterminate = modelJobIsIndeterminate(job);
  return h("div", { class: "list-row model-row model-row-downloading", "data-model-job-row": id }, [
    h("div", { class: "model-job-row-copy" }, [
      h("strong", { class: "truncate" }, job.modelId || job.label || id),
      h("div", { class: "model-meta" }, [
        h("span", { class: `chip ${job.kind === "self-test" ? "processing" : "downloading"}`, "data-model-job-row-status": "" }, localized(job.kind === "self-test" ? "testing" : "downloading")),
        h("span", { class: "muted", "data-model-job-row-phase": "" }, `${localized(job.kind || "embedding")} · ${modelJobPhaseText(job)}`),
      ]),
    ]),
    h("div", { class: "model-job-row-progress" }, [
      h("strong", { class: "model-inline-percent", "data-model-job-row-percent": "" }, modelJobProgressText(job)),
      h("div", { class: `progress model-inline-progress${indeterminate ? " indeterminate" : ""}` }, h("span", { class: `model-inline-fill${indeterminate ? " indeterminate" : ""}`, style: `width:${percent}%` })),
    ]),
  ]);
}

function updateModelJobRow(id, job) {
  const row = [...document.querySelectorAll("[data-model-job-row]")]
    .find((node) => node.getAttribute("data-model-job-row") === id);
  if (!row) return;
  const percent = modelJobPercent(job);
  const indeterminate = modelJobIsIndeterminate(job);
  const percentNode = row.querySelector("[data-model-job-row-percent]");
  const phaseNode = row.querySelector("[data-model-job-row-phase]");
  const progressNode = row.querySelector(".model-inline-progress");
  const fillNode = row.querySelector(".model-inline-fill");
  if (percentNode) percentNode.textContent = modelJobProgressText(job);
  if (phaseNode) phaseNode.textContent = `${localized(job.kind || "embedding")} · ${modelJobPhaseText(job)}`;
  if (progressNode) progressNode.classList.toggle("indeterminate", indeterminate);
  if (fillNode) {
    fillNode.classList.toggle("indeterminate", indeterminate);
    fillNode.style.width = `${percent}%`;
  }
}

function mountModelJobRow(id, job) {
  if (!job.modelId) return;
  const list = document.querySelector("[data-local-models-list]");
  if (!list) return;
  const existing = [...list.querySelectorAll("[data-model-job-row]")]
    .find((node) => node.getAttribute("data-model-job-row") === id);
  if (existing) updateModelJobRow(id, job);
  else list.append(modelJobRow(id, job));
}

function removeModelJobRow(id) {
  const row = [...document.querySelectorAll("[data-model-job-row]")]
    .find((node) => node.getAttribute("data-model-job-row") === id);
  row?.remove();
}

function normalizeLocalModelId(value) {
  return String(value ?? "").trim().replace(/^local:/i, "");
}

function installedLocalModelChoices(models, kind) {
  const seen = new Set();
  return models
    .filter((model) => {
      const status = String(model.status ?? "").toLowerCase();
      const lifecycle = String(model.lifecycle ?? "").toUpperCase();
      return model.kind === kind
        && !["registered", "not-downloaded", "incomplete"].includes(status)
        && (status === "installed" || model.ready || ["READY", "INSTALLED", "LOADING"].includes(lifecycle));
    })
    .map((model) => ({ id: normalizeLocalModelId(model.id), status: model.status, lifecycle: model.lifecycle }))
    .filter((model) => model.id && !seen.has(model.id) && seen.add(model.id))
    .sort((left, right) => left.id.localeCompare(right.id));
}

function localModelOptions(kind, choices, current) {
  const currentId = normalizeLocalModelId(current);
  const options = choices.map((model) => h("option", {
    value: model.id,
    selected: model.id === currentId,
  }, model.id));
  if (currentId && !choices.some((model) => model.id === currentId)) {
    options.unshift(h("option", { value: currentId, selected: true }, `${currentId} · ${localized("Current setting not found locally")}`));
  }
  if (!options.length) {
    options.push(h("option", { value: "", selected: true, disabled: true }, localized(`No installed ${kind} models`)));
  }
  return options;
}

function modelField(slot, name, kind, current, local, choices) {
  const control = local
    ? h("select", { name, required: true }, localModelOptions(kind, choices, current))
    : h("input", { name, value: current ?? "", placeholder: "org/model-name" });
  slot.replaceChildren(h("label", { class: "field" }, [
    h("span", { class: "field-label" }, localized("Model")),
    control,
    h("span", { class: "field-help" }, localized(local ? "Select an installed local model" : "Remote model identifier")),
  ]));
}

function urlField(slot, name, current, local, kind) {
  slot.replaceChildren(local
    ? h("div", { class: "model-field-note" }, localized(`${kind} local models need no URL`))
    : h("label", { class: "field" }, [
      h("span", { class: "field-label" }, localized("Base URL")),
      h("input", { name, value: current ?? "", placeholder: "http://127.0.0.1:11434/v1" }),
      h("span", { class: "field-help" }, localized("Remote URL only; leave blank for local models")),
    ]));
}

async function renderModels() {
  const base = selectedBase();
  const config = base?.config ?? {};
  const localModels = await api.localModels(state.selectedBaseId);
  const localEmbeddingModels = installedLocalModelChoices(localModels.models, "embedding");
  const localRerankModels = installedLocalModelChoices(localModels.models, "rerank");
  const ocrModel = await api.ocrModel();
  let ocrRuntime = { status: {} };
  try {
    ocrRuntime = await api.runtimeStatus();
  } catch {
    ocrRuntime = { status: { ocr: { ready: false, details: { error: "runtime status unavailable" } } } };
  }
  let ollamaModels = [];
  let ollamaError = "";
  try {
    const ollama = await api.ollamaModels();
    if (!ollama.available) ollamaError = ollama.error || "Ollama is unavailable";
    else ollamaModels = ollama.models;
  } catch (error) {
    ollamaError = error.message;
  }
  const jobCards = Object.entries(state.modelJobs).map(([id, job]) => modelJobCard(id, job));
  const activeModelJobs = Object.entries(state.modelJobs).filter(([, job]) => job.modelId && job.kind !== "self-test");
  const embeddingProvider = h("select", { name: "provider" }, ["openai", "ollama", "local", "none"].map((value) => h("option", { value, selected: config.embeddingProvider === value }, value)));
  const rerankMode = h("select", { name: "rerankMode" }, [
    ["local", "Local"], ["remote", "Remote"],
  ].map(([value, label]) => h("option", { value, selected: (config.rerankBaseUrl ? "remote" : "local") === value }, localized(label))));
  const embeddingModelSlot = h("div", { class: "model-config-slot" });
  const embeddingURLSlot = h("div", { class: "model-config-slot" });
  const rerankModelSlot = h("div", { class: "model-config-slot" });
  const rerankURLSlot = h("div", { class: "model-config-slot" });
  const embeddingTestStatus = h("span", { class: "model-test-status muted", "aria-live": "polite" });
  const rerankTestStatus = h("span", { class: "model-test-status muted", "aria-live": "polite" });
  const embeddingTestButton = h("button", { class: "button small", type: "button", onclick: async (event) => {
    const button = event.currentTarget;
    const form = button.closest("form");
    button.disabled = true;
    embeddingTestStatus.className = "model-test-status muted testing";
    embeddingTestStatus.textContent = localized("Testing configuration");
    try {
      const result = await api.probeEmbedding({
        provider: form.provider.value,
        baseUrl: form.elements.embeddingBaseUrl?.value ?? "",
        model: form.elements.embeddingModel?.value ?? "",
        apiKey: form.elements.embeddingApiKey?.value ?? "",
      });
      embeddingTestStatus.className = "model-test-status success";
      embeddingTestStatus.textContent = `${localized("Test passed")} · ${result.dimensions} ${localized("dimensions")}`;
    } catch (error) {
      embeddingTestStatus.className = "model-test-status error";
      embeddingTestStatus.textContent = `${localized("Test failed")}: ${error.message}`;
    } finally {
      button.disabled = false;
    }
  } }, localized("Test configuration"));
  const rerankTestButton = h("button", { class: "button small", type: "button", onclick: async (event) => {
    const button = event.currentTarget;
    const form = button.closest("form");
    button.disabled = true;
    rerankTestStatus.className = "model-test-status muted testing";
    rerankTestStatus.textContent = localized("Testing configuration");
    try {
      const result = await api.probeRerank({
        baseUrl: form.elements.rerankBaseUrl?.value ?? "",
        model: form.elements.rerankModel?.value ?? "",
        apiKey: form.elements.rerankApiKey?.value ?? "",
      });
      rerankTestStatus.className = "model-test-status success";
      rerankTestStatus.textContent = `${localized("Test passed")} · ${result.scores.length} ${localized("scores")}`;
    } catch (error) {
      rerankTestStatus.className = "model-test-status error";
      rerankTestStatus.textContent = `${localized("Test failed")}: ${error.message}`;
    } finally {
      button.disabled = false;
    }
  } }, localized("Test configuration"));
  const providerForm = h("form", { class: "panel panel-body model-config-form", onsubmit: (event) => { event.preventDefault(); guard(async () => {
    const form = event.target;
    const embeddingIsLocal = form.provider.value === "local";
    const rerankIsLocal = form.rerankMode.value === "local";
    await api.updateBase(state.selectedBaseId, { config: {
      ...config,
      embeddingProvider: form.provider.value,
      embeddingBaseUrl: embeddingIsLocal ? "" : (form.elements.embeddingBaseUrl?.value ?? ""),
      embeddingModel: embeddingIsLocal ? normalizeLocalModelId(form.elements.embeddingModel?.value) : (form.elements.embeddingModel?.value ?? ""),
      rerankEnabled: true,
      rerankModel: rerankIsLocal ? normalizeLocalModelId(form.elements.rerankModel?.value) : (form.elements.rerankModel?.value ?? ""),
      rerankBaseUrl: rerankIsLocal ? "" : (form.elements.rerankBaseUrl?.value ?? ""),
    } });
    await loadBases(); await render();
  }, "Model configuration saved"); } }, [
    h("div", { class: "model-provider-grid" }, [
      h("section", { class: "model-provider-card" }, [
        h("div", { class: "model-provider-card-head" }, [h("div", { class: "eyebrow" }, "EMBEDDING"), h("h3", {}, localized("Embedding model"))]),
        h("label", { class: "field" }, [h("span", { class: "field-label" }, localized("Provider")), embeddingProvider]),
        embeddingModelSlot,
        embeddingURLSlot,
        h("div", { class: "model-test-row" }, [embeddingTestButton, embeddingTestStatus]),
      ]),
      h("section", { class: "model-provider-card" }, [
        h("div", { class: "model-provider-card-head" }, [h("div", { class: "eyebrow" }, "RERANK"), h("h3", {}, localized("Rerank model"))]),
        h("label", { class: "field" }, [h("span", { class: "field-label" }, localized("Source")), rerankMode]),
        rerankModelSlot,
        rerankURLSlot,
        h("div", { class: "model-test-row" }, [rerankTestButton, rerankTestStatus]),
      ]),
    ]),
    h("div", { class: "model-config-actions" }, [
      h("span", { class: "model-config-note" }, localized("Local models are selected from the installed model list; URL is only for remote providers.")),
      h("button", { class: "button primary" }, [icon(icons.save), localized("Save providers")]),
    ]),
  ]);
  const refreshProviderFields = () => {
    const embeddingIsLocal = embeddingProvider.value === "local";
    const rerankIsLocal = rerankMode.value === "local";
    const currentEmbeddingModel = providerForm.elements.embeddingModel?.value ?? config.embeddingModel ?? "";
    const currentRerankModel = providerForm.elements.rerankModel?.value ?? config.rerankModel ?? "";
    const currentEmbeddingURL = providerForm.elements.embeddingBaseUrl?.value ?? config.embeddingBaseUrl ?? "";
    const currentRerankURL = providerForm.elements.rerankBaseUrl?.value ?? config.rerankBaseUrl ?? "";
    modelField(embeddingModelSlot, "embeddingModel", "embedding", currentEmbeddingModel, embeddingIsLocal, localEmbeddingModels);
    modelField(rerankModelSlot, "rerankModel", "rerank", currentRerankModel, rerankIsLocal, localRerankModels);
    urlField(embeddingURLSlot, "embeddingBaseUrl", currentEmbeddingURL, embeddingIsLocal || embeddingProvider.value === "none", "Embedding");
    urlField(rerankURLSlot, "rerankBaseUrl", currentRerankURL, rerankIsLocal, "Rerank");
  };
  embeddingProvider.addEventListener("change", refreshProviderFields);
  rerankMode.addEventListener("change", refreshProviderFields);
  refreshProviderFields();
  screen.replaceChildren();
  screen.append(h("section", { class: "section" }, [
    h("div", { class: "section-head" }, [h("h2", {}, localized("Model selection")), basePicker(async (value) => { syncBasePicker(value); await render(); })]),
    providerForm,
  ]));
  const activeJobs = Object.values(state.modelJobs).length;
  screen.append(h("section", { class: "section", "data-model-jobs-section": "", hidden: !jobCards.length }, [
    h("div", { class: "section-head" }, [
      h("h2", {}, "Model tasks"),
      h("span", { class: "chip processing", "data-model-job-summary": "" }, `${activeJobs} active task${activeJobs === 1 ? "" : "s"}`),
    ]),
    h("div", { class: "model-jobs", "data-model-jobs-container": "" }, jobCards),
  ]));
  screen.append(h("section", { class: "section grid-2" }, [
    h("div", {}, [
      h("div", { class: "section-head" }, [h("h2", {}, "OCR model"), chip(ocrModel.status)]),
      h("div", { class: "panel panel-body" }, [
        h("div", { class: "toolbar", style: "justify-content:space-between; margin-bottom:10px" }, [
          h("div", {}, [
            h("strong", { class: "truncate" }, ocrModel.id),
            h("div", { class: "muted mono" }, `${ocrModel.artifacts.join(" · ")}`),
          ]),
          h("div", { class: "toolbar" }, [
            ocrModel.status !== "not-downloaded" ? h("button", { class: "button small danger", onclick: () => guard(async () => {
              if (!window.confirm(`Delete OCR model "${ocrModel.id}"?`)) return;
              const job = await api.removeOCRModel();
              trackJob(job.jobId, `remove ocr ${ocrModel.id}`, { modelId: ocrModel.id, kind: "ocr-remove", progressMode: job.progressMode || "determinate" }).catch((error) => showToast(error.message, true));
              await render();
            }, "OCR model removed") }, [icon(icons.trash), "Delete"]) : null,
            h("button", { class: "button small primary", onclick: () => guard(async () => {
              const job = await api.downloadOCRModel();
              trackJob(job.jobId, `ocr ${ocrModel.id}`, { modelId: ocrModel.id, kind: "ocr" }).catch((error) => showToast(error.message, true));
              await render();
            }) }, [icon(icons.refresh), "Download"]),
          ]),
        ]),
        h("div", { class: "muted" }, ocrModel.missing?.length
          ? `Missing artifacts: ${ocrModel.missing.join(", ")}`
          : `All artifacts installed${ocrModel.downloadedAt ? ` · ${date(ocrModel.downloadedAt)}` : ""}`),
        h("div", { class: "muted", style: "margin-top:8px" }, ocrRuntime.status.ocr
          ? `Runtime helper: ${ocrRuntime.status.ocr.ready ? "ready" : "not ready"}`
          : "Runtime helper: not configured"),
        h("div", { class: "muted" }, ocrModel.status === "installed" && ocrRuntime.status.ocr?.ready
          ? "OCR artifacts and helper are ready"
          : "OCR remains unavailable until both are ready"),
      ]),
      h("div", { class: "section-head" }, [h("h2", {}, "Local model files"), h("span", { class: "muted mono" }, localModels.cacheDir)]),
        h("div", { class: "panel list", "data-local-models-list": "" }, [
          ...(localModels.models.length ? localModels.models.map((model) => h("div", { class: "list-row model-row" }, [
          h("div", {}, [
            h("strong", { class: "truncate" }, model.id),
            h("div", { class: "model-meta" }, [
              (() => {
                const runtimeReady = model.ready || model.lifecycle === "READY";
                return h("span", { class: `chip ${runtimeReady ? "ready" : (model.status || "unknown")}` }, localized(runtimeReady ? "READY" : (model.status || "unknown")));
              })(),
              h("span", { class: "muted" }, `${localized(model.kind)} · ${model.sizeBytes ? `${number(model.sizeBytes)} bytes` : localized("Managed runtime cache")}`),
              !(model.ready || model.lifecycle === "READY") && localized(model.lifecycle || "INSTALLED") !== localized(model.status || "unknown")
                ? h("span", { class: "muted mono" }, localized(model.lifecycle || "INSTALLED")) : null,
              model.runtimeStatus ? h("span", { class: "muted mono" }, localized(model.runtimeStatus)) : null,
              model.selfTest ? h("span", { class: "muted" }, model.selfTest.current ? "self-test passed" : model.selfTest.healthy ? "stale self-test" : "self-test failed") : model.kind === "rerank" ? h("span", { class: "muted" }, "self-test required") : null,
            ]),
          ]),
          h("div", { class: "toolbar" }, [
          model.kind === "rerank" ? (() => {
            const selfTestRunning = Object.values(state.modelJobs).some((job) => job.kind === "self-test" && job.selfTestModelId === model.id);
            return h("button", { class: "button small", disabled: selfTestRunning, onclick: () => guard(async () => {
              const job = await api.selfTestReranker(model.id);
              trackJob(job.jobId, `self-test ${model.id}`, {
                modelId: model.id, kind: "self-test", selfTestModelId: model.id,
                progressMode: job.progressMode || "indeterminate",
              }).catch((error) => showToast(error.message, true));
              await render();
            }) }, selfTestRunning ? "Self-test running" : "Self-test");
          })() : null,
            h("button", { class: "button small danger", onclick: () => guard(async () => {
              if (!window.confirm(`Delete local model "${model.id}"?`)) return;
              const job = await api.removeModel(model.id);
              trackJob(job.jobId, `remove ${model.id}`, { modelId: model.id, kind: "model-remove", progressMode: job.progressMode || "determinate" }).catch((error) => showToast(error.message, true));
              await render();
            }, "Model removed") }, "Delete"),
          ]),
        ])) : []),
          ...activeModelJobs.map(([id, job]) => modelJobRow(id, job)),
          ...(!localModels.models.length && !activeModelJobs.length ? [h("div", { class: "empty" }, "No local model files")] : []),
        ]),
          h("form", { class: "panel panel-body form-grid", style: "margin-top:12px", onsubmit: (event) => { event.preventDefault(); guard(async () => {
            const form = event.target;
            await api.registerReranker(form.customReranker.value);
            showToast("Custom reranker registered; download artifacts and run self-test");
            form.reset(); await render();
          }, "Custom reranker registered"); } }, [
            h("label", { class: "field" }, "Custom local reranker", h("input", { name: "customReranker", required: true, placeholder: "owner/model-name" })),
            h("button", { class: "button primary" }, [icon(icons.plus), "Register"]),
          ]),
          h("form", { class: "panel panel-body form-grid", style: "margin-top:12px", onsubmit: (event) => { event.preventDefault(); guard(async () => {
        const form = event.target;
        const body = { id: form.id.value, kind: form.kind.value };
        if (form.artifacts.value) body.artifacts = form.artifacts.value.split(",").map((value) => value.trim()).filter(Boolean);
        const job = await api.downloadModel(body);
        trackJob(job.jobId, `${body.kind} ${body.id}`, { modelId: body.id, kind: body.kind, progressMode: job.progressMode || "indeterminate" }).catch((error) => showToast(error.message, true));
        form.reset();
      }); } }, [
        h("label", { class: "field" }, "Hugging Face model", h("input", { name: "id", required: true, placeholder: "org/model-name" })),
        h("label", { class: "field" }, "Kind", h("select", { name: "kind" },
          ["embedding", "rerank"].map((value) => h("option", { value }, value)))),
        h("label", { class: "field" }, "Artifacts", h("input", { name: "artifacts", placeholder: "Leave blank for defaults" })),
          h("button", { class: "button primary" }, [icon(icons.refresh), "Download model"]),
        ]),
        h("form", { class: "panel panel-body form-grid", style: "margin-top:12px", onsubmit: (event) => { event.preventDefault(); guard(async () => {
          const form = event.target;
          const removeSource = form.removeSource.checked;
          if (removeSource && !window.confirm("Move the model cache and remove the source models after verification?")) return;
          const operation = await api.submitOperation({
            type: "migrate_model_cache", commandSchemaVersion: 1,
            target: {},
            input: { targetDir: form.targetDir.value, removeSource },
          }, crypto.randomUUID());
          await trackJob(operation.operationId, "model cache migration", { kind: "cache-migration" });
          showToast("Model cache migrated");
          form.reset(); await render();
        }, "Model cache migrated"); } }, [
          h("label", { class: "field" }, "New cache directory", h("input", { name: "targetDir", type: "text", required: true, placeholder: "/absolute/path/to/models" })),
          h("label", { class: "switch" }, h("input", { name: "removeSource", type: "checkbox" }), "Remove source after verification"),
          h("button", { class: "button primary" }, [icon(icons.save), "Migrate cache"]),
        ]),
      ]),
    ollamaPanel(ollamaModels, ollamaError),
  ]));
}

function ollamaPanel(ollamaModels, ollamaError) {
  return h("div", {}, [
    h("div", { class: "section-head" }, h("h2", {}, "Ollama")),
    h("div", { class: "panel list" }, ollamaError ? h("div", { class: "empty" }, ollamaError) :
      ollamaModels.length ? ollamaModels.map((model) => h("div", { class: "list-row" }, [
        h("div", {}, [h("strong", { class: "truncate" }, model.name), h("div", { class: "muted" }, `${number(model.size)} bytes`)]),
        h("button", { class: "button small danger", onclick: () => guard(async () => {
          if (!window.confirm(`Delete Ollama model "${model.name}"?`)) return;
          const job = await api.deleteOllama(model.name);
          trackJob(job.jobId, `remove ollama ${model.name}`, { modelId: model.name, kind: "ollama-remove", progressMode: job.progressMode || "determinate" }).catch((error) => showToast(error.message, true));
          await render();
        }, "Ollama model deleted") }, "Delete"),
      ])) : h("div", { class: "empty" }, "No Ollama models")),
    h("form", { class: "panel panel-body toolbar", style: "margin-top:12px", onsubmit: (event) => { event.preventDefault(); guard(async () => {
      const form = event.target;
      const job = await api.pullOllama(form.model.value);
      trackJob(job.jobId, `ollama ${form.model.value}`).catch((error) => showToast(error.message, true));
      form.reset();
    }); } }, [
      h("label", { class: "field" }, "Model", h("input", { name: "model", required: true, placeholder: "nomic-embed-text" })),
      h("button", { class: "button primary" }, [icon(icons.refresh), "Pull model"]),
    ]),
  ]);
}

async function trackJob(id, label, metadata = {}) {
  state.modelJobs[id] = { label, status: "pending", progress: 0, total: 100, ...metadata };
  if (state.route === "models") {
    mountModelJobCard(id, state.modelJobs[id]);
    mountModelJobRow(id, state.modelJobs[id]);
  }
  for (;;) {
    const job = await api.job(id);
    state.modelJobs[id] = {
      ...state.modelJobs[id], status: job.status, progress: job.progress, total: job.total || 100,
      phase: job.phase || state.modelJobs[id].phase,
      file: job.file || state.modelJobs[id].file,
      completedBytes: job.completedBytes ?? state.modelJobs[id].completedBytes,
      totalBytes: job.totalBytes ?? state.modelJobs[id].totalBytes,
    };
    updateModelJobCard(id, state.modelJobs[id], label);
    updateModelJobRow(id, state.modelJobs[id]);
    if (["done", "failed", "cancelled"].includes(job.status)) {
      const error = job.status === "failed" ? job.error : job.status === "cancelled" ? "Download cancelled" : "";
      delete state.modelJobs[id];
      if (state.route === "models") {
        removeModelJobCard(id);
        removeModelJobRow(id);
        render().catch((refreshError) => showToast(refreshError.message, true));
      }
      if (error) throw new Error(error);
      return job;
    }
    await new Promise((resolve) => setTimeout(resolve, 350));
  }
}

async function renderSettings() {
  const numberField = (name, label, value) => h("label", { class: "field" }, label, h("input", { name, type: "number", value: value ?? "" }));
  const settingsGroup = (title, description, fields) => h("section", { class: "settings-group" }, [
    h("div", { class: "settings-group-head" }, [
      h("div", {}, h("h3", {}, title), h("p", { class: "settings-group-description" }, description)),
    ]),
    h("div", { class: "settings-group-body form-grid" }, fields),
  ]);
  const settings = await api.config();
  const config = settings.config;
  const suggestions = await api.suggestions();
  const captionTestStatus = h("span", { class: "model-test-status muted", "aria-live": "polite" });
  const captionTestButton = h("button", { class: "button small", type: "button", onclick: async (event) => {
    const button = event.currentTarget;
    const form = button.closest("form");
    button.disabled = true;
    captionTestStatus.className = "model-test-status muted testing";
    captionTestStatus.textContent = localized("Testing configuration");
    try {
      const result = await api.probeCaption({
        provider: form.elements.captionProvider.value,
        baseUrl: form.elements.captionBaseUrl.value,
        model: form.elements.captionModel.value,
        apiKey: form.elements.captionApiKey.value,
      });
      captionTestStatus.className = "model-test-status success";
      const preview = String(result.caption || "").replace(/\s+/g, " ").trim();
      captionTestStatus.textContent = `${localized("Test passed")}${preview ? ` · ${preview.slice(0, 90)}` : ""}`;
    } catch (error) {
      captionTestStatus.className = "model-test-status error";
      captionTestStatus.textContent = `${localized("Test failed")}: ${error.message}`;
    } finally {
      button.disabled = false;
    }
  } }, localized("Test configuration"));
  const globalForm = h("form", { class: "section panel panel-body", onsubmit: (event) => { event.preventDefault(); guard(async () => {
    const form = event.target;
    const next = {
      ...config,
      embedding: { ...config.embedding, provider: form.embeddingProvider.value, baseUrl: form.embeddingBaseUrl.value, model: form.embeddingModel.value },
      rerank: { ...config.rerank, enabled: form.rerankEnabled.checked, model: form.rerankModel.value, baseUrl: form.rerankBaseUrl.value },
      chunking: { ...config.chunking, smart: form.smartChunk.checked, separator: form.chunkSeparator.value, size: Number(form.chunkSize.value || 0), overlap: Number(form.chunkOverlap.value || 0), semantic: form.semanticChunk.checked, semanticThreshold: Number(form.semanticThreshold.value || 0), tokenLimit: Number(form.chunkTokenLimit.value || 0) },
      retrieval: { ...config.retrieval, topK: Number(form.topK.value || 0), mode: form.searchMode.value, similarityMin: Number(form.similarityMin.value || 0), mmr: form.mmr.checked, mmrDiversity: Number(form.mmrDiversity.value || 0), rrfVectorWeight: Number(form.rrfWeight.value || 0), siblingChunks: Number(form.siblingChunks.value || 0) },
      processing: { ...config.processing, provider: form.processor.value, apiHost: form.mineruHost.value },
      workflow: { ...config.workflow, conflictStrategy: form.workflowConflict.value, urlRefreshHours: Number(form.workflowRefreshHours.value || 0) },
      autoRetrieve: { ...config.autoRetrieve, enabled: form.autoRetrieve.checked, weight: Number(form.autoRetrieveWeight.value || 0) },
      captioning: { ...config.captioning, provider: form.captionProvider.value, model: form.captionModel.value, baseUrl: form.captionBaseUrl.value },
      models: { ...config.models, cacheDir: form.modelCacheDir.value, hfEndpoint: form.hfEndpoint.value },
      jobs: { ...config.jobs, importWorkers: Number(form.importWorkers.value || 0), resumeInterrupt: form.resumeInterrupt.checked },
      ocr: { ...config.ocr, renderHelper: form.ocrRenderHelper.value },
      helpers: { ...config.helpers, legacyOffice: form.legacyOffice.value, contentConverter: form.contentConverter.value, imageDecoder: form.imageDecoder.value },
    };
    const body = { config: next };
    if (form.embeddingApiKey.value) body.embeddingApiKey = form.embeddingApiKey.value;
    if (form.rerankApiKey.value) body.rerankApiKey = form.rerankApiKey.value;
    if (form.mineruApiKey.value) body.mineruApiKey = form.mineruApiKey.value;
    if (form.captionApiKey.value) body.captionApiKey = form.captionApiKey.value;
    if (form.clearEmbeddingApiKey.checked) body.clearEmbeddingApiKey = true;
    if (form.clearRerankApiKey.checked) body.clearRerankApiKey = true;
    if (form.clearMineruApiKey.checked) body.clearMineruApiKey = true;
    if (form.clearCaptionApiKey.checked) body.clearCaptionApiKey = true;
    await api.updateConfig(body); await render();
  }, "Global settings saved"); } }, [
    h("div", { class: "settings-intro" }, [
      h("div", {}, [h("div", { class: "eyebrow" }, "KNOWLEDGE DEFAULTS"), h("h2", {}, "Global settings"), h("p", {}, "These defaults apply to every knowledge base unless a base has its own override.")]),
      h("span", { class: "chip" }, "Global"),
    ]),
    h("div", { class: "settings-groups" }, [
      settingsGroup("Models & AI", "Embedding and reranking providers used by default for new imports and retrieval.", [
      h("label", { class: "field" }, "Embedding provider", h("select", { name: "embeddingProvider" },
        ["none", "openai", "ollama", "local"].map((value) => h("option", { value, selected: config.embedding.provider === value }, value)))),
      h("label", { class: "field" }, "Embedding base URL", h("input", { name: "embeddingBaseUrl", value: config.embedding.baseUrl ?? "" })),
      h("label", { class: "field" }, "Embedding model", h("input", { name: "embeddingModel", value: config.embedding.model ?? "", list: "embedding-suggestions" })),
      h("label", { class: "field" }, "Embedding API key", h("input", { name: "embeddingApiKey", type: "password", placeholder: settings.embeddingApiKeySet ? "Configured" : "" })),
      h("label", { class: "switch" }, h("input", { name: "clearEmbeddingApiKey", type: "checkbox" }), "Clear embedding key"),
      h("label", { class: "switch" }, h("input", { name: "rerankEnabled", type: "checkbox", checked: config.rerank.enabled }), "Reranker enabled"),
      h("label", { class: "field" }, "Rerank model", h("input", { name: "rerankModel", value: config.rerank.model ?? "", list: "rerank-suggestions" })),
      h("label", { class: "field" }, "Rerank base URL", h("input", { name: "rerankBaseUrl", value: config.rerank.baseUrl ?? "" })),
      h("label", { class: "field" }, "Rerank API key", h("input", { name: "rerankApiKey", type: "password", placeholder: settings.rerankApiKeySet ? "Configured" : "" })),
      h("label", { class: "switch" }, h("input", { name: "clearRerankApiKey", type: "checkbox" }), "Clear rerank key"),
      ]),
      settingsGroup("Chunking & indexing", "Control how source text is split and prepared for the index.", [
      h("label", { class: "switch" }, h("input", { name: "smartChunk", type: "checkbox", checked: config.chunking.smart }), "Smart chunking"),
      h("label", { class: "field" }, "Chunk separator", h("input", { name: "chunkSeparator", value: config.chunking.separator ?? "" })),
      numberField("chunkSize", "Chunk size", config.chunking.size), numberField("chunkOverlap", "Chunk overlap", config.chunking.overlap),
      h("label", { class: "switch" }, h("input", { name: "semanticChunk", type: "checkbox", checked: config.chunking.semantic }), "Semantic chunking"),
      numberField("semanticThreshold", "Semantic threshold", config.chunking.semanticThreshold),
      numberField("chunkTokenLimit", "Chunk token limit", config.chunking.tokenLimit),
      ]),
      settingsGroup("Retrieval", "Tune result count, vector search, hybrid fusion, and diversity behavior.", [
      numberField("topK", "Top K", config.retrieval.topK),
      h("label", { class: "field" }, "Search mode", h("select", { name: "searchMode" },
        ["auto", "hybrid", "vector", "lexical"].map((value) => h("option", { value, selected: config.retrieval.mode === value }, value)))),
      numberField("similarityMin", "Similarity minimum", config.retrieval.similarityMin),
      h("label", { class: "switch" }, h("input", { name: "mmr", type: "checkbox", checked: config.retrieval.mmr }), "MMR"),
      numberField("mmrDiversity", "MMR diversity", config.retrieval.mmrDiversity),
      numberField("rrfWeight", "RRF vector weight", config.retrieval.rrfVectorWeight),
      numberField("siblingChunks", "Sibling chunks", config.retrieval.siblingChunks),
      ]),
      settingsGroup("Document processing", "Choose the parser used for difficult files and configure MinerU when enabled.", [
      h("label", { class: "field" }, "Document processor", h("select", { name: "processor" },
        ["builtin", "mineru"].map((value) => h("option", { value, selected: config.processing.provider === value }, value)))),
      h("label", { class: "field" }, "MinerU API host", h("input", { name: "mineruHost", value: config.processing.apiHost ?? "" })),
      h("label", { class: "field" }, "MinerU API key", h("input", { name: "mineruApiKey", type: "password", placeholder: settings.mineruApiKeySet ? "Configured" : "" })),
      h("label", { class: "switch" }, h("input", { name: "clearMineruApiKey", type: "checkbox" }), "Clear MinerU key"),
      ]),
      settingsGroup("Workflow & automatic retrieval", "Set conflict handling, URL refresh, and whether the Agent may retrieve automatically.", [
      h("label", { class: "field" }, "Conflict strategy", h("select", { name: "workflowConflict" },
        ["rename", "replace", "keep"].map((value) => h("option", { value, selected: config.workflow.conflictStrategy === value }, value)))),
      numberField("workflowRefreshHours", "URL refresh hours", config.workflow.urlRefreshHours),
      h("label", { class: "switch" }, h("input", { name: "autoRetrieve", type: "checkbox", checked: config.autoRetrieve.enabled }), "Automatic retrieval"),
      numberField("autoRetrieveWeight", "Auto-retrieve weight", config.autoRetrieve.weight),
      ]),
      settingsGroup("Image captioning", "Optional image-to-text enrichment for documents that contain figures.", [
      h("label", { class: "field" }, "Caption provider", h("select", { name: "captionProvider" },
        ["off", "openai", "ollama"].map((value) => h("option", { value, selected: config.captioning.provider === value }, value)))),
      h("label", { class: "field" }, "Caption model", h("input", { name: "captionModel", value: config.captioning.model ?? "" })),
      h("label", { class: "field" }, "Caption base URL", h("input", { name: "captionBaseUrl", value: config.captioning.baseUrl ?? "" })),
      h("label", { class: "field" }, "Caption API key", h("input", { name: "captionApiKey", type: "password", placeholder: settings.captionApiKeySet ? "Configured" : "" })),
      h("label", { class: "switch" }, h("input", { name: "clearCaptionApiKey", type: "checkbox" }), "Clear caption key"),
      h("div", { class: "settings-test-row" }, [captionTestButton, captionTestStatus]),
      ]),
      settingsGroup("Runtime & performance", "Manage local model cache and the amount of background import concurrency.", [
      h("label", { class: "field" }, "Model cache directory", h("input", { name: "modelCacheDir", value: config.models.cacheDir ?? "" })),
      h("label", { class: "field" }, "Hugging Face endpoint", h("input", { name: "hfEndpoint", value: config.models.hfEndpoint ?? "" })),
      numberField("importWorkers", "Import workers", config.jobs.importWorkers),
      h("label", { class: "switch" }, h("input", { name: "resumeInterrupt", type: "checkbox", checked: config.jobs.resumeInterrupt }), "Resume interrupted imports"),
      ]),
      settingsGroup("External helpers", "Optional command templates for Office conversion, PDF extraction, image decoding, and rendering.", [
      h("label", { class: "field" }, "Legacy office helper", h("input", { name: "legacyOffice", value: config.helpers?.legacyOffice ?? "", placeholder: "converter {input} {format}" })),
      h("label", { class: "field" }, "PDF content helper", h("input", { name: "contentConverter", value: config.helpers?.contentConverter ?? "", placeholder: "anydoc {input} {format}" })),
      h("label", { class: "field" }, "Image decoder helper", h("input", { name: "imageDecoder", value: config.helpers?.imageDecoder ?? "", placeholder: "image-decode {input} {format}" })),
      h("label", { class: "field" }, "Image renderer helper", h("input", { name: "ocrRenderHelper", value: config.ocr?.renderHelper ?? "", placeholder: "pdf-render {input} {format}" })),
      ]),
    ]),
    h("div", { class: "settings-form-footer" }, [
      h("p", { class: "model-config-note" }, "Save global defaults here; a knowledge-base override takes precedence. Existing vectors may need rebuilding after model changes."),
      h("button", { class: "button primary" }, [icon(icons.save), "Save global settings"]),
    ]),
    h("datalist", { id: "embedding-suggestions" }, suggestions.embedding.map((value) => h("option", { value }))),
    h("datalist", { id: "rerank-suggestions" }, suggestions.rerank.map((value) => h("option", { value }))),
  ]);
  screen.append(globalForm);

  const base = selectedBase();
  if (!base) return;
  const baseConfig = base.config ?? {};
  screen.append(h("form", { class: "section panel panel-body", onsubmit: (event) => { event.preventDefault(); guard(async () => {
    const form = event.target;
    const next = {
      ...baseConfig,
      topK: Number(form.topK.value || 0),
      urlRefreshHours: Number(form.refreshHours.value || 0),
      conflictStrategy: form.conflict.value,
      ocrMode: form.ocr.value,
      autoRetrieve: form.autoRetrieve.checked,
      autoRetrieveWeight: Number(form.autoRetrieveWeight.value || 0),
      chunkSeparator: form.chunkSeparator.value.trim(),
      chunkSize: Number(form.chunkSize.value || 0),
      chunkOverlap: Number(form.chunkOverlap.value || 0),
      semanticChunkThreshold: Number(form.semanticThreshold.value || 0),
      chunkTokenLimit: Number(form.chunkTokenLimit.value || 0),
    };
    if (form.smartMode.value === "global") delete next.smartChunk;
    else next.smartChunk = form.smartMode.value === "smart";
    if (form.semanticMode.value === "global") delete next.semanticChunk;
    else next.semanticChunk = form.semanticMode.value === "enabled";
    await api.updateBase(state.selectedBaseId, { config: next }); await loadBases(); await render();
  }, "Base settings saved"); } }, [
    h("div", { class: "section-head" }, [h("h2", {}, `Base settings · ${base.name}`), basePicker(async (value) => { syncBasePicker(value); await render(); })]),
    h("div", { class: "form-grid" }, [
      numberField("topK", "Top K", baseConfig.topK), numberField("refreshHours", "URL refresh hours", baseConfig.urlRefreshHours),
      h("label", { class: "field" }, "Conflict strategy", h("select", { name: "conflict" }, ["rename", "replace", "keep"].map((value) => h("option", { value, selected: baseConfig.conflictStrategy === value }, value)))),
      h("label", { class: "switch" }, h("input", { name: "autoRetrieve", type: "checkbox", checked: baseConfig.autoRetrieve !== false }), "Automatic retrieval"),
      numberField("autoRetrieveWeight", "Auto-retrieve weight", baseConfig.autoRetrieveWeight),
      h("label", { class: "field" }, "OCR mode", h("select", { name: "ocr" }, ["auto", "forced", "off"].map((value) => h("option", { value, selected: baseConfig.ocrMode === value }, value)))),
    ]),
    h("section", { class: "settings-group base-analysis-group" }, [
      h("div", { class: "settings-group-head" }, [
        h("div", {}, [
          h("h3", {}, "Analysis method"),
          h("p", { class: "settings-group-description" }, "Configure document analysis and chunking for this knowledge base. Reindex existing documents after changing these settings."),
        ]),
      ]),
      h("div", { class: "settings-group-body form-grid" }, [
        h("label", { class: "field" }, "Structural analysis", h("select", { name: "smartMode" }, [
          ["global", "Inherit global setting"], ["smart", "Smart structural"], ["fixed", "Fixed separator"],
        ].map(([value, label]) => h("option", { value, selected: (baseConfig.smartChunk == null ? "global" : baseConfig.smartChunk ? "smart" : "fixed") === value }, label)))),
        h("label", { class: "field" }, "Semantic chunking", h("select", { name: "semanticMode" }, [
          ["global", "Inherit global setting"], ["enabled", "Enabled"], ["disabled", "Disabled"],
        ].map(([value, label]) => h("option", { value, selected: (baseConfig.semanticChunk == null ? "global" : baseConfig.semanticChunk ? "enabled" : "disabled") === value }, label)))),
        h("label", { class: "field" }, "Chunk separator", h("input", { name: "chunkSeparator", value: baseConfig.chunkSeparator ?? "", placeholder: "\n\n" })),
        numberField("chunkSize", "Chunk size", baseConfig.chunkSize),
        numberField("chunkOverlap", "Chunk overlap", baseConfig.chunkOverlap),
        numberField("semanticThreshold", "Semantic threshold", baseConfig.semanticChunkThreshold),
        numberField("chunkTokenLimit", "Chunk token limit", baseConfig.chunkTokenLimit),
        h("p", { class: "model-config-note" }, "Leave numeric fields blank or 0 to inherit the global value. Semantic chunking requires an available embedding model."),
      ]),
    ]),
    h("button", { class: "button primary" }, [icon(icons.save), "Save base settings"]),
  ]));
}

async function loadBases() {
  state.bases = await api.bases({ signal: routeAbortController?.signal });
  if (!state.bases.some((base) => base.id === state.selectedBaseId)) syncBasePicker("");
}

function renderNavigation() {
  document.getElementById("navigation").replaceChildren(...navigation.map(([route, label]) => h("a", { href: `#/${route}`, class: route === state.route ? "active" : "" }, label)));
  applyI18n(document);
}

async function route() {
  const [route, query = ""] = (location.hash.replace("#/", "") || "overview").split("?");
  const generation = ++routeGeneration;
  documentListRefresh = null;
  routeAbortController?.abort();
  routeAbortController = new AbortController();
  setRouteSignal(routeAbortController.signal);
  state.route = route;
  renderNavigation();
  showRouteLoading();
  try {
    await loadBases();
    if (!isCurrentRoute(generation)) return;
    await render(new URLSearchParams(query).get("q") ?? "", generation);
    applyI18n(document);
  } catch (error) {
    if (isAbortError(error)) return;
    if (isCurrentRoute(generation)) {
      showRouteError(error);
      showToast(error.message, true);
    }
  }
}

window.addEventListener("hashchange", route);
initializeI18n();
const returnAgent = document.getElementById("return-agent");
if (returnAgent) {
  returnAgent.hidden = !/^\/extensions\/shutu-knowledge(?:\/|$)/.test(window.location.pathname);
}
observeI18n(screen);
recoverOperations().catch((error) => showToast(error.message, true));
route();
