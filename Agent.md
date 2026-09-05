# Shutu Knowledge 项目工作计划

本文档基于《Shutu Knowledge 独立项目完整实现 Codex 提示词.md》分析生成，作为 `shutu-knowledge` 项目从零到 V1 Ready 的详细工作任务计划。执行过程中如与需求文档冲突，以需求文档为准，并应同步更新本计划。

---

## 1. 项目定位与总目标

在独立 Git 仓库 `shutu-knowledge` 中，实现与 `dsh-knowledge` 功能等价（Capability-by-Capability Functional Equivalence）的知识库系统，并通过 `shutu-agent` 已冻结的 **Extension Platform v1** 深度接入 Agent，使 Knowledge 在用户体验与 Agent Runtime 中表现得像原生内置能力，但工程上保持完全独立。

最终形态：

- 独立 Git、独立版本、独立构建、独立测试、独立发布、独立升级。
- 单向依赖：`shutu-knowledge → shutu-agent 公开 Extension API`。
- Agent 提供通用机制（Agent loop、Session、Context policy、Tool policy、Approval、Lifecycle 框架、Web shell、Observability）。
- Knowledge 提供领域能力（Documents、KB、Parsing、OCR、Chunking、Embedding、Index、Retrieval、Rerank、Context composition、Models、数据、UI、配置）。

参考仓库（均只读）：

- Agent 底座：`https://github.com/shutu-ai/shutu-agent`
- 功能参考：`https://github.com/Soren-ABT/dsh-knowledge`

---

## 2. 架构红线（全程有效）

以下约束适用于所有阶段，任何一条被违反即判定架构违规，必须立即停止并纠正：

1. **单向依赖**：`shutu-knowledge` 可以依赖 `shutu-agent` 公开 Extension API；`shutu-agent` 绝不依赖 `shutu-knowledge`。
2. **Agent 只读**：禁止修改 `shutu-agent` 的源码、docs、tests、Web、config schema；禁止给 Agent 增加 Knowledge 专用代码。
3. **Contract 唯一入口**：只能通过 Extension Manifest / Protocol v1 / `sdk/extension` / Capability Negotiation / Context Provider / Tool Contribution / Risk Metadata / Approval / Lifecycle / Health / Events / Web Contribution / Navigation / Observability 接入。
4. **禁止 internal import**：生产代码中 `github.com/shutu-ai/shutu-agent/internal/...` 导入数必须为 0。
5. **禁止绕过 Contract**：禁止直接操作 Agent Session DB、修改 message history / Prompt / 私有 runtime memory、通过文件系统改 Agent config、patch Agent 可执行文件、复制 internal structs、反射或 unsafe 访问 internals。
6. **Persistence Boundary**：Agent 拥有 Agent 状态；Knowledge 拥有 Knowledge 状态（KB、文档、chunks、metadata、索引、模型配置、任务、来源记录、检索历史），绝不写入 Agent Session DB。
7. **Context Ownership**：Knowledge 只能返回 `ContextContribution`，不能修改 Agent messages、追加 Prompt、控制 Session 或 Compaction；最终 Context Budget 由 Agent 控制。
8. **删除即无痕**：任一设计决策前自问——"如果明天删除整个 `shutu-knowledge` 目录，`shutu-agent` 是否完全不受影响并可持续升级？"答案必须是 YES。
9. **不降级目标**：目标是 dsh-knowledge capability equivalence，不是 MVP / 核心版 / 简化版；未经用户明确要求不得自行删减高级检索、模型管理、目录刷新、Retrieval Test、Anchor、Context Window、Semantic Chunking、OCR、Web 管理等能力。
10. **参考仓库只读**：`dsh-knowledge` 同样不得修改；实验代码复制到本项目合法位置。

### 2.1 Agent Contract Gap 处理协议

当发现 Extension Platform v1 缺少实现某项 Knowledge 能力所需的接口时：

1. 停止该功能的任何绕路实现。
2. 绝不修改 `shutu-agent`。
3. 在 `docs/agent_extension_gap_report.md` 记录 Gap。
4. 继续其它不受阻塞的功能。

