const STORAGE_KEY = "knowledge-language";

const zh = {
  "Language": "语言",
  "Dismiss notification": "关闭通知",
  "English": "English",
  "Chinese": "中文",
  "Overview": "总览",
  "Knowledge Bases": "知识库",
  "Documents": "文档",
  "Import": "导入",
  "Recall Test": "检索测试",
  "Models": "模型",
  "Settings": "设置",
  "Knowledge service status": "Knowledge 服务状态",
  "Ready": "就绪",
  "Degraded": "降级",
  "Chunks": "块",
  "Expand": "展开",
  "Collapse": "折叠",
  "Expand all": "全部展开",
  "Collapse all": "全部折叠",
  "Characters": "字符",
  "Tokens": "令牌",
  "Knowledge bases": "知识库",
  "Name": "名称",
  "Status": "状态",
  "Empty": "空",
  "Retrieval scope": "检索范围",
  "Automatic retrieval enabled": "自动检索已启用",
  "No pinned scope; all bases are available.": "未固定范围；所有知识库均可用。",
  "Pin the current selection? Cancel keeps all bases.": "固定当前选择？取消将保持所有知识库可用。",
  "Save scope": "保存范围",
  "Scope saved": "范围已保存",
  "Groups": "分组",
  "New group": "新建分组",
  "Add": "添加",
  "Group": "分组",
  "New name": "新名称",
  "Rename": "重命名",
  "Delete": "删除",
  "Delete group": "删除分组",
  "Ungrouped": "未分组",
  "All knowledge bases": "所有知识库",
  "Bases": "知识库",
  "New knowledge base": "新建知识库",
  "Description": "描述",
  "Create base": "创建知识库",
  "Group created": "分组已创建",
  "Group renamed": "分组已重命名",
  "Group deleted": "分组已删除",
  "Base created": "知识库已创建",
  "Base renamed": "知识库已重命名",
  "Base deleted": "知识库已删除",
  "Rename knowledge base": "重命名知识库",
  "docs": "个文档",
  "chunks": "个块",
  "Select a knowledge base to manage its documents.": "选择一个知识库以管理其文档。",
  "Root": "根目录",
  "Open": "打开",
  "Rescan": "重新扫描",
  "Refresh": "刷新",
  "Text": "文本",
  "Chunks": "块",
  "Rename document": "重命名文档",
  "Reindex": "重建索引",
  "Delete selected": "删除所选",
  "Rebuild selected": "重建所选",
  "Title": "标题",
  "Source": "来源",
  "Updated": "更新时间",
  "Actions": "操作",
  "Preview": "预览",
  "Close": "关闭",
  "No chunks": "没有块",
  "Empty document": "空文档",
  "Directory rescanned": "目录已重新扫描",
  "URL refreshed": "URL 已刷新",
  "URL unchanged": "URL 未变化",
  "Document renamed": "文档已重命名",
  "Document reindexed": "文档已重建索引",
  "Document deleted": "文档已删除",
  "Selected documents reindexed": "所选文档已重建索引",
  "Select a knowledge base before importing.": "导入前请先选择知识库。",
  "Content": "内容",
  "Import text": "导入文本",
  "Page URL": "页面 URL",
  "Import URL": "导入 URL",
  "Files": "文件",
  "Conflict strategy": "冲突策略",
  "Import files": "导入文件",
  "Directory": "目录",
  "Absolute local path": "本地绝对路径",
  "Import directory": "导入目录",
  "Text imported": "文本已导入",
  "URL imported": "URL 已导入",
  "Files imported": "文件已导入",
  "Directory imported": "目录已导入",
  "Ask the imported corpus": "向已导入语料提问",
  "Search mode": "搜索模式",
  "Top K": "Top K",
  "MMR": "MMR",
  "Run retrieval": "执行检索",
  "Query history": "查询历史",
  "Clear": "清空",
  "Clear recall query history?": "清空检索查询历史？",
  "Query history cleared": "查询历史已清空",
  "Delete query history item": "删除查询历史项",
  "Run a query to create replayable history": "运行一次查询以生成可回放历史",
  "hits": "条命中",
  "Copy citations": "复制引用",
  "All citations copied": "已复制全部引用",
  "Copy": "复制",
  "Citation copied": "引用已复制",
  "Provider": "提供方",
  "Embedding provider": "嵌入提供方",
  "Base URL": "基础 URL",
  "Embedding model": "嵌入模型",
  "Rerank model": "重排模型",
  "Rerank URL": "重排 URL",
  "Save providers": "保存提供方",
  "Model configuration saved": "模型配置已保存",
  "OCR model": "OCR 模型",
  "Download": "下载",
  "Missing artifacts": "缺少制品",
  "All artifacts installed": "全部制品已安装",
  "Runtime helper: ready": "运行时 helper：就绪",
  "Runtime helper: not ready": "运行时 helper：未就绪",
  "Runtime helper: not configured": "运行时 helper：未配置",
  "OCR artifacts and helper are ready": "OCR 制品与 helper 均已就绪",
  "OCR remains unavailable until both are ready": "制品和 helper 就绪前，OCR 不可用",
  "OCR model removed": "OCR 模型已删除",
  "Ollama is unavailable": "Ollama 不可用",
  "Runtime status unavailable": "运行时状态不可用",
  "Local model files": "本地模型文件",
  "Self-test": "自检",
  "No local model files": "没有本地模型文件",
  "Custom local reranker": "自定义本地重排器",
  "Register": "注册",
  "Hugging Face model": "Hugging Face 模型",
  "Kind": "类型",
  "Artifacts": "制品",
  "Leave blank for defaults": "留空使用默认值",
  "Download model": "下载模型",
  "New cache directory": "新缓存目录",
  "Remove source after verification": "校验后删除源目录",
  "Migrate cache": "迁移缓存",
  "Model cache migrated": "模型缓存已迁移",
  "Ollama": "Ollama",
  "No Ollama models": "没有 Ollama 模型",
  "Model": "模型",
  "Pull model": "拉取模型",
  "Ollama model deleted": "Ollama 模型已删除",
  "Model removed": "模型已删除",
  "Download cancelled": "下载已取消",
  "Custom reranker registered": "自定义重排器已注册",
  "Global settings": "全局设置",
  "Embedding base URL": "嵌入基础 URL",
  "Embedding API key": "嵌入 API 密钥",
  "Configured": "已配置",
  "Clear embedding key": "清除嵌入密钥",
  "Reranker enabled": "启用重排",
  "Rerank base URL": "重排基础 URL",
  "Rerank API key": "重排 API 密钥",
  "Clear rerank key": "清除重排密钥",
  "Smart chunking": "智能分块",
  "Chunk separator": "分块分隔符",
  "Chunk size": "块大小",
  "Chunk overlap": "块重叠",
  "Semantic chunking": "语义分块",
  "Semantic threshold": "语义阈值",
  "Chunk token limit": "块令牌上限",
  "Similarity minimum": "最低相似度",
  "MMR diversity": "MMR 多样性",
  "RRF vector weight": "RRF 向量权重",
  "Sibling chunks": "相邻块",
  "Document processor": "文档处理器",
  "MinerU API host": "MinerU API 主机",
  "MinerU API key": "MinerU API 密钥",
  "Clear MinerU key": "清除 MinerU 密钥",
  "URL refresh hours": "URL 刷新小时数",
  "Automatic retrieval": "自动检索",
  "Auto-retrieve weight": "自动检索权重",
  "Caption provider": "图注提供方",
  "Caption model": "图注模型",
  "Caption base URL": "图注基础 URL",
  "Caption API key": "图注 API 密钥",
  "Clear caption key": "清除图注密钥",
  "Model cache directory": "模型缓存目录",
  "Hugging Face endpoint": "Hugging Face 端点",
  "Import workers": "导入工作线程数",
  "Resume interrupted imports": "恢复中断的导入",
  "Legacy office helper": "旧版 Office helper",
  "PDF content helper": "PDF 内容 helper",
  "Save global settings": "保存全局设置",
  "Global settings saved": "全局设置已保存",
  "Base settings": "知识库设置",
  "OCR mode": "OCR 模式",
  "Save base settings": "保存知识库设置",
  "Base settings saved": "知识库设置已保存",
  "pending": "等待中",
  "processing": "处理中",
  "failed": "失败",
  "done": "已完成",
  "cancelled": "已取消",
  "stale": "已过期",
  "downloading": "下载中",
  "incomplete": "不完整",
  "registered": "已注册",
  "not-downloaded": "未下载",
  "not ready": "未就绪",
  "ready": "就绪",
  "unknown": "未知",
  "error": "错误",
  "text": "文本",
  "file": "文件",
  "url": "URL",
  "directory": "目录",
  "rename": "重命名",
  "replace": "替换",
  "keep": "保留",
  "detect": "检测",
  "off": "关闭",
  "auto": "自动",
  "forced": "强制",
  "local": "本地",
  "none": "无",
  "No items yet": "暂无项目",
  "Delete group": "删除分组",
};

