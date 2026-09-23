# 回答依据校验：独立实验，不用于在线拦截

本实验审查固定的候选回答，不重新回答客户，也不修改、拦截或重试正式回复。
它是模型审查实验，不是通过关键词判断正确性的规则引擎。

最新改动为[结构与诊断 v5](../examples/knowledge/webchat/grounding-structure-v5.md)：拆分事实与通用建议，记录失败阶段。
六题定向回归有四题符合预期、一题误判、一题格式错误；还不能认定稳定或上线。
此前[建议边界 v4](../examples/knowledge/webchat/grounding-advice-v4.md)的十二题结果保留，不与本轮不同样本数直接比较。

**此前 v3 验收：冻结版本已完成原十二题和八条新题验收，未全部通过。**
20 题有 14 题符合预期，另有误判、不确定和输出契约错误；设备查询存在两阶段共同漏检。
详见[冻结验收报告](../examples/knowledge/webchat/grounding-frozen-validation.md)。
此前[六题修正记录](../examples/knowledge/webchat/grounding-judge-v3.md)保留原范围，不能代替本轮结论。

## 标准与结果

审查模型需要覆盖回答中的实质性断言，返回连续原句、类别、资料原文与理由：

| 断言类别 | 判定标准 |
| --- | --- |
| `supported` | 当前系统事实有资料支持；必须提供逐字的资料摘录。 |
| `unsupported` | 未被资料支持或与其冲突；包括隐含的功能存在、入口、验证机制、操作效果和假执行。 |
| `uncertain` | 语义无法明确消歧；不是资料缺失的替代标签。 |
| `general` | 明确表达未知、纠正无依据说法、通用安全提醒或不承诺具体渠道能力的人工咨询建议。 |

客户猜测与历史助手说法不作为系统事实证据。候选回答或资料里的“忽略规则”等文字
是数据，不是指令。这是提示层约束，不是已经证明有效的注入防御。

程序派生回答级结果：存在 `unsupported` 则判无依据，否则存在 `uncertain` 则待判断，
其余为 `clear`。调用失败、非法 JSON、未知类别、空断言、不存在的回答/资料摘录均为 `error`。
格式错误不重新调用模型；传输失败停止整个实验，输出已完成部分。

schema v3 增加：

- `checks`：逐个描述具体细节，标记 `entailed`、`not_stated` 或 `contradicted`。
  `detail` 可以语义改写；回答 `quote` 和证据 `evidence` 必须逐字匹配各自原文。
  支持和冲突都需要原文证据；缺失项证据为空。发现缺失或冲突时，即使模型顶层写
  `supported`，程序也判 `unsupported`。
- 全文覆盖：未被任何 `quote` 覆盖的文字或数字使结果至少为 `uncertain`，不能直接放行。
  标点及排版符号忽略。这是字面覆盖检查，不能保证每个隐含断言都提取正确。
- 独立细节复核：第一轮格式有效且尚未判无依据时，再调用一次模型；它只接收原始数据，
  不接收第一次的判断或理由，专门寻找新增的系统机制、渠道和效果。发现问题可以否决；
  未发现问题不能把第一轮的 `uncertain` 升为 `clear`。第一轮不合规或已判无依据则不复核。
- 复核失败不放行，也不自动重试；每题最多两次请求。两次审查使用同一配置模型，
  是上下文隔离，不是独立模型集成，可能仍有共同盲点。

schema v4 再增加 `advice_kind`：`none`、`information_seeking`、`capability_use`。
`general` 项必须明确标注；模型标为能力使用时，不得通过 `general` 跳过细节证据。
其他类型允许该字段省略，但提示要求逐项填写。非能力类 `general` 仍不得携带细节事实检查。
v3 原始报告保持归档格式，不应用 v4 的必填规则重新计算成旧结果失败。
两阶段使用同一通用建议定义；全文覆盖检查没有放宽。边界语义仍依赖模型，不是确定性证明。

schema v5 增加：

- `advice`：独立通用建议数组，每项只有 `quote`、`advice_kind`、`reason`。
  只接受 `none` / `information_seeking`，禁止 `checks`、`evidence` 等事实字段。
  模型应把事实放 `claims`、通用建议放 `advice`；解析器兼容原有 `general` 项，保留原校验。
  两个数组合计 1–64 项，合并原句覆盖全文；事实证据和全文覆盖门槛没有降低。
- `failure`：`phase` 区分首审 `primary` 与复核 `review`；`stage` 记录请求构建、发送、
  HTTP 状态、响应读取、响应 JSON、结束状态、首审解析、复核解析或复核校验。
  `code` 为受控错误码，HTTP 失败另有 `http_status`。不存储传输错误原文、URL、鉴权头或供应商错误正文。
- 复核失败可保留通过结构校验的首审 `raw`、`claims`、`advice`，但回答级仍为 `error`，
  不自动重试，停止批次。首审普通结构错误记录为 `primary/judgment_parse`，继续下一样例。
  此前 v4 的泛化调用失败没有足够信息，不能靠新字段追认旧失败原因。

结构只限制字段，不会自动识别模型把事实错误放入 `advice`；本轮实测已观察到这种问题。

