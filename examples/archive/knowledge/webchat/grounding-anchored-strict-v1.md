# 原文编号与严格工具输出实验：未通过真实验收

日期：2026-09-26。实验保持默认关闭，不接入正式客服，不修改 CRMEB。

## 官方能力核查

DeepSeek 的普通 response_format 仅列 text/json_object；严格 Schema 在 Beta 工具调用中，
要求 https://api.deepseek.com/beta、function.strict=true、对象字段全部 required 且 additionalProperties=false，
支持 anyOf。指定必须调用某个工具时，需要关闭思考模式。
来源：[工具调用指南](https://api-docs.deepseek.com/guides/tool_calls)、
[对话接口参数](https://api-docs.deepseek.com/api/create-chat-completion)。

搜索技能调用因证书校验失败未取得结果，后续直接读取官方页面，未关闭 TLS 校验。
文档描述不代替实测：本次实际参数中仍出现不符合所提交 Schema 的内容，不能声称严格约束已经可靠生效。

## 实现

新增独立开关 `-anchored-primary`（schema 11），不替换原 typed/split/inventory 模式。
程序按标点无损切分原回答，提供完整 answer/history/reference 和 answer_anchors 地址表；
模型返回 start_id/end_id 连续范围、scope/reason，只有 system_fact 可以含 assessment。
本地用编号重建 quote，再复用原 typed 的事实、证据、引用与覆盖校验。
漏掉任何非空白地址、范围越界、倒序或不合法字段均拒绝；不会自动补编号或删字段。
重复文字的不同位置也必须分别覆盖，不能用一次引用代替另一位置。

编号仅为地址，不是语义标签。混合范围可以按不同声明重复审查；仍要求识别其中所有事实。
编号不能保证分类正确。原独立复核继续读完整原文，不接受首审分类提示。

首审使用 DeepSeek Beta、strict 工具 submit_review、thinking=disabled、指定工具调用；
只读取返回参数，不执行任何工具、不发送工具执行结果，也不增加第三次模型调用。
复核保留普通接口和原提示，thinking 参数省略。
字段、空证据、引用出处、范围顺序和覆盖均需本地校验，即使服务端接受 Schema。

## 初版冻结验证

相同六种输入两轮，计划最多 24 次请求，实际 16 次，12 次执行全部完成，6 次符合预期、6 次错误。
没有最终漏放、误报或 uncertain，但不能把错误计为正确放行。
四次负例最终全部检出；其中两次能力短片段首审仍漏掉事实，是独立复核纠正了它。
八次正例仅两次通过，六次错误，尚不具备可用性。

| 输入 | 第一轮 | 第二轮 |
| --- | --- | --- |
| 完整负例 | unsupported | unsupported |
| 完整正例 | error | error |
| 能力短片段负例 | unsupported（复核否决） | unsupported（复核否决） |
| 能力短片段正例 | error | error |
| 助手限制片段 | clear | clear |
| 有依据步骤片段 | error | error |

首审同时改变了地址表示、工具输出、Beta 端点及思考设置，不能把与上轮 10/12 的差异单独归因于某一改动。
同一案例家族、小样本、非同期比较，也不能推断模型整体能力。
采样期间没有修改代码或提示，结束后源码哈希一致；之后进行了下面单独标明的本地修正。

## 原始失败与严格约束异常

归档目录：eval_runs/2026-09-26/anchored-strict-v1。

- `1.json`、`5.json`、`11.json`：初版编号器把纯换行变成独立地址，模型将空白单独标为 social。
  重建后触发原有非空 quote 校验，invalid_general_advice。这是本地设计缺陷，不应归咎为模型不懂业务。
- `3.json`：漏掉尾部建议片段，missing_answer_anchor；同时非事实项出现 assessment:"SKIP"，
  也违反提交的 anyOf 字段契约。首先命中的错误不代表没有其他问题。
- `7.json`：遗漏了引出重置步骤的非空过渡文字，missing_answer_anchor，不只是遗漏空白。
- `9.json`：额外字段 reason_note 导致 invalid_anchored_primary。虽然请求设置 strict=true，
  返回参数仍不满足所声明 Schema；本轮不能确认服务端严格约束的可靠性，内部原因尚未确定。
- `2.json`、`8.json`：首审把“通过账号安全功能查看”标为 knowledge_limit，原复核发现缺失功能依据。
  两题最终通过不意味着首审已经修好语义边界。

没有删掉 SKIP/reason_note、补齐遗漏内容或重新判分。原始工具参数保存在 anchored_primary_raw。

## 本地修正与版本边界

真实验证结束后，修正了编号器：纯空白附着于相邻文本地址，不删除或归一化任何原文字节。
开头空白保留在首个文本地址中，后续空白保留在前一个文本地址中。
新增回归覆盖连续空行、CRLF、Unicode、重复文字、无标点混合句和精确重建。
报告新增 anchor_version=lossless-punctuation-v2 及逐案例 answer_anchors 表，便于核对编号对应原文。

**上面的 6/12 属于修正前冻结初版，不属于当前 v2 编号器。当前 v2 只完成离线验证，未再调用模型。**
旧归档的编号不可套用新编号器重算成绩。覆盖、证据与非法字段规则未放宽。
未继续付费测试的原因：已观察到严格参数仍违规及语义漏判，先保留诊断证据，不把修空白等同于整个方案通过。

## 工程验证

初版全项目测试、grounding-check race 与 vet 通过；空白修正后再次通过 grounding-check 全部测试、race、vet 和 git diff --check。
测试验证无损重建、非法/遗漏编号、空证据拒绝、未知字段拒绝、独立复核、单次工具返回约束、错误不重试及模式互斥。
这些测试不等于模型验收，也不等于真实数据库集成验收。

## 后续最小步骤

不要把当前候选上线，也不要继续增加审核层。先用极简 Schema 单独确认 Beta 严格模式是否能在当前模型上遵守约束，
将 anyOf 等契约结构因素分开验证；不执行业务工具，不同时修改语义提示。
严格能力确认后，才值得对修正版编号器做固定正负案例重复验证。

材料：[冻结计划](eval_runs/2026-09-26/anchored-strict-v1/freeze.json)、
[逐次结果](eval_runs/2026-09-26/anchored-strict-v1/summary.json)、
[归档运行脚本](eval_runs/2026-09-26/anchored-strict-v1/runner.cjs)。
脚本是初版冻结运行记录，当前源码已修正；再次实验必须使用新目录和新冻结记录。