每个 Gap 至少记录：Gap ID、Knowledge capability、业务场景、为什么当前 Extension v1 无法实现、Agent 已有相关能力、缺失的最小通用能力、推荐 Agent Improvement Requirement、是否阻塞、是否存在不破坏架构的临时方案。改进建议必须是通用 Agent capability（如更通用的 Extension Context/Event/Web/Tool Contract），禁止 Knowledge-specific API（如 `Agent.AddKnowledgeContext()`、`/api/knowledge/search`）。

### 2.2 许可证与源码复用规则

1. 先检查 `dsh-knowledge` LICENSE、第三方依赖许可证、被复制文件版权头。
2. 直接复制或改写受许可证约束代码时：保留必要版权与许可声明、记录来源、遵守原许可义务。
3. 建立并维护 `THIRD_PARTY_NOTICES.md` 与 `docs/source_reuse_inventory.md`（记录：源仓库、源文件/模块、复用/翻译/重实现、许可证、本地目标路径、必需署名）。
4. 禁止无记录复制源码；许可冲突时改为基于功能行为重新实现。

---

## 3. 前置准备（Phase 0 开始前必做）

- [x] 在 `shutu-knowledge` 目录执行并确认：`git status`、`git branch --show-current`、`git remote -v`，确保当前工作目录正确。
- [x] 定位本地 `shutu-agent` 仓库（若存在），记录基线：`git -C <SHUTU_AGENT_PATH> status --porcelain` 与 `git -C <SHUTU_AGENT_PATH> rev-parse HEAD`。若 Agent 仓库原本有用户未提交改动：不修改、不清理、不 reset，仅记录。（基线记录见 `docs/source_reuse_inventory.md`）
- [x] 定位本地 `dsh-knowledge` 参考仓库（只读），同样记录基线。（克隆至 `C:\dev-projects\dsh\dsh-knowledge`）
- [x] 确认开发环境：Go 版本、Node 版本、SQLite、构建工具链。（Go 1.26.7 / Node 24 / modernc.org/sqlite 纯 Go 驱动）
- [x] 通读需求文档全文，确认阶段划分与验收 Gate。

**收尾要求**：每个阶段结束时复查两个参考仓库，证明 HEAD 不变且未产生本任务引入的 tracked modifications；若发现跨仓库污染，判定 ARCHITECTURE VIOLATION，撤销本任务对参考仓库的修改（不得覆盖用户原有改动）。

---

## 4. 阶段计划

总体推进方式：**Phase 0 → Phase 8 顺序执行，不允许大爆炸式一次完成**。每阶段遵循固定节奏：

```text
audit → design → implementation → tests → self-review → commit
```

每阶段完成即独立提交（commit 只出现在 `shutu-knowledge` 仓库），不累计几十个不相关功能后一次提交。

### Phase 0 — Source Audit（源码审计，不写生产代码）

**目标**：完整理解两个参考仓库，建立能力基线、契约映射、Gap 清单与本项目架构设计，产出第一轮可提交的审计文档。

任务：

