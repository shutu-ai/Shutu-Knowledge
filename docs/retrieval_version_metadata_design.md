# 检索版本元数据独立设计建议

本次审计确认，当前版本信息主要来自文件名（例如 `GDE_26.3.0_xxx.pdf`），chunk 表没有独立的 documentVersion、documentFamily 或 supersession 字段。该长期能力不应阻塞当前检索修复，也不建议在没有真实数据迁移样本前直接猜测版本规则。

建议后续增加以下 document 级字段：

```text
product
documentFamily
version
status = current | superseded
supersedes
supersededBy
```

实施要求：

1. 版本解析应是显式 parser/metadata step，文件名只作为低置信度候选；人工命名或导入 API 提供的 metadata 优先。
2. version filter 应先缩小 scope，再参与 ranking；`26.3` 不应被当作普通高权重全文词反复写入 embedding。
3. chunk 继承 document 的 version/product metadata，但用户正文和 embedding representation 不应无限重复公共词（如 GDE、26.3）。
4. superseded 文档默认可检索但降低权重，只有用户显式要求历史版本时才优先；current 文档不能因文件名排序偶然获胜。
5. migration 必须保留未知值为空并保证旧数据库可读；先增加 nullable columns 和回填审计，再切换默认过滤策略。
6. benchmark 至少覆盖同一 documentFamily 的 current、superseded、无版本和错误文件名四类样本，并分别测 document/chunk/answer-bearing Recall@k、MRR、误选旧版本率。

建议接口新增为可选字段，保持当前 `Chunk`/`SearchHit` JSON 兼容；在 schema migration、导入 preview、检索 debug 和负向版本测试同时具备后，再启用版本排序。
