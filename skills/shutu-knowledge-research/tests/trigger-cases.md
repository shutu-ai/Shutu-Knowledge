# Trigger Test Cases

Lightweight expectation set for skill triggering. `should: yes` means the Skill
should activate (or the agent should reach for Shutu-Knowledge tools); `should: no`
means ordinary capability must answer and the Skill must stay dormant. Assertions
reference the SKILL.md trigger description, not model internals.

## Should trigger

| # | Prompt | Why |
|---|---|---|
| 1 | 我们文档里 SmartCare 7.1 的下行吞吐率下降怎么排查？ | product-versioned doc question |
| 2 | knowledge base 里哪个文档讲了 KPI 计算公式？ | explicit KB query |
| 3 | Compare the retry policy between version 0.2 and 0.3 in our docs. | version comparison |
| 4 | 这几个文档里 SDR 计数器和 KPI 的关系是什么？ | cross-document relation question |
| 5 | What error codes does the ingest service document for corrupt files? | exact parameter/error lookup in indexed docs |
| 6 | 按文档说明，预算不足时作业会怎样？链路里还有哪些模块受影响？ | multi-hop impact chain |
| 7 | Summarize what our knowledge base says about the retention policy. | topic summary over KB |
| 8 | 用户表 user_info 里 order_status 字段的枚举含义，数据字典里有吗？ | structured data dictionary field |
| 9 | 本地文档中 128k context window 是哪个版本引入的？ | versioned fact in KB |
| 10 | 帮我查一下文档里写的备份命令和前置条件。 | procedure lookup with preconditions |
| 11 | docs 里有没有内容和我们讨论的幂等性设计冲突？ | conflict check against KB |
| 12 | Use the Shutu-Knowledge MCP to find the citation for the 15-minute refresh claim. | explicit tool invocation |

## Should not trigger

| # | Prompt | Why |
|---|---|---|
| 1 | 帮我写一首关于秋天的诗。 | creative writing |
| 2 | What is the capital of France? | general world knowledge |
| 3 | 把这段话翻译成英文。 | translation |
| 4 | 帮我优化这个 Python 函数的性能。 | code editing on user-provided code, no KB lookup requested |
| 5 | 今天天气怎么样？ | real-time info |
| 6 | 讲讲你自己的能力。 | meta conversation |
| 7 | 帮我把这个文件夹里所有文件重命名。 | filesystem action, not knowledge query |
| 8 | 1 到 100 的质数有哪些？ | pure computation |
| 9 | 我想聊聊昨晚的球赛。 | small talk |
| 10 | Delete the knowledge base called test. | management action; Skill is read-only and points to CLI instead |
| 11 | 用中文总结上面我说的三点。 | operates on conversation content only |
| 12 | 搜索引擎里查一下最新的新闻。 | public web, not the local knowledge bases |