- [x] 完整读取 `dsh-knowledge` 源码（不只读 README）：source tree、packages、server、client、tools、database、migrations、parsers、chunkers、retrieval、embedding、reranker、OCR、model handling、configuration、tests、Web UI、API、background jobs、directory ingestion、URL ingestion、document lifecycle。
- [x] 审计 `dsh-knowledge` 真实支持的输入来源与文件格式（PDF/DOCX/PPTX/XLSX/TXT/Markdown/HTML/CSV/EPUB 及其它实际格式），以源码为准，不按提示词假定。
- [x] 审计 `dsh-knowledge` 暴露给 Agent 的完整 Tool inventory（提示词中的列表仅为线索，非最终依据）。
- [x] 审计 `dsh-knowledge` 的检索流水线真实行为（BM25、Vector、RRF、MMR、Rerank、Auto RAG、Anchor/continuation、Context Window），以源码与测试为准。
- [x] 完整读取 `shutu-agent` Extension Platform v1 公开文档、`sdk/extension`、Protocol 定义与相关测试，理解：Manifest、Capability Negotiation、Native Context Provider、Tool Contribution、Risk Metadata、Approval、Lifecycle、Health、Events（含 `ToolResultBoundary` / after_tool_result 官方语义）、Web Contribution、Navigation、Observability。
- [x] 检查 `dsh-knowledge` LICENSE 与第三方依赖许可，初始化 `THIRD_PARTY_NOTICES.md` 与 `docs/source_reuse_inventory.md`。
- [x] 建立能力清单 `docs/dsh_knowledge_capability_inventory.md`，分类至少覆盖：KB Management、Document Lifecycle、Input Sources、Parsing、OCR、Chunking、Semantic Chunking、Metadata、Indexing、Full Text Search、Vector Search、Hybrid Retrieval、RRF、MMR、Reranking、Context Composition、Context Window、Anchor/continuation、Embedding Models、Reranker Models、Model Management、Retrieval Testing、Knowledge Tools、Agent Integration、Automatic Retrieval、Web UI、Configuration、Persistence、Background Jobs、Observability、Error Handling、Security。每项记录：Capability ID、dsh-knowledge 实现、用户可见行为、输入、输出、配置、依赖、持久化、边界情况、测试、shutu-knowledge 目标实现、Status。
- [x] 建立等价矩阵 `docs/dsh_knowledge_equivalence_matrix.md`，状态只用 `PASS / PARTIAL / BLOCKED / NOT APPLICABLE` 并解释原因，禁止"大致支持/类似/基本完成"。
- [x] 建立契约映射 `docs/agent_contract_mapping.md`：Knowledge 每项能力 ↔ Extension v1 具体机制；标出无法映射项。
- [x] 建立 `docs/agent_extension_gap_report.md`（初始可为空表结构）。
- [x] 设计并提交 `docs/architecture.md`：Knowledge Core + Extension Adapter / Web API / CLI 多入口架构，数据域 `~/.shutu/knowledge/`，技术选型（推荐 Go backend + Web frontend，允许可选外部 runtime 如 OCR/MinerU/Node/Python helper，但需 health check、错误提示、自动发现、可选安装、不进 Agent Core）。

产出物：

```text
docs/dsh_knowledge_capability_inventory.md
docs/dsh_knowledge_equivalence_matrix.md
docs/agent_contract_mapping.md
docs/agent_extension_gap_report.md
docs/architecture.md
THIRD_PARTY_NOTICES.md（初始）
docs/source_reuse_inventory.md（初始）
```

验收：

- 能力清单覆盖 dsh-knowledge 全部用户可见功能与关键内部机制；矩阵无未解释遗漏。
- 契约映射覆盖 Context / Tools / Approval / Events / Lifecycle / Health / Web / Observability。
- 所有疑似 Gap 已按协议记录，未做任何绕路设计。
- 两个参考仓库基线复查通过，未产生任何修改。

### Phase 1 — Project Foundation（项目地基）

**目标**：搭建可构建、可测试、可运行的独立工程骨架，并完成与 shutu-agent 的 Extension 握手最小闭环。

任务：

- [x] 初始化 Go module 与推荐目录结构：`cmd/shutu-knowledge`、`internal/{extension,knowledge,ingest,parser,chunk,embedding,rerank,retrieval,index,storage(models→models),jobs,web,config}`、`web/`、`migrations/`、`examples/`、`docs/`、`tests/`。（目录随实现渐进创建）
- [x] 实现配置系统：Knowledge 自有配置（database、raw store、chunking、embedding、reranker、retrieval、OCR、models、Web、jobs），不向 Agent config 注入业务字段。
- [x] 实现结构化日志与日志安全基线（默认不记录完整敏感文档、完整 retrieval evidence、完整用户问题、credentials；debug 模式明确允许后才放开）。
- [x] 实现存储层与数据域 `~/.shutu/knowledge/`（`knowledge.db`、`raw/`、`indexes/`、`models/`、`cache/`、`tmp/`、`logs/`），SQLite 作为初始数据库。
- [x] 建立 versioned migrations 机制（禁止长期依赖业务代码中散落的 `CREATE TABLE IF NOT EXISTS`）。
- [x] 实现 CLI 骨架：`shutu-knowledge serve`、`shutu-knowledge doctor`、`shutu-knowledge version`。
- [x] 实现 Health 基础框架，至少区分：process healthy、database healthy、index subsystem healthy、model availability；可选组件（如 reranker）不可用不应判死整个 Extension（除非配置为 required）。
- [x] 实现 Extension Adapter 骨架与 `shutu-agent` 的 Manifest / handshake / capability negotiation 最小集成，能以 managed extension 身份被 Agent 发现并进入 ready。（stdio 握手 + health 已有真实 JSON-RPC 测试；Agent 双进程发现在 Phase 5 集成测试验证）
- [x] 建立 CI 基础：build、vet、unit test。（含 Gate B：shutu-agent/internal import 检查）

