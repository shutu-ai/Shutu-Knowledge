import { api, fileToBase64 } from "./api.js";
import { applyI18n, currentLocale, formatDate, formatNumber, initializeI18n, localized, observeI18n, setLocale } from "./i18n.js";

const navigation = [
  ["overview", "Overview"],
  ["bases", "Knowledge Bases"],
  ["documents", "Documents"],
  ["import", "Import"],
  ["recall", "Recall Test"],
  ["models", "Models"],
  ["settings", "Settings"],
];

const state = {
  route: "overview", bases: [], selectedBaseId: localStorage.getItem("knowledge-base") ?? "",
  modelJobs: {}, chunkExpansionAll: false, expandedChunks: new Set(),
};
const screen = document.getElementById("screen");
const toastNode = document.getElementById("toast");
let toastTimer;
const MAX_IMPORT_FILES = 20;
const SUPPORTED_IMPORT_EXTENSIONS = ".txt,.md,.markdown,.mdx,.csv,.html,.htm,.json,.log,.pdf,.docx,.doc,.pptx,.ppt,.xlsx,.xls,.epub";

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

function basePicker(onChange = () => {}) {
  return h("select", { onchange: (event) => onChange(event.target.value), "aria-label": "Knowledge base" },
    h("option", { value: "" }, "All knowledge bases"),
    state.bases.map((base) => h("option", { value: base.id, selected: base.id === state.selectedBaseId }, base.name)),
  );
}

function syncBasePicker(value) {
  state.selectedBaseId = value ?? state.selectedBaseId;
  localStorage.setItem("knowledge-base", state.selectedBaseId);
}

function metric(label, value) {
  return h("div", { class: "metric" }, [h("strong", {}, number(value)), h("span", {}, label)]);
}

function table(headers, rows) {
  return h("div", { class: "panel table-wrap" }, h("table", {},
    h("thead", {}, h("tr", {}, headers.map((header) => h("th", { scope: "col" }, header)))),
    h("tbody", {}, rows.length ? rows : h("tr", {}, h("td", { colspan: headers.length, class: "empty" }, "No items yet"))),
  ));
}