本地结构校验只验证原文包含关系，**不能证明语义支持、断言覆盖完整或模型分类正确**。
例如模型遗漏错误句子，或者将无依据步骤误标为 `general`，仍可能漏检。
不能把 `clear` 当成事实正确的保证。

## 样例设计

[样例文件](../examples/knowledge/webchat/grounding_judge_cases.json)固定 12 条、逐条标注：

- 原样保留上一轮六个真实回答：三条有问题、三条符合本轮标准。
- 三组配对正例：候选回答完全不变，只补全缺失依据，测试是否依赖资料而非敏感词。
- 图书馆规则冲突负例、候选回答中的指令注入负例、无资料情况下安全回答正例。

共 5 条 `unsupported`、7 条 `clear`。标注为开发者/助手审阅的实验基线，不是业务方最终签收。
`id`、`expected`、`rationale` 和 `origin` 不发送给审查模型。
只发送资料、历史、候选回答，以及独立审查提示；不发送客服生成提示或判分标签。

这是开发集，不是隐藏测试集。上线决策前还需要冻结标准后新增的独立保留集、人工复核，
并评估长回答遗漏、不同语言、资料冲突和其他位置的提示注入。

新增[四题迁移样例](../examples/knowledge/webchat/grounding_judge_transfer.json)，
以及本轮实际测试的[六题组合](../examples/knowledge/webchat/grounding_judge_targeted.json)。
四题在本轮首次调用前固定，但其结果已用于开发，因此后续不能称为未见过的隐藏测试。

另有[冻结前固定的八条新题](../examples/knowledge/webchat/grounding_judge_holdout_v3.json)，
用于不改规则的本轮验收；不是第三方独立盲测。原十二题与新八题必须分开运行，
合计最多 40 次模型请求；实际结果见上述冻结报告。

## 运行

默认完全不联网，不读取模型配置，只验证样例并输出计划：

```sh
go run ./cmd/grounding-check
```

真实实验必须显式开启；只使用已经导出的模型环境变量，不自动加载 `.env`。
该独立原型只支持显式配置的 HTTPS Chat Completions 地址：
`AI_PROVIDER`、`AI_PROTOCOL=chat_completions`、`AI_MODEL`、`AI_BASE_URL`、`AI_API_KEY`
（或已有供应商密钥变量）。不在命令行放密钥。

```sh
go run ./cmd/grounding-check \
  -cases examples/knowledge/webchat/grounding_judge_cases.json \
  -allow-model-api -timeout 10m
```

最多 12 条样例、每条最多两次请求（整组最多 24 次），无 SDK 重试、不跟随重定向；每次超时 75 秒，
整体超时最多 15 分钟。没有 Embedding、检索、数据库或业务工具调用。
`AI_SYSTEM_PROMPT` 不替换审查提示；正式客服的提示与 provider 逻辑未改变。
真实实验会将样例发给所配置的供应商，只使用虚构或明确授权的资料。

可显式设置实验专用 `GROUNDING_REASONING_EFFORT=low|high|max`；不设置时不发送该参数，
是否支持由所选供应商/模型决定，工具不自动转换或降级。此配置不影响正式客服模型。
本轮 GLM 使用 `low`，不是证明所有模型都适用。复现本轮六题：

```sh
GROUNDING_REASONING_EFFORT=low go run ./cmd/grounding-check \
  -cases examples/knowledge/webchat/grounding_judge_targeted.json -allow-model-api
```

stdout 为报告 JSON，stderr 为进度。报告包含提示、样例、原始模型文本、逐条结果、
提示与样例哈希，不保存 API Key 或供应商 URL。报告内容可能包含样例中的个人信息，
不要把真实客户资料未经授权放入样例或提交到仓库。

`complete` 仅表示所选模式已运行完毕，不代表质量通过；默认模式没有任何语义判断。
schema v3 的 `calls` 为已尝试的样例数，`provider_requests` 才是两阶段合计 HTTP 请求尝试数，
均不代表供应商成功计费数；旧 schema 的一阶段记录没有这个区别。摘要分别记录命中、漏检、误报、正确放行、
无法判断和错误；不把后两类计为通过。未执行样例不会计入任何正确项。
实验跑完即使有误报/漏检也可正常退出，必须阅读结果；不能以退出码作为上线验收门槛。

本轮边界样例位于 `examples/knowledge/webchat/grounding_advice_cases.json`；
可用原有 `-cases` 参数选择，一组最多 24 次请求。完整数据、分批运行和失败信息见 v4 报告。

## 实现阅读顺序

1. 样例 JSON：先理解预期标准与配对设计。
2. `cmd/grounding-check/main.go`：`judgePrompt` → `judgeInput` → `parse` → `aggregate` → `run`。
3. `cmd/grounding-check/main_test.go`：默认不联网、标签隔离、错误不是通过、HTTP/超时与无重试。

此命令不被服务器或客服应用层调用。即使后续样例全部通过，也要先评估同模型审查的
共同盲点、延迟/费用、误拦及不确定结果的处理，再决定是否设计运行时试点。