产出物：可构建的二进制、doctor 可运行的诊断、与真实 shutu-agent 的握手 demo/测试、初始 migrations、README 骨架。

验收：

- `shutu-knowledge serve` 可独立启动；doctor 能检查 database、migrations、storage 权限、extension 配置（模型与解析依赖检查随后续阶段补全）。
- 真实 Agent 能发现扩展并完成握手、报告 health。
- 生产代码中 Agent internal import 为 0。

### Phase 2 — Knowledge Core（知识库核心）

**目标**：实现 KB 与文档的完整生命周期、解析、切块、持久化与后台任务。

任务：

- [ ] KB CRUD：create / list / get / update / delete / stats；字段覆盖 name、description、metadata、created_at、updated_at、document_count、chunk_count、index state；delete 按 destructive operation 处理（进入 Tool Registry 时声明风险）。
- [ ] 文档生命周期状态机：pending → processing → ready / failed，外加 stale；支持 import、parse、chunk、index、ready、refresh、reindex、delete、error；禁止用一个 bool `indexed` 覆盖全部状态。
- [ ] Parser Registry 或等价通用结构：Document → MIME/type detection → Parser → Normalized Document → Chunking；避免主流程中 `if pdf else if docx` 无限堆积。
- [ ] 按 Phase 0 审计结果实现文件格式解析（目标尽量覆盖 PDF、DOCX、PPTX、XLSX、TXT、Markdown、HTML、CSV、EPUB 及 dsh-knowledge 实际其它格式）。
- [ ] 实现 Chunking 策略（实际名称以 dsh-knowledge 源码为准）：fixed/recursive、heading-aware；每个 chunk 保留 document、position、text、metadata、source reference。
- [ ] Metadata Preservation：document title、source、URL/path、page、sheet、slide、section、heading、chunk order、timestamp、custom metadata。
- [ ] Background Jobs：Import / Parse / Embedding / Reindex / Refresh Job，大文件不阻塞 Web 与 Agent；轻量实现即可，不过度设计分布式队列。
- [ ] Job Recovery：处理 process crash、partial import、embedding interrupted、reindex interrupted；保证数据库不进入不可恢复状态。
- [ ] Duplicate Handling：same file、same URL、same content hash、same path changed 的去重与更新策略。
- [ ] Input Sources 基础：local file、text（directory / URL 在 Phase 4 深化）。
- [ ] 单元与集成测试：状态机迁移、解析器注册与分发、切块正确性、元数据保留、任务恢复、重复处理。

产出物：可导入文件并完成 parse → chunk → 持久化的核心流程；文档与 KB 的持久化模型；基础 Web API 或内部 service 层。

验收：KB/文档 CRUD 全部可用；文档生命周期状态可观测；崩溃后重启可恢复；重复导入不产生脏数据。

### Phase 3 — Retrieval（检索流水线）

**目标**：实现与 dsh-knowledge 行为等价的完整检索与上下文组装能力。

任务：