async function render(initialQuery = "") {
  const status = await api.status();
  const healthChip = chip(status.ready ? "Ready" : "Degraded");
  const searchButton = state.route !== "recall"
    ? h("a", { class: "button", href: "#/recall" }, [icon(icons.search), "Recall Test"]) : null;
  setHeader(`${localized(status.status)} · v${status.version}`, [healthChip, searchButton].filter(Boolean));
  screen.replaceChildren();
  if (state.route === "overview") await renderOverview();
  if (state.route === "bases") await renderBases();
  if (state.route === "documents") await renderDocuments();
  if (state.route === "import") await renderImport();
  if (state.route === "recall") await renderRecall(initialQuery);
  if (state.route === "models") await renderModels();
  if (state.route === "settings") await renderSettings();
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
          await api.deleteBase(base.id);
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

async function renderDocuments() {
  if (!state.selectedBaseId || !state.bases.some((base) => base.id === state.selectedBaseId)) {
    screen.append(h("div", { class: "empty panel" }, "Select a knowledge base to manage its documents."));
    return;
  }
  const documents = await api.documents(state.selectedBaseId);
  if (state.docFolder && !documents.some((doc) => doc.id === state.docFolder)) state.docFolder = "";
  const current = documents.find((doc) => doc.id === state.docFolder);
  const childFiles = documents.filter((doc) => (doc.parentDirectoryId ?? "") === state.docFolder && doc.sourceType !== "directory");
  const childFolders = documents.filter((doc) => (doc.parentDirectoryId ?? "") === state.docFolder && doc.sourceType === "directory");
  const topLevelFiles = documents.filter((doc) => !doc.parentDirectoryId && doc.sourceType !== "directory" && doc.sourceType !== "url");
  const rows = current ? [...childFolders, ...childFiles] : [...childFolders, ...topLevelFiles];

  const breadcrumbs = [h("button", { class: "button small", onclick: () => { state.docFolder = ""; state.docPreview = null; render(); } }, "Root")];
  if (current) {
    const trail = [];
    let item = current;
    while (item) { trail.unshift(item); item = documents.find((doc) => doc.id === item.parentDirectoryId); }
    trail.forEach((item, index) => breadcrumbs.push(h("button", {
      class: `button small${index === trail.length - 1 ? " primary" : ""}`,
      onclick: () => { state.docFolder = item.id; state.docPreview = null; render(); },
    }, item.title)));
  }

  const statusText = (doc) => h("div", {}, [
    chip(doc.status),
    h("div", { class: "muted mono" }, `${doc.phase || doc.status}${doc.status === "processing" ? ` · ${doc.progress}%` : ""}`),
  ]);
  const documentTitle = (doc) => h("div", { class: "truncate" }, [
    h("strong", {}, doc.title),
    h("div", { class: "muted" }, doc.sourcePath || doc.fileName || doc.url || `${number(doc.charCount)} chars`),
    doc.errorMessage ? h("div", { class: "muted" }, `${doc.errorCode || "error"}: ${doc.errorMessage}`) : null,
  ]);
  const previewPanel = h("section", { class: "section", "aria-live": "polite" });
  const setPreview = (doc, mode) => { state.docPreview = { id: doc.id, mode }; renderPreview(previewPanel, doc, mode); };
  const documentActions = (doc) => h("div", { class: "toolbar" }, [
    doc.sourceType === "directory" ? h("button", { class: "button small", onclick: () => { state.docFolder = doc.id; state.docPreview = null; render(); } }, "Open") : null,
    doc.sourceType === "directory" ? h("button", { class: "button small", onclick: () => guard(async () => {
      const job = await api.rescanDirectory(doc.id); await pollJob(job.jobId); await render();
    }, "Directory rescanned") }, "Rescan") : null,
    doc.sourceType === "url" ? h("button", { class: "button small", onclick: () => guard(async () => {
      const result = await api.refreshURL(doc.id); showToast(result.changed ? "URL refreshed" : "URL unchanged"); await render();
    }) }, "Refresh") : null,
    doc.sourceType !== "directory" ? h("button", { class: "button small", onclick: () => setPreview(doc, "text") }, "Text") : null,
    doc.sourceType !== "directory" ? h("button", { class: "button small", onclick: () => setPreview(doc, "chunks") }, "Chunks") : null,
    h("button", { class: "button small", onclick: () => guard(async () => {
      const title = window.prompt("Rename document", doc.title);
      if (!title || title === doc.title) return;
      await api.updateDocument(doc.id, title); await render();
    }, "Document renamed") }, "Rename"),
    doc.sourceType !== "directory" ? h("button", { class: "button small", onclick: () => guard(async () => {
      await api.reindexDocument(doc.id); await render();
    }, "Document reindexed") }, "Reindex") : null,
    h("button", { class: "button small danger", onclick: () => guard(async () => {
      const message = doc.sourceType === "directory" ? `Delete folder "${doc.title}" and all nested documents?` : `Delete "${doc.title}"?`;
      if (!window.confirm(message)) return;
      if (doc.sourceType === "directory") await api.deleteDirectory(doc.id); else await api.deleteDocument(doc.id);
      await render();
    }, "Document deleted") }, "Delete"),
  ]);

  const form = h("form", { class: "section", onsubmit: (event) => { event.preventDefault(); guard(async () => {
    const ids = [...event.target.querySelectorAll("input[name='select']:checked")].map((input) => input.value);
    if (!ids.length) return;
    const job = await api.reindexDocuments(ids); await pollJob(job.jobId); await render();
  }, "Selected documents reindexed"); } }, [
    h("section", { class: "section toolbar" }, [
      basePicker(async (value) => { syncBasePicker(value); state.docFolder = ""; state.docPreview = null; await render(); }),
      ...breadcrumbs,
      h("button", { class: "button" }, [icon(icons.refresh), "Rebuild selected"]),
      h("button", { class: "button danger", type: "button", onclick: () => guard(async () => {
        const ids = [...document.querySelectorAll("input[name='select']:checked")].map((input) => input.value);
        if (!ids.length || !window.confirm(`Delete ${ids.length} selected documents?`)) return;
        const result = await api.deleteDocuments(ids); showToast(`${result.deleted} documents deleted`); await render();
      }) }, "Delete selected"),
    ]),
    h("section", { class: "section" }, table(["", "Title", "Status", "Source", "Chunks", "Updated", "Actions"], rows.map((doc) => h("tr", {},
      h("td", {}, doc.sourceType !== "directory" ? h("input", { name: "select", type: "checkbox", value: doc.id }) : null),
      h("td", { class: "truncate" }, documentTitle(doc)),
      h("td", {}, statusText(doc)),
      h("td", {}, doc.sourceType),
      h("td", {}, number(doc.chunkCount)),
      h("td", { class: "muted" }, date(doc.updatedAt || doc.createdAt)),
      h("td", {}, documentActions(doc)),
    )))),
  ]);
  screen.append(form);
  screen.append(previewPanel);
  if (state.docPreview) {
    const doc = documents.find((item) => item.id === state.docPreview.id);
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
    const text = await api.rawText(doc.id);
    container.append(h("pre", { class: "panel panel-body context" }, text.slice(0, 40_000) || "Empty document"));
  } catch (error) {
    container.append(h("div", { class: "panel empty" }, error.message));
  }
}

async function renderImport() {
  if (!state.selectedBaseId) { screen.append(h("div", { class: "empty panel" }, "Select a knowledge base before importing.")); return; }
  const base = selectedBase();
  screen.append(h("section", { class: "section toolbar" }, [basePicker(async (value) => { syncBasePicker(value); await render(); })]));
  screen.append(h("section", { class: "section grid-2" }, [
    h("form", { class: "panel panel-body", onsubmit: (event) => { event.preventDefault(); guard(async () => {
      const form = event.target;
      await api.addText(state.selectedBaseId, { title: form.title.value, content: form.content.value });
      form.reset(); await render();
    }, "Text imported"); } }, [
      h("h2", {}, "Text"), h("label", { class: "field" }, "Title", h("input", { name: "title", required: true })),
      h("label", { class: "field" }, "Content", h("textarea", { name: "content", required: true })),
      h("button", { class: "button primary" }, "Import text"),
    ]),
    h("form", { class: "panel panel-body", onsubmit: (event) => { event.preventDefault(); guard(async () => {
      const form = event.target;
      await api.addURL(state.selectedBaseId, { url: form.url.value, title: form.title.value });
      form.reset(); await render();
    }, "URL imported"); } }, [
      h("h2", {}, "URL"), h("label", { class: "field" }, "Page URL", h("input", { name: "url", type: "url", required: true })),
      h("label", { class: "field" }, "Title", h("input", { name: "title" })),
      h("button", { class: "button primary" }, "Import URL"),
    ]),
    h("form", { class: "panel panel-body", onsubmit: async (event) => { event.preventDefault(); await guard(async () => {
      const form = event.target;
      if (form.files.files.length > MAX_IMPORT_FILES) {
        showToast(`At most ${MAX_IMPORT_FILES} files per import; split the selection`, true);
        return;
      }
      const files = await Promise.all([...form.files.files].map(async (file) => ({
        fileName: file.name, contentBase64: await fileToBase64(file),
      })));
      await api.addFiles(state.selectedBaseId, { files, conflict: form.conflict.value });
      form.reset(); await render();
    }, "Files imported"); } }, [
      h("h2", {}, "Files"), h("input", { name: "files", type: "file", multiple: true, required: true, accept: SUPPORTED_IMPORT_EXTENSIONS }),
      h("label", { class: "field" }, "Conflict strategy", h("select", { name: "conflict" },
        ["rename", "replace", "keep", "detect"].map((value) => h("option", { value, selected: value === "rename" }, value)))),
      h("button", { class: "button primary" }, "Import files"),
    ]),
    h("form", { class: "panel panel-body", onsubmit: (event) => { event.preventDefault(); guard(async () => {
      const form = event.target;
      const job = await api.importDirectory(state.selectedBaseId, form.path.value);
      await pollJob(job.jobId); form.reset(); await render();
    }, "Directory imported"); } }, [
      h("h2", {}, "Directory"), h("label", { class: "field" }, "Absolute local path", h("input", { name: "path", required: true })),
      h("button", { class: "button primary" }, "Import directory"),
    ]),
  ]));
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
    return [
      `[${index + 1}] ${hit.documentTitle || hit.docId}${heading}`,
      `source: baseId=${hit.baseId}; docId=${hit.docId}; chunkId=${hit.chunkId}; chunkIndex=${hit.index}`,
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
        h("div", { class: "muted mono" }, `base ${hit.baseId} · doc ${hit.docId} · chunk ${hit.chunkId}`),
    ])),
    result.rerank ? h("div", { class: "muted mono", style: "margin-bottom:12px" },
      `rerank ${result.rerank.status} · ${result.rerank.model} · ${result.rerank.candidateCount} candidates · ${result.rerank.elapsedMs || 0} ms`) : null,
  );
}