const patterns = [
  [/^(\d+) bases? pinned$/, (_, count) => `${count} 个知识库已固定`],
  [/^(\d+) docs · (\d+) chunks(?: · (.*))?$/, (_, docs, chunks, description) =>
    `${docs} 个文档 · ${chunks} 个块${description ? ` · ${description}` : ""}`],
  [/^Delete group "(.+)"\? Member bases become ungrouped\.$/, (_, name) =>
    `删除分组“${name}”？成员知识库将变为未分组。`],
  [/^Delete "(.+)" and all documents\?$/, (_, name) => `删除“${name}”及其全部文档？`],
  [/^Delete folder "(.+)" and all nested documents\?$/, (_, name) =>
    `删除文件夹“${name}”及其嵌套文档？`],
  [/^Delete "(.+)"\?$/, (_, name) => `删除“${name}”？`],
  [/^Delete local model "(.+)"\?$/, (_, name) => `删除本地模型“${name}”？`],
  [/^Delete OCR model "(.+)"\?$/, (_, name) => `删除 OCR 模型“${name}”？`],
  [/^Delete Ollama model "(.+)"\?$/, (_, name) => `删除 Ollama 模型“${name}”？`],
  [/^Missing artifacts: (.+)$/, (_, artifacts) => `缺少制品：${artifacts}`],
  [/^All artifacts installed(?: · (.+))?$/, (_, downloaded) =>
    `全部制品已安装${downloaded ? ` · ${downloaded}` : ""}`],
  [/^(\w+) · top (\d+)( · MMR)? · (\d+) hits · (.+)$/, (_, mode, topK, mmr, hits, date) =>
    `${localizedText(mode)} · 前 ${topK}${mmr ?? ""} · ${hits} 条命中 · ${date}`],
  [/^Delete (\d+) selected documents\?$/, (_, count) => `删除所选的 ${count} 个文档？`],
  [/^(\d+) documents deleted$/, (_, count) => `已删除 ${count} 个文档`],
  [/^At most (\d+) files per import; split the selection$/, (_, count) =>
    `每次导入最多 ${count} 个文件；请拆分选择`],
  [/^Preview · (.+)$/, (_, title) => `预览 · ${title}`],
  [/^Base settings · (.+)$/, (_, name) => `知识库设置 · ${name}`],
  [/^Final context · (\d+) hits$/, (_, count) => `最终上下文 · ${count} 条命中`],
  [/^(.+) complete$/, (_, label) => `${label} 已完成`],
  [/^Migrated (\d+) models \(([\d,]+) bytes\)$/, (_, count, bytes) =>
    `已迁移 ${count} 个模型（${bytes} 字节）`],
  [/^Reranker self-test passed · (\d+) ms$/, (_, latency) => `重排器自检通过 · ${latency} 毫秒`],
  [/^(.+) · self-test passed$/, (_, prefix) => `${prefix} · 自检通过`],
  [/^(.+) · stale self-test$/, (_, prefix) => `${prefix} · 自检已过期`],
  [/^(.+) · self-test failed$/, (_, prefix) => `${prefix} · 自检失败`],
  [/^(.+) · self-test required$/, (_, prefix) => `${prefix} · 需要自检`],
  [/^(.+ · \d+ bytes) · (.+)$/, (_, prefix, status) => `${prefix} · ${localizedText(status)}`],
];