- [ ] Full-text Retrieval：实现等价 SQLite FTS5 / BM25 能力，不因有向量检索而省略。
- [ ] `EmbeddingProvider` 抽象：embedding 生成、向量持久化/索引、相似度搜索、top-k、filters；业务代码不直接依赖具体模型实现。
- [ ] 向量索引健壮性：model change、dimension change、reindex、index corruption。
- [ ] Hybrid Retrieval：按 dsh-knowledge 真实流程实现（预计 BM25 + Vector → RRF → MMR → Rerank，以源码为准，不能凭 README 猜测）。
- [ ] RRF：相同行为与可配置参数，确定性测试。
- [ ] MMR：relevance + diversity；覆盖 duplicate chunks、near duplicate、same document concentration 测试。
- [ ] `RerankerProvider` 抽象：candidate retrieval → reranker → final evidence；支持 enable/disable、configure、health check、fallback；reranker 失败默认不拖垮 Knowledge 服务。
- [ ] Context Composer：独立模块负责 ranking、source grouping、dedup、context formatting、metadata/citations、context window；最终不能简单返回 `[]Chunk` 给 Agent。
- [ ] Context Window / 相邻 chunk 扩展：命中 chunk 可按策略补前后相邻 chunk（如 #19/#20/#21），防止 context explosion。
- [ ] Anchor / Continuation：若 dsh-knowledge 存在此机制，先从源码与测试理解业务目的，再完整实现。
- [ ] Local Model Management（若 dsh-knowledge 有）：download、discover、configure、status、delete；模型文件归 Knowledge 所有。
- [ ] Retrieval Test 能力：输入 query、选择 KB、执行检索，可查看 BM25 candidates、Vector candidates、Fusion result、Rerank result、Final context；具体可视化以原项目为准。此功能为 P0 级可调试能力，不得省略。
- [ ] Explainability：检索测试与 API 尽量输出每个 chunk 为何被选中、各阶段 score、最终 rank、source。
- [ ] 测试：RRF 确定性、MMR 多样性、混合排序趋势、reranker 降级、context 长度控制、元数据引用；为关键算法建立 dsh-knowledge 行为对照测试（same input，比较功能、排序趋势、边界条件、错误语义；不要求浮点 score 完全一致）。

产出物：完整检索引擎、Context Composer、模型抽象层、Retrieval Test 后端与 API。

验收：混合检索全链路可用；reranker/embedding 故障可降级；Retrieval Test 能解释每阶段结果；关键算法行为与 dsh-knowledge 等价。

### Phase 4 — Advanced Ingestion（高级摄取）

**目标**：补齐复杂文档与外部来源的摄取、刷新与增量能力。

任务：

- [ ] OCR（若 dsh-knowledge 支持）：区分 native text extraction、OCR fallback、forced OCR；OCR failure 不破坏已有可解析文本。
- [ ] MinerU 或类似高级 PDF pipeline（若 dsh-knowledge 支持）：设计 optional provider，包含 health、availability detection、fallback；外部依赖不进入 Agent。
- [ ] URL ingestion：导入、刷新、错误语义与 dsh-knowledge 对齐。
- [ ] Directory ingestion：目录扫描、递归导入、刷新。
- [ ] Incremental Update：changed → update，unchanged → skip，避免每次全量重新 embedding；行为参考 dsh-knowledge。
- [ ] Semantic Chunking（若 dsh-knowledge 有）：独立策略、配置、测试、benchmark、fallback；不得因复杂而省略。
- [ ] 高级解析器补齐与外部 runtime 管理：可选安装、自动发现、健康检查、错误提示；不让 Agent Core 依赖。
- [ ] 测试：OCR 三种模式与失败降级、URL/目录刷新、增量判断、语义切块质量、坏文档容错。

产出物：高级摄取子系统、可选依赖管理、增量刷新机制。

验收：大文件与复杂文档处理不阻塞 Web/Agent；刷新按增量执行；OCR/MinerU 等可选组件缺失时主流程可用且提示清晰。

### Phase 5 — Agent Native Integration（Agent 原生集成，P0）

**目标**：通过 Extension Platform v1 将 Knowledge 的 Context、Tools、审批、事件、生命周期原生接入 shutu-agent，不建立第二套 Tool Registry。

任务：

