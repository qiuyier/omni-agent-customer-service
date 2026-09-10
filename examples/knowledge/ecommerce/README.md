# 青禾生活演示店：电商模拟测试材料 v1

这是一套由助手编写的**虚构测试数据**，不是用户的真实业务资料、真实商家政策或法律意见。
店名、商品、金额、时效和入口名称均用于模拟，未采集真实客户或订单数据。
它用来检验通用客服系统，不把系统改成电商专用，也不能证明生产准确率。

## 材料组成

| 文件 | 内容 | 用途 |
| --- | --- | --- |
| [faq.json](faq.json) | 16 条短知识资料 | 可直接由现有 FAQ 加载器使用 |
| [eval_cases.json](eval_cases.json) | 20 题：18 有相关资料、2 无相关资料 | 开发/诊断集 |
| [eval_holdout.json](eval_holdout.json) | 10 题：7 有相关资料、3 无相关资料 | 单独保留的回归题，不按结果反向改标签 |
| [reply_cases.json](reply_cases.json) | 从上述题目选取 8 题 | 普通回复、SSE、顺序重放及内容审阅 |

30 题指开发集和保留集的不同问题之和，8 道回复题是子集，不额外计入。
保留题与开发题不重复，但它们与资料均由同一助手编写；这不是独立人工标注或盲测。
所有 expected_ids 都是待审阅的参考相关性标签，不是不可争议的真实业务金标准。

资料覆盖：人工客服与节假日、登录改密、运费门槛、仓库发货、配送范围、物流、
取消订单、改地址、退货、破损售后、退款阶段、发票、优惠券，以及两款虚构商品。
商品示例为云杉随行杯500毫升和原色棉柔毛巾，不含实际售价或库存。

每条 FAQ 自带虚构声明，source 采用 `synthetic/qinghe/v1/...`：
它是内部溯源标识，不是可访问的网址。测试题和审阅答案不会作为知识库内容送给模型；
只有被检索的 FAQ 与客户问题进入回复链路。

## 标签与答案边界

每题 note 同时给出预期回答要点和禁止事项，供内容审阅，而不是用关键词断言模型正确。

- **有资料且充分**：可以解释明确给出的规格、流程或计算规则。
- **相关但不充分**：物流入口、节假日安排和改地址流程有参考价值，但不能确认实际状态或执行操作。
- **无相关资料**：WiFi、未收录产品参数、当前库存、未来活动等应明确不知道。
  同属一个店铺或同款商品并不自动使资料相关。
- **多意图、否定与改写**：检查两条资料一起命中、被否定主题不命中、口语表达以及99元边界。
- **无工具能力**：不得承诺代查库存/物流、改地址、退款、转人工或开票；不为不存在的查询索取订单号、地址、电话、税号或验证码。

例如“下一个节假日几点值班”的预期引用是客服时间条目，但预期答案仍是
“现有资料不能确认，以公告为准”，不是常规工作日的时间。
库存题不应因为商品规格写了“雾蓝”就回答“有货”。
退货规则仅为模拟流程，不能用来判定真实客户的法定权利；条件不适用不等于应当一律拒绝。

## 本地离线检查

首轮已完成：开发题相关集合匹配17/20、保留题6/10，失败项与输入摘要见
[离线基线记录](baseline.md)。这不是模型回答验收成绩。

从项目根目录执行，无需 API Key、不读取本地模型配置、不产生模型费用：

~~~bash
go run ./cmd/knowledge-eval -faq examples/knowledge/ecommerce/faq.json -cases examples/knowledge/ecommerce/eval_cases.json -retriever keyword -format json
go run ./cmd/knowledge-eval -faq examples/knowledge/ecommerce/faq.json -cases examples/knowledge/ecommerce/eval_holdout.json -retriever keyword -format json
~~~

离线检索分数反映关键词检索的限制，不按测试题不断补关键词来伪造满分。
资料合法、程序正常运行不等于检索准确，也不等于生成回答正确。

## 真实模型验收

2026-09-10已完成30题语义检索评测：开发题12/20、保留题7/10集合匹配。
GLM运费题普通/SSE和4次重放通过；第二题退货多带入质量问题引用，严格验收失败并停止。
其余回复尚未执行，完整结果、措辞问题与输入摘要见[真实验收记录](acceptance.md)。

同日新增[开发集候选分数诊断](diagnostics.md)：20题逐题结果与前次一致，仍为12/20。
双意图题的两条正确资料排第1、2但均被阈值过滤，WiFi题则误选登录密码资料；
没有候选因Top-K被截断，单一阈值不能同时修好这两题。本轮只做诊断，未改资料、
标签或检索策略，未调用GLM或重跑保留集。原始分数与 `-diagnostics` 用法见该记录。

这套材料没有改变 `.env.local`、默认 FAQ 路径、已有 Prompt 或原来的 3 条演示 FAQ。
若要比较语义检索，先安全加载独立 Embedding 配置，再显式允许联网；不要公开密钥。
以下命令**可能产生费用**：

~~~bash
go run ./cmd/knowledge-eval -faq examples/knowledge/ecommerce/faq.json -cases examples/knowledge/ecommerce/eval_cases.json -retriever semantic -allow-embedding-api -timeout 5m -format json
go run ./cmd/knowledge-eval -faq examples/knowledge/ecommerce/faq.json -cases examples/knowledge/ecommerce/eval_holdout.json -retriever semantic -allow-embedding-api -timeout 5m -format json
~~~

先检查检索结果，再运行回复验收，不因真实服务返回了内容就默认成功：

~~~bash
go run ./cmd/reply-check -faq examples/knowledge/ecommerce/faq.json -cases examples/knowledge/ecommerce/reply_cases.json -allow-model-api -allow-embedding-api -timeout 10m
~~~

8 题每题两种生成模式，完整运行最多涉及16次首次应用层回复生成和32次完成后重放，
另有索引与查询 Embedding 调用；不是计费次数。工具遇到引用不符或其他机械失败会提前停止，
不会自动完成所有题，可能已产生前序费用。完成后仍需逐题审阅 note 中的事实与能力边界。
这8题含保留题：一旦使用其输出调 Prompt 或阈值，它们就不再是未见保留题。

## 采用方式

先作为隔离的演示数据包使用，不自动替换运行中的知识库。未来有真实业务资料时，
逐条替换虚构规则并由业务负责人复核；保留输入快照、版本、相关性标签和失败记录。