function localizedText(value) {
  if (locale !== "zh") return String(value ?? "");
  const text = String(value ?? "");
  const trimmed = text.trim();
  if (!trimmed) return text;
  const replacement = zh[trimmed];
  if (replacement != null) return text.replace(trimmed, replacement);
  for (const [expression, format] of patterns) {
    const match = trimmed.match(expression);
    if (match) return text.replace(trimmed, format(...match));
  }
  return text;
}

function shouldTranslate(node) {
  const parent = node.parentElement;
  return Boolean(parent && !parent.closest("pre, strong, .no-i18n"));
}

function translateTree(root) {
  if (root.nodeType !== Node.TEXT_NODE) {
    for (const element of root.querySelectorAll?.("input, select, [aria-label]") ?? []) {
      for (const attribute of ["placeholder", "aria-label"]) {
        const value = element.getAttribute(attribute);
        if (value) element.setAttribute(attribute, localizedText(value));
      }
    }
  }
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  const nodes = [];
  while (walker.nextNode()) nodes.push(walker.currentNode);
  for (const node of nodes) {
    if (!shouldTranslate(node)) continue;
    const translated = localizedText(node.nodeValue);
    if (translated !== node.nodeValue) node.nodeValue = translated;
  }
}

function detectLocale() {
  const stored = localStorage.getItem(STORAGE_KEY);
  if (stored === "en" || stored === "zh") return stored;
  return "en";
}

export function currentLocale() {
  return locale;
}

export function initializeI18n() {
  locale = detectLocale();
  document.getElementById("language").value = locale;
  applyI18n(document);
}

export function formatNumber(value) {
  return new Intl.NumberFormat(locale === "zh" ? "zh-CN" : "en-US").format(value ?? 0);
}

export function formatDate(value) {
  if (!value) return "—";
  return new Date(value).toLocaleString(locale === "zh" ? "zh-CN" : "en-US");
}

export function applyI18n(root = document) {
  document.documentElement.lang = locale === "zh" ? "zh-CN" : "en";
  translateTree(root);
}

export function observeI18n(root) {
  const observer = new MutationObserver(() => {
    observer.takeRecords();
    queueMicrotask(() => applyI18n(root));
  });
  observer.observe(root, { childList: true, subtree: true });
  applyI18n(root);
}

export function localized(value) {
  return localizedText(value);
}

export function setLocale(next) {
  locale = next === "zh" ? "zh" : "en";
  localStorage.setItem(STORAGE_KEY, locale);
  document.getElementById("language").value = locale;
  window.location.reload();
}

let locale = "en";