async function renderModels() {
  const base = selectedBase();
  const config = base?.config ?? {};
  const localModels = await api.localModels();
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
  const jobBanners = Object.entries(state.modelJobs).map(([id, job]) => h("div", { class: "panel panel-body", style: "margin-bottom:10px" }, [
    h("div", { class: "toolbar", style: "justify-content:space-between" }, [
      h("span", { class: "mono" }, `${job.label || id} · ${job.status} · ${job.progress || 0}%`),
      h("button", { class: "button small danger", onclick: () => guard(async () => {
        state.modelJobs[id].cancelling = true;
        await api.cancelJob(id);
      }) }, "Cancel"),
    ]),
  ]));
  screen.append(h("section", { class: "section" }, [
    h("div", { class: "section-head" }, [h("h2", {}, "Provider"), basePicker(async (value) => { syncBasePicker(value); await render(); })]),
    h("form", { class: "panel panel-body form-grid", onsubmit: (event) => { event.preventDefault(); guard(async () => {
      const form = event.target;
      await api.updateBase(state.selectedBaseId, { config: { ...config, embeddingProvider: form.provider.value, embeddingBaseUrl: form.baseUrl.value, embeddingModel: form.model.value, rerankModel: form.rerankModel.value, rerankBaseUrl: form.rerankUrl.value } });
      await loadBases(); await render();
    }, "Model configuration saved"); } }, [
      h("label", { class: "field" }, "Embedding provider", h("select", { name: "provider" }, ["openai", "ollama", "local", "none"].map((value) => h("option", { value, selected: config.embeddingProvider === value }, value)))),
      h("label", { class: "field" }, "Base URL", h("input", { name: "baseUrl", value: config.embeddingBaseUrl ?? "" })),
      h("label", { class: "field" }, "Embedding model", h("input", { name: "model", value: config.embeddingModel ?? "" })),
      h("label", { class: "field" }, "Rerank model", h("input", { name: "rerankModel", value: config.rerankModel ?? "" })),
      h("label", { class: "field" }, "Rerank URL", h("input", { name: "rerankUrl", value: config.rerankBaseUrl ?? "" })),
      h("button", { class: "button primary" }, [icon(icons.save), "Save providers"]),
    ]),
    ...jobBanners,
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
              await api.removeOCRModel(); await render();
            }, "OCR model removed") }, [icon(icons.trash), "Delete"]) : null,
            h("button", { class: "button small primary", onclick: () => guard(async () => {
              const job = await api.downloadOCRModel();
              trackJob(job.jobId, `ocr ${ocrModel.id}`).catch((error) => showToast(error.message, true));
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
        h("div", { class: "muted" }, ocrModel.status === "ready" && ocrRuntime.status.ocr?.ready
          ? "OCR artifacts and helper are ready"
          : "OCR remains unavailable until both are ready"),
      ]),
      h("div", { class: "section-head" }, [h("h2", {}, "Local model files"), h("span", { class: "muted mono" }, localModels.cacheDir)]),
        h("div", { class: "panel list" }, localModels.models.length ? localModels.models.map((model) => h("div", { class: "list-row" }, [
          h("div", {}, [
            h("strong", { class: "truncate" }, model.id),
            h("div", { class: "muted" }, `${model.kind} · ${number(model.sizeBytes)} bytes · ${model.status}${model.selfTest ? (model.selfTest.current ? " · self-test passed" : model.selfTest.healthy ? " · stale self-test" : " · self-test failed") : model.kind === "rerank" ? " · self-test required" : ""}`),
          ]),
          h("div", { class: "toolbar" }, [
            model.kind === "rerank" ? h("button", { class: "button small", onclick: () => guard(async () => {
              const result = await api.selfTestReranker(model.id);
              showToast(`Reranker self-test passed · ${result.latencyMs} ms`);
              await render();
            }) }, "Self-test") : null,
            h("button", { class: "button small danger", onclick: () => guard(async () => {
              if (!window.confirm(`Delete local model "${model.id}"?`)) return;
              await api.removeModel(model.id); await render();
            }, "Model removed") }, "Delete"),
          ]),
        ])) : h("div", { class: "empty" }, "No local model files")),
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
        trackJob(job.jobId, `${body.kind} ${body.id}`).catch((error) => showToast(error.message, true));
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
          const result = await api.migrateCache({ targetDir: form.targetDir.value, removeSource });
          showToast(`Migrated ${result.modelCount} models (${number(result.bytes)} bytes)`);
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
          await api.deleteOllama(model.name); await render();
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

async function trackJob(id, label) {
  state.modelJobs[id] = { label, status: "pending", progress: 0 };
  if (state.route === "models") await render();
  for (;;) {
    const job = await api.job(id);
    state.modelJobs[id] = { label, status: job.status, progress: job.progress };
    if (state.route === "models") await render();
    if (["done", "failed", "cancelled"].includes(job.status)) {
      const error = job.status === "failed" ? job.error : job.status === "cancelled" ? "Download cancelled" : "";
      delete state.modelJobs[id];
      await render();
      if (error) throw new Error(error);
      showToast(`${label} complete`);
      return job;
    }
    await new Promise((resolve) => setTimeout(resolve, 350));
  }
}

async function renderSettings() {
  const numberField = (name, label, value) => h("label", { class: "field" }, label, h("input", { name, type: "number", value: value ?? "" }));
  const settings = await api.config();
  const config = settings.config;
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
    h("h2", {}, "Global settings"),
    h("div", { class: "form-grid" }, [
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
      h("label", { class: "switch" }, h("input", { name: "smartChunk", type: "checkbox", checked: config.chunking.smart }), "Smart chunking"),
      h("label", { class: "field" }, "Chunk separator", h("input", { name: "chunkSeparator", value: config.chunking.separator ?? "" })),
      numberField("chunkSize", "Chunk size", config.chunking.size), numberField("chunkOverlap", "Chunk overlap", config.chunking.overlap),
      h("label", { class: "switch" }, h("input", { name: "semanticChunk", type: "checkbox", checked: config.chunking.semantic }), "Semantic chunking"),
      numberField("semanticThreshold", "Semantic threshold", config.chunking.semanticThreshold),
      numberField("chunkTokenLimit", "Chunk token limit", config.chunking.tokenLimit),
      numberField("topK", "Top K", config.retrieval.topK),
      h("label", { class: "field" }, "Search mode", h("select", { name: "searchMode" },
        ["auto", "hybrid", "vector", "lexical"].map((value) => h("option", { value, selected: config.retrieval.mode === value }, value)))),
      numberField("similarityMin", "Similarity minimum", config.retrieval.similarityMin),
      h("label", { class: "switch" }, h("input", { name: "mmr", type: "checkbox", checked: config.retrieval.mmr }), "MMR"),
      numberField("mmrDiversity", "MMR diversity", config.retrieval.mmrDiversity),
      numberField("rrfWeight", "RRF vector weight", config.retrieval.rrfVectorWeight),
      numberField("siblingChunks", "Sibling chunks", config.retrieval.siblingChunks),
      h("label", { class: "field" }, "Document processor", h("select", { name: "processor" },
        ["builtin", "mineru"].map((value) => h("option", { value, selected: config.processing.provider === value }, value)))),
      h("label", { class: "field" }, "MinerU API host", h("input", { name: "mineruHost", value: config.processing.apiHost ?? "" })),
      h("label", { class: "field" }, "MinerU API key", h("input", { name: "mineruApiKey", type: "password", placeholder: settings.mineruApiKeySet ? "Configured" : "" })),
      h("label", { class: "switch" }, h("input", { name: "clearMineruApiKey", type: "checkbox" }), "Clear MinerU key"),
      h("label", { class: "field" }, "Conflict strategy", h("select", { name: "workflowConflict" },
        ["rename", "replace", "keep"].map((value) => h("option", { value, selected: config.workflow.conflictStrategy === value }, value)))),
      numberField("workflowRefreshHours", "URL refresh hours", config.workflow.urlRefreshHours),
      h("label", { class: "switch" }, h("input", { name: "autoRetrieve", type: "checkbox", checked: config.autoRetrieve.enabled }), "Automatic retrieval"),
      numberField("autoRetrieveWeight", "Auto-retrieve weight", config.autoRetrieve.weight),
      h("label", { class: "field" }, "Caption provider", h("select", { name: "captionProvider" },
        ["off", "openai", "ollama"].map((value) => h("option", { value, selected: config.captioning.provider === value }, value)))),
      h("label", { class: "field" }, "Caption model", h("input", { name: "captionModel", value: config.captioning.model ?? "" })),
      h("label", { class: "field" }, "Caption base URL", h("input", { name: "captionBaseUrl", value: config.captioning.baseUrl ?? "" })),
      h("label", { class: "field" }, "Caption API key", h("input", { name: "captionApiKey", type: "password", placeholder: settings.captionApiKeySet ? "Configured" : "" })),
      h("label", { class: "switch" }, h("input", { name: "clearCaptionApiKey", type: "checkbox" }), "Clear caption key"),
      h("label", { class: "field" }, "Model cache directory", h("input", { name: "modelCacheDir", value: config.models.cacheDir ?? "" })),
      h("label", { class: "field" }, "Hugging Face endpoint", h("input", { name: "hfEndpoint", value: config.models.hfEndpoint ?? "" })),
      numberField("importWorkers", "Import workers", config.jobs.importWorkers),
      h("label", { class: "switch" }, h("input", { name: "resumeInterrupt", type: "checkbox", checked: config.jobs.resumeInterrupt }), "Resume interrupted imports"),
      h("label", { class: "field" }, "Legacy office helper", h("input", { name: "legacyOffice", value: config.helpers?.legacyOffice ?? "", placeholder: "converter {input} {format}" })),
      h("label", { class: "field" }, "PDF content helper", h("input", { name: "contentConverter", value: config.helpers?.contentConverter ?? "", placeholder: "anydoc {input} {format}" })),
      h("label", { class: "field" }, "Image decoder helper", h("input", { name: "imageDecoder", value: config.helpers?.imageDecoder ?? "", placeholder: "image-decode {input} {format}" })),
      h("label", { class: "field" }, "Image renderer helper", h("input", { name: "ocrRenderHelper", value: config.ocr?.renderHelper ?? "", placeholder: "pdf-render {input} {format}" })),
    ]),
    h("datalist", { id: "embedding-suggestions" }, (await api.suggestions()).embedding.map((value) => h("option", { value }))),
    h("datalist", { id: "rerank-suggestions" }, (await api.suggestions()).rerank.map((value) => h("option", { value }))),
    h("button", { class: "button primary" }, [icon(icons.save), "Save global settings"]),
  ]);
  screen.append(globalForm);

  const base = selectedBase();
  if (!base) return;
  const baseConfig = base.config ?? {};
  screen.append(h("form", { class: "section panel panel-body", onsubmit: (event) => { event.preventDefault(); guard(async () => {
    const form = event.target;
    const next = {
      ...baseConfig,
      chunkSize: Number(form.chunkSize.value || 0),
      chunkOverlap: Number(form.chunkOverlap.value || 0),
      topK: Number(form.topK.value || 0),
      urlRefreshHours: Number(form.refreshHours.value || 0),
      conflictStrategy: form.conflict.value,
      ocrMode: form.ocr.value,
      autoRetrieve: form.autoRetrieve.checked,
      autoRetrieveWeight: Number(form.autoRetrieveWeight.value || 0),
    };
    await api.updateBase(state.selectedBaseId, { config: next }); await loadBases(); await render();
  }, "Base settings saved"); } }, [
    h("div", { class: "section-head" }, [h("h2", {}, `Base settings · ${base.name}`), basePicker(async (value) => { syncBasePicker(value); await render(); })]),
    h("div", { class: "form-grid" }, [
      numberField("chunkSize", "Chunk size", baseConfig.chunkSize), numberField("chunkOverlap", "Chunk overlap", baseConfig.chunkOverlap),
      numberField("topK", "Top K", baseConfig.topK), numberField("refreshHours", "URL refresh hours", baseConfig.urlRefreshHours),
      h("label", { class: "field" }, "Conflict strategy", h("select", { name: "conflict" }, ["rename", "replace", "keep"].map((value) => h("option", { value, selected: baseConfig.conflictStrategy === value }, value)))),
      h("label", { class: "switch" }, h("input", { name: "autoRetrieve", type: "checkbox", checked: baseConfig.autoRetrieve !== false }), "Automatic retrieval"),
      numberField("autoRetrieveWeight", "Auto-retrieve weight", baseConfig.autoRetrieveWeight),
      h("label", { class: "field" }, "OCR mode", h("select", { name: "ocr" }, ["auto", "forced", "off"].map((value) => h("option", { value, selected: baseConfig.ocrMode === value }, value)))),
    ]),
    h("button", { class: "button primary" }, [icon(icons.save), "Save base settings"]),
  ]));
}

async function pollJob(id) {
  for (;;) {
    const job = await api.job(id);
    if (["done", "failed", "cancelled"].includes(job.status)) {
      if (job.status !== "done") throw new Error(job.error || `Job ${job.status}`);
      return job;
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
}

async function loadBases() {
  state.bases = await api.bases();
  if (!state.bases.some((base) => base.id === state.selectedBaseId)) syncBasePicker("");
}

function renderNavigation() {
  document.getElementById("navigation").replaceChildren(...navigation.map(([route, label]) => h("a", { href: `#/${route}`, class: route === state.route ? "active" : "" }, label)));
  applyI18n(document);
}

async function route() {
  const [route, query = ""] = (location.hash.replace("#/", "") || "overview").split("?");
  state.route = route;
  renderNavigation();
  try {
    await loadBases();
    await render(new URLSearchParams(query).get("q") ?? "");
    applyI18n(document);
  } catch (error) {
    showToast(error.message, true);
  }
}

window.addEventListener("hashchange", route);
initializeI18n();
document.getElementById("language").addEventListener("change", (event) => {
  setLocale(event.target.value);
  route();
});
observeI18n(screen);
route();