- [ ] Native Context Provider：实现 `User Message → Agent Turn → Model Step → Extension Context Provider → shutu-knowledge retrieval → Evidence → ContextContribution → Agent Context Budget → LLM` 链路。
- [ ] Context Cadence 设计：在 `once_per_turn`、`before_every_model_call`、`on_user_input_change`、`after_tool_result`、`manual` 中按成本与语义选择合适默认，不一开始强制全部 `before_every_model_call`。
- [ ] `after_tool_result`：直接使用 Agent 提供的真实 `ToolResultBoundary` 与 per-provider at-most-once automatic consumption，不在 Knowledge 内猜 StepID 或维护 ToolResult sequence。
- [ ] Retrieval Request 权限最小化：只使用 SDK 真实授权的 session/turn/step/workspace/user input，不要求多余权限。
- [ ] Evidence 输出：`ContextContribution` 尽量包含 content、source、priority、metadata、token hint（字段以 Extension Contract 为准），支持模型解释证据来源。
- [ ] Token Budget 边界：Knowledge 内部控制 top-k、reranker candidates、context composing 并提供估算；最终 Agent Context Budget 由 Agent 控制。
- [ ] Knowledge Tools：按 Phase 0 审计结果实现完整 Tool inventory（候选：knowledge_search、knowledge_list_bases、knowledge_create_base、knowledge_delete_base、knowledge_add_document、knowledge_list_documents、knowledge_delete_document、knowledge_import_url、knowledge_refresh_url、knowledge_stats、knowledge_get_document、knowledge_read_document、knowledge_reindex_document、knowledge_reindex_base，以源码审计为准）。
- [ ] Tool Registry Integration：通过 Extension Tool Contribution / 现有 MCP-Extension 机制进入 Agent 原 Tool Registry。
- [ ] Tool Risk Metadata：每个 Tool 声明真实风险（search/read → read；create/import/reindex → write；delete → destructive），审批策略交给 Agent，不绕过 approval。
- [ ] Events：只订阅真正需要的 Agent Events（先审计 turn / tool / context / session.started 是否必要）；Native Context Provider 不通过 Event 模拟；禁止 subscribe all。
- [ ] Lifecycle：initialize、ready、health、shutdown、restart compatibility，支持作为 managed extension 运行。
- [ ] Core/Adapter 分层：Knowledge Core 不依赖 Agent-specific DTO；`extension.ContextRequest → KnowledgeQuery → Core` 映射，保证未来 standalone / CLI / Web / extension 多入口复用。
- [ ] Extension Integration Test：真实启动 `shutu-agent + shutu-knowledge` 两个外部进程（非 mock Contract），验证：发现、握手、health ready、tools 进入 registry、Context Provider 注入 evidence、Tool approval 生效、Events 工作、重启恢复、干净关停。

产出物：完整 Extension Adapter、Context Provider、Tools + 风险声明、事件订阅、生命周期实现、真实进程集成测试。

验收：真实 Agent 会话中自动 RAG 生效；工具调用经审批；Knowledge 崩溃不影响 Agent Core；移除/disable 后 Agent 一切正常（菜单、工具、context 注入消失）。

### Phase 6 — Web（Web UI 与原生导航）

**目标**：实现完整 Knowledge Web UI，并作为 Extension Web Contribution 动态出现在 Agent 界面。

任务：

- [ ] Knowledge Web UI 独立构建于 `shutu-knowledge/web` 自己的 pipeline，不编译进 `shutu-agent/web`。
- [ ] Agent 负责 Navigation、Reverse Proxy、Auth shell、Route；Knowledge 只负责业务 UI、Knowledge API、业务状态。
- [ ] 按 dsh-knowledge 实际能力实现页面（候选结构：Overview、Knowledge Bases、Documents、Import、Retrieval Test、Models、Settings，以审计为准）。
- [ ] Native Navigation：安装并启用扩展后，Knowledge 通过 Agent `/api/extensions` 等正式机制自动出现，不要求用户手改 Agent 菜单。
- [ ] Retrieval Test UI：query 输入、KB 选择、执行检索、展示 BM25/Vector/Fusion/Rerank/Final context 与各阶段 score。
- [ ] Models 与 Settings 管理：模型配置、状态、下载/删除（按审计结果）、各子系统配置。
- [ ] Web 测试：页面功能、API 契约、构建产物、Agent 内嵌导航与路由。

产出物：可独立构建的前端、Knowledge API、Agent 内动态菜单验证。

验收：安装即见 Knowledge 菜单；禁用/删除后菜单消失且 Agent 其它功能不受影响；Retrieval Test 可完整调试检索链路。

### Phase 7 — Equivalence Audit（等价性复审）

**目标**：从零重新对照 dsh-knowledge，发现并补齐开发期间遗漏的隐藏功能。

任务：

- [ ] 重新完整审计 `dsh-knowledge`（不依赖最初 inventory）：source tree comparison、tool comparison、config comparison、UI comparison、workflow comparison、test comparison。
- [ ] 逐项更新 `docs/dsh_knowledge_equivalence_matrix.md`，所有目标功能必须 `PASS` 或有明确 `BLOCKED / NOT APPLICABLE` 理由，无未解释遗漏。
- [ ] 对发现的遗漏排期补齐实现，并补充对应测试与文档。
- [ ] 复审许可证与源码复用记录，确认 `THIRD_PARTY_NOTICES.md` 与 `docs/source_reuse_inventory.md` 完整。
- [ ] 复审 Agent Contract Gap Report，确认所有 BLOCKED 项均有通用改进建议且未私自绕路。

