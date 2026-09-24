# 回答依据校验：独立实验，不用于在线拦截

本实验审查固定的候选回答，不重新回答客户，也不修改、拦截或重试正式回复。
它是模型审查实验，不是通过关键词判断正确性的规则引擎。

## 按类型区分的首审契约（默认关闭）

最新 [v2 完整验收](../examples/knowledge/webchat/grounding-typed-primary-v2.md)：定向七题本次全通过，
同版本二十题完整回归十七题符合预期，另有设备漏判、覆盖不确定、非法字段错误各一题。
仍未达到上线条件。此前 [v1 定向记录](../examples/knowledge/webchat/grounding-typed-primary-v1.md)保留原结果。

`-typed-primary` 是替换首审输出契约的实验，不增加分类调用，也不叠加在旧首审提示词上。
它与分类、分类辅助及回放诊断模式互斥。正式客服不使用该模式。

```sh
go run ./cmd/grounding-check -typed-primary \
  -cases examples/knowledge/webchat/grounding_typed_cases.json
```

默认只做本地校验；`-allow-model-api` 才调用模型，每题最多首审和独立复核两次。
输出使用 `segments`，每项包含连续原文 `quote`、`scope` 和 `reason`：

- `system_fact`：系统规则、功能、操作、渠道、机制或执行结果，肯定和否定都需要核验。
  只有此类型能携带 `assessment`，包含 status/evidence/checks。
- `assistant_limitation`：当前助手不能代办或没有工具的说明。
- `knowledge_limit`：资料局限和无法确认，不肯定或否定系统能力。
- `inquiry`：一般求证，不承诺渠道可办理。
- `social`：礼貌、安全提醒等。
- `correction`：撤回以前无依据说法。

后五类必须完全省略 assessment，连 null 也拒绝；未知字段不被容错删除。
业务事实的 supported 必须有非空原文证据，supported/unsupported 都要列出细节检查；
细节缺失/冲突仍会覆盖不一致的 supported。原来的原文引用和全文覆盖检查不放宽。
通过校验后仅做表示转换，复用旧结果聚合及复核；不修补证据、不更改模型给出的类型。
模型仍可能把业务事实错标为非事实，因此复核必须看到原始全文，不能因非事实类型直接放行。

类型契约报告 schema 8，模式 `typed_primary_experiment`。`typed_primary_raw` 是首审实际输出，
`raw` 是通过校验后的兼容表示与可选复核结果，二者不能混淆。
首审结构失败标为 primary / typed_primary_parse，不重试，停止当前批次；
有原始模型正文时在大小限制内保留，传输错误正文仍不保存。
这是本地严格类型校验，不是供应商强制结构生成或语义正确性保证。

typed 首审 v2 去掉了共享旧枚举文本的拼接，提示只包含六种合法 scope；解析器仍拒绝旧枚举，
没有增加别名容错。混合建议按动作拆分：提交、查询个人记录、改变状态需要核验渠道能力，
一般询问规则或咨询人工不能为这些操作背书，也不从并列句自行推断人工保证办成。
这些是跨行业语义规则，不是按“工单”等词写特例。独立复核、证据和覆盖门槛保持不变。

清单复核候选仅在显式指定 `-typed-primary -inventory-review` 时替换第二次调用，不增加第三次调用。
单独使用 `-typed-primary` 仍使用原独立复核。候选报告 schema 9，模式 `typed_inventory_experiment`。
本轮首审提示另明确区分“功能存在”和“入口未知”；修改后的首审搭配原复核未在本轮接受真实模型验证。
复核独立接收原始全文，不接收首审的类型或结论；返回业务声明 checks（含原句、声明、
证据关系、原文证据），以及非业务内容 non_facts（原句、理由）。不再仅返回新增问题列表。
程序将 not_stated/contradicted 转为否决发现，空证据的 entailed/contradicted 为错误；
复核原句覆盖不足则保留为 uncertain，不可直接放行。全部片段均被误归非业务仍可能漏判，
因此清单形式本身不是语义正确性证明。
`inventory_review_raw` 保留实际复核清单；兼容字段 review 是程序派生结果，不是模型原始清单。
非法字段仍报错，不作静默删除，未放宽首审或复核覆盖门槛。

[清单复核三轮验证](../examples/knowledge/webchat/grounding-inventory-review-v1.md)未通过：
固定六题重复三轮，18 次执行中 11 次符合预期，2 次误报、1 次不确定、4 次错误。
设备负例三次检出均来自首审，对应有依据正例三次均未通过。候选保持隔离，本轮未追加完整二十题回归。

## 固定分类提示的对照诊断

[本轮五题配对结果](../examples/knowledge/webchat/grounding-hint-comparison-v1.md)：
提供提示组零题符合预期，不提供组两题符合；其余三题两组均未解决。
单次、失败样例对照不足以作因果判断，不代表已修复或可上线。

`-replay-hints` 只回放归档分类，不再调用分类模型；它与 `-with-classification`、
`-classify-only` 互斥。`-omit-replayed-hints` 必须与回放文件同时使用。

```sh
go run ./cmd/grounding-check \
  -cases examples/knowledge/webchat/grounding_hint_comparison_cases.json \
  -replay-hints examples/knowledge/webchat/grounding_hint_comparison_hints.json
```

