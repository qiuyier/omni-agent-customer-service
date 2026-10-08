# 首轮真实 Embedding 检索评测

运行日期：2026-09-09。仅评测 FAQ 检索，不评测客服生成回答或生产准确率。

## 结论

使用通用 OpenAI-compatible Embeddings 接入，阿里云百炼北京业务空间专属端点实测成功。
基线集 20 条、预留验证集 6 条的语义检索结果均与人工相关集合完全匹配。
两组仍只有 3 条虚构 FAQ，不能据此宣称真实客服准确率为 100%，也不能证明阈值是最优值。

## 固定条件

- 模型：`qwen3.7-text-embedding`；实际响应维度：1024。
- `EMBEDDING_BATCH_SIZE=16`，`KNOWLEDGE_MIN_SCORE=0.7`，Top K = 3。
- 文档格式：`title-newline-content-v1`，不把 ID、source、keywords 送入向量化。
- 每条查询超时：10 秒；评测整体预算：5 分钟（不含单独的索引初始化）。
- FAQ、基线标签、预留标签、模型及阈值在两轮之间均未修改。
- 关键词对照在同日使用同一份输入重跑；两种检索器的输入摘要与 K 一致。
- 真实密钥与业务空间 ID 仅保留在本地配置，报告不记录 endpoint 或凭据。
- 两次语义命令各自初始化内存索引，不复用上一进程的向量；重跑可能再次计费。

## 成绩

### 原 20 条基线

| 指标 | 关键词 | 语义检索 |
| --- | --- | --- |
| 首条命中（有相关资料） | 10/15（66.7%） | 15/15（100%） |
| 正确无命中（无相关资料） | 2/5（40.0%） | 5/5（100%） |
| 相关集合完全匹配 | 12/20（60.0%） | 20/20（100%） |
| 有漏检的问题数 | 4 | 0 |
| 含无关命中的问题数 | 4 | 0 |

本次语义检索覆盖了关键词版本漏掉的口语改写和验证邮件问题，并在这些样例中正确
排除了歧义词、代码变量名及被否定的主题。该结论仅针对报告中的样例。

### 预留 6 条验证题

| 指标 | 关键词 | 语义检索 |
| --- | --- | --- |
| 首条命中（有相关资料） | 1/4（25.0%） | 4/4（100%） |
| 正确无命中（无相关资料） | 1/2（50.0%） | 2/2（100%） |
| 相关集合完全匹配 | 1/6（16.7%） | 6/6（100%） |
| 有漏检的问题数 | 3 | 0 |
| 含无关命中的问题数 | 2 | 0 |

预留集在固定参数下单独运行，没有根据其结果调整标签或阈值；后续若拿这 6 条调参，
就不应继续把它们当作未参与调参的验证数据。

## 原始报告与输入快照

以下 JSON 为本次命令输出，包含逐题预期、实际引用 ID 和指标。已按逐题结果复核汇总计数。

- [关键词基线](eval_runs/2026-09-09/keyword-baseline.json)
- [语义基线](eval_runs/2026-09-09/semantic-baseline.json)
- [关键词预留集](eval_runs/2026-09-09/keyword-holdout.json)
- [语义预留集](eval_runs/2026-09-09/semantic-holdout.json)

加载后规范 JSON 的 SHA-256（不是原始文件字节摘要）：

- FAQ：`c063ea5e0fe3c5a7ebe0ffc7ab496ca6a534fad946f2e9e6e3cf2f6bafa8563a`
- 基线标签：`f7a88d24a3c110b04050aaa9eaa0536166314fbea7743dc3479451be7c303438`
- 预留标签：`50a4b0dee62ee26d847018e39777b2817e0a2b8c33f51ec143bc5551767c423f`

[原关键词基线记录](eval_baseline.md)保持不变。模型服务端别名后续可能更新，重复执行
也不保证与本次成绩相同；这里记录的是请求模型 ID，不是不可变的模型权重版本。

## 复现

在项目根目录、已安全加载本地环境配置后运行。后两条命令会向配置服务发送 FAQ
和问题，可能产生费用；无需启动客服服务器或调用客服回复模型。

~~~bash
go run ./cmd/knowledge-eval -retriever keyword -format json
go run ./cmd/knowledge-eval -retriever keyword -cases examples/regression/knowledge/eval_holdout.json -format json
go run ./cmd/knowledge-eval -retriever semantic -allow-embedding-api -timeout 5m -format json
go run ./cmd/knowledge-eval -retriever semantic -allow-embedding-api -cases examples/regression/knowledge/eval_holdout.json -timeout 5m -format json
~~~

## 尚未验证

- 本轮没有采集单次请求耗时、P50/P95、Token 用量或账单；超时预算不是实测延迟。
- 没有使用真实业务语料、进行并发压测或验证跨租户隔离。
- 检索正确不等于生成回答正确，也不等于已经执行改密、开票等业务操作。
- 本次不修改通用适配器，不把这组模型成绩写进自动化测试，也不调整阈值。
- 下一步宜增加更贴近业务、人工脱敏且有独立标签的数据，以及真实回复/SSE 的人工验收；
  是否需要阈值调整、混合检索、切分、缓存或向量数据库，应由新证据决定。