产出物：更新后的能力清单、等价矩阵、Gap Report、补齐实现与测试。

验收：矩阵无 `PARTIAL` 残留（除非用户明确接受）；每个非 PASS 项均有清晰理由与证据。

### Phase 8 — Hardening（加固与性能）

**目标**：把功能可用提升为生产可靠，覆盖故障、资源与性能边界。

任务：

- [ ] 并发与稳定性：race 检测、crash/restart、large documents、bad documents、model unavailable、index corruption、migration、cancellation、resource cleanup。
- [ ] Failure Isolation 验证：至少覆盖 Knowledge crash、Embedding unavailable、Reranker timeout、Bad document、OCR failure、Web unavailable，均不导致 Agent Core crash。
- [ ] Removal Test：移除/disable `shutu-knowledge` 后 Agent 正常运行、菜单消失、工具消失、context 注入消失、其它功能不受影响。
- [ ] Upgrade Independence Test：模拟 Agent version A → B（Protocol v1 兼容范围内）Knowledge 不改代码仍可运行；以及 Knowledge 内部升级（新 parser/reranker/embedding/chunk strategy）Agent 无需修改。
- [ ] Observability：记录 import duration、parse duration、chunk count、embedding duration、retrieval duration、rerank duration、candidate count、context count、model error、job failure；同时通过 Extension observability 暴露集成层状态。
- [ ] Benchmark：document ingestion、chunking、embedding、BM25 query、vector query、hybrid retrieval、rerank、end-to-end RAG；不用极小数据集宣称性能优秀。
- [ ] Retrieval Benchmark Dataset：可重复，覆盖 exact lexical、semantic、multi-document、near duplicate、long document、Chinese、English、mixed language（支持其它语言则加入）。
- [ ] 安全复查：日志脱敏、凭证处理、路径安全、Web 输入校验、错误信息不泄露敏感内容。
- [ ] 完善测试层级：Unit、Integration、Extension Integration、Web、E2E、Regression、Benchmark 全部建立并纳入 CI。

产出物：加固补丁、benchmark 套件与数据集、失败注入测试、可观测指标。

验收：全部加固项通过；关键指标有可重复 benchmark 报告；所有 Gate 检查就绪。

---

## 5. Definition of Done（Gate A–J）

全部满足才可声明 `SHUTU-KNOWLEDGE V1 READY`：

| Gate | 内容 | 通过标准 |
|---|---|---|
| A | One-way dependency | `shutu-knowledge → shutu-agent public Extension API` 成立；Agent 无任何 Knowledge modification |
| B | No internal dependency | `shutu-agent/internal` 搜索结果生产 import 为 0 |
| C | Capability equivalence | 矩阵所有目标功能 PASS，或 BLOCKED/N/A 有明确理由；无未解释遗漏 |
| D | Native Context | 真实 `User → Agent → Knowledge retrieval → ContextContribution → Model` 链路通过 |
| E | Tools | Knowledge Tools 进入 Agent 原 Tool Registry；Approval 生效 |
| F | Web Native | Knowledge 菜单动态出现，未修改 Agent frontend |
| G | Lifecycle | start / ready / health / restart / shutdown 全部通过 |
| H | Failure Isolation | Knowledge crash、Embedding unavailable、Reranker timeout、Bad document、OCR failure、Web unavailable 均不导致 Agent Core crash |
| I | Removal | 删除 Knowledge 后 Agent 仍健康 |
| J | Independent Upgrade | Agent 与 Knowledge 均可独立版本升级 |

最终验收结论只允许二选一：`SHUTU-KNOWLEDGE V1 READY` 或 `SHUTU-KNOWLEDGE NOT READY`。若 NOT READY，必须列出 remaining capability gaps、Agent Contract gaps、failed tests、license blockers，不得使用"基本完成"等模糊表述。

---

## 6. 文档交付清单

最终至少交付：