默认仍为离线校验。显式 `-allow-model-api` 才请求模型，每题每组最多两次：首审及条件复核。
以上为提供提示组；附加 `-omit-replayed-hints` 是不提供提示组。
两组使用完全相同的 assistedPrompt 和复核提示词，仅首审 user JSON 中
`classification_hint` 是否存在不同；复核始终不接收提示。
因此无提示组不是旧 v5 提示词的基准测试，不能混为一谈。

回放文件必须精确匹配每条原始 reference/history/answer；拒绝缺失、重复、
无效分类、覆盖不完整、未知字段或样例数据变化，校验失败不会构造模型请求。
诊断报告 schema 7，记录回放文件哈希及 `hint_delivery=recorded|omitted`。
两组结果里的 `classification` 都是归档诊断材料；只有 recorded 组把它传入首审。
样例 ID、标签、上一轮判定不发送给模型。没有新增业务判定规则或自动修复输出。

## 默认关闭的分类辅助组合模式

[完整冻结验收](../examples/knowledge/webchat/grounding-assisted-v1.md)：二十题十五题符合预期，
另有一题漏判、两题误判、两题结构错误。组合实现完成不等于模型可靠性通过。

`-with-classification` 开启独立旁路实验，不能与 `-classify-only` 同用。
默认行为保持原来的 v5 两阶段审查；正式客服不会使用这个开关。

```sh
go run ./cmd/grounding-check -with-classification \
  -cases examples/knowledge/webchat/grounding_judge_cases.json
```

默认仅本地校验。显式加 `-allow-model-api` 才运行以下阶段：

1. 分类原始全文，校验分类结构及全文覆盖；失败停止批次，不重试、不放行。
2. 首审读取原始 reference/history/answer 和不可信 `classification_hint`。
   分类不能作为证据，不能删减原文，不能让任何非 assertion 标签自动免审。
3. 首审格式有效且未判 unsupported 时，复核再次读取原始全文；
   不接收分类提示、首审判断或理由，保留独立否决能力。

每题最多三次模型请求；十二题最多三十六次，八题最多二十四次。
仍保留单次 75 秒超时、整体至多 15 分钟、无重试、无重定向、错误脱敏。
这不改变旧模式每题最多两次、纯分类模式每题一次的预算。

报告 schema 6，模式为 `classification_assisted_judge_experiment`，记录完整的
`classification_prompt`、首审 prompt 和复核 prompt；每题 `classification` 记录通过
结构校验的分类结果，即使后续审查失败也保留，不能当作事实审查成功。
分类错误的 `failure.phase=classification`，解析环节为 `classification_parse`。
仍使用旧的错误/漏判/误判/不确定分类统计，不因为增加了分类步骤而放宽成功标准。

分类和两个审查阶段使用同一模型，仍可能存在共同盲点。这是实验，不是可靠性保证。

## 独立分类模式

新增独立的言语行为分类实验，入口 `-classify-only`。它不判断答案是否有依据，
不替代下面的 v5 事实审查，也没有接入正式客服。
用于先定位“否定/未知说明被当事实”和“操作步骤被归为通用建议”的分类错误。
本轮十二题单次分类均匹配，范围和限制见[分类验收报告](../examples/knowledge/webchat/speech-classification-v1.md)。
后续[长回答验收](../examples/knowledge/webchat/speech-long-validation-v1.md)保持相同分类规则：
九题严格匹配七题，两题差异仅在礼貌语归属，零结构错误；没有连接事实审查或线上回复。
对应语料为 `examples/knowledge/webchat/speech_long_holdout_cases.json`，可替换下方 `-cases` 路径验证。

```sh
go run ./cmd/grounding-check -classify-only \
  -cases examples/knowledge/webchat/speech_classification_cases.json
```

默认仅检查本地样例。加 `-allow-model-api` 才调用模型，每题一次、整批最多十二次，
沿用既有 HTTPS 配置、超时、禁止重定向、无重试和脱敏错误诊断。
该模式独立使用 schema 1，报告模式为 `speech_classification_experiment`，
结果只有 `matched` / `mismatched` / `error`，没有事实审查的 `clear`。

分类契约：

- `assertion`：业务事实或能力的肯定/否定、具体操作指导；有依据的步骤仍在此类。
- `uncertainty`：无法确认能力是否存在，不是声称系统没有该能力。
- `self_limitation`：当前助手不能代办或没有工具，不代表业务系统不支持。
- `correction`：撤回先前无依据说法；同时新增的系统断言必须另拆。
- `information_seeking`：建议查询官方资料或咨询以确认规则，不承诺可办理。
- `social_safety`：礼貌、安全提醒、不含独立业务事实的过渡语。

输出每段只能包含 `quote`、`act`、`reason`；未知字段、非法类别、非原文引用、覆盖不完整、
同一文字类别冲突均为格式错误。段落划分可以不同，评分按全部文字/数字的位置比较类别，
忽略标点与排版；任一文字分错，整题为 `mismatched`。同样文字重复出现且需要不同分类时，
当前引用匹配方式会拒绝冲突，不适合直接扩展为任意长对话的通用对齐器。
标签及标注理由不发送给模型。此约束是本地严格校验，不是供应商 JSON Schema 强制生成，
也不能证明模型的语义判断稳定；分类正确更不等于事实有依据。

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