```text
README.md
shutu_knowledge_implementation_report.md
THIRD_PARTY_NOTICES.md
docs/
├── architecture.md
├── agent_integration.md
├── dsh_knowledge_capability_inventory.md
├── dsh_knowledge_equivalence_matrix.md
├── agent_contract_mapping.md
├── agent_extension_gap_report.md
├── ingestion.md
├── retrieval.md
├── models.md
├── web.md
├── configuration.md
├── deployment.md
├── troubleshooting.md
└── testing.md
```

实现报告必须回答：

1. 是否实现 dsh-knowledge 全部目标能力（逐项 PASS / PARTIAL / BLOCKED / N/A）。
2. 是否修改过 shutu-agent（必须 NO；YES 即 FINAL RESULT = FAIL）。
3. 是否存在 `import shutu-agent/internal/...`（必须 NO）。
4. 是否存在 Agent Contract Gap（列出 Gap ID，未因 Gap 私自修改 Agent）。
5. 是否真正实现 automatic RAG + tools + approval + web + events + lifecycle。
6. 是否经过真实 `shutu-agent + shutu-knowledge` 外部进程集成测试（提供测试证据）。

---

## 7. 测试策略总览

必须包含的测试层级：

- **Unit**：算法与模块（chunking、RRF、MMR、状态机、duplicate 判断等）。
- **Integration**：存储、任务、解析、检索端到端（项目内）。
- **Extension Integration**：真实双进程启动 shutu-agent + shutu-knowledge，禁止 mock Extension Contract。
- **Web**：UI 功能、API 契约、构建与内嵌导航。
- **E2E**：用户工作流（导入 → 检索 → 自动 RAG → 工具调用 → 审批 → Web 管理）。
- **Regression**：关键行为对照 dsh-knowledge 的输出趋势与错误语义。
- **Benchmark**：见 Phase 8 清单，配套可重复数据集。

---

## 8. 提交与工作流规范

1. 每阶段一次或多次聚焦提交，提交前完成 self-review 与测试。
2. 所有 commit 只出现在 `shutu-knowledge` 仓库；严禁跨仓库提交。
3. 每阶段开始与结束检查参考仓库基线（HEAD + porcelain status）。
4. 任何架构红线违规：立即停止、修复、在文档中记录教训。
5. 遇到 Agent Contract Gap：走 2.1 协议，不等待人工确认（除非涉及安全、许可证或真正无法通过现有 Contract 解决的阻塞）。

---

## 9. 关键风险与应对

| 风险 | 应对 |
|---|---|
| Extension v1 缺少所需能力 | Gap Report + 通用改进建议；BLOCKED 项不绕路实现 |
| dsh-knowledge 功能范围被低估 | Phase 0 全量源码审计 + Phase 7 从零复审双保险 |
| 许可证冲突 | 先审计再复用；冲突时基于行为重实现并记录 |
| OCR / 高级解析外部依赖复杂 | optional provider + health/availability detection + fallback，不影响主流程 |
| 大文件与后台任务拖垮服务 | 独立 Job 体系 + 恢复机制 + Phase 8 加固 |
| 模型不可用导致整体不可用 | Provider 抽象 + health check + 降级策略（reranker 默认可降级） |
| 双向依赖 / 内部耦合 | 每 Gate 复查 import 与单向依赖；Core 与 Extension Adapter 严格分层 |

---

## 10. 执行顺序摘要

```text
Phase 0  Source Audit          → 产出五份核心文档 + 许可审计
Phase 1  Project Foundation    → 骨架 / 存储 / 迁移 / CLI / 握手
Phase 2  Knowledge Core        → KB / 文档生命周期 / 解析 / 切块 / 任务
Phase 3  Retrieval             → BM25 / 向量 / 混合 / RRF / MMR / Rerank / Context
Phase 4  Advanced Ingestion    → OCR / URL / 目录 / 增量 / 语义切块 / 高级解析
Phase 5  Agent Integration     → Context Provider / Tools / 审批 / 事件 / 生命周期
Phase 6  Web                   → 完整 UI / 原生导航 / Retrieval Test
Phase 7  Equivalence Audit     → 从零复审并补齐遗漏
Phase 8  Hardening             → 故障 / 性能 / 隔离 / 移除 / 升级独立
Final    Gates A–J + 实现报告  → V1 READY 或 NOT READY
```
