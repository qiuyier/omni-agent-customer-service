# DeepSeek 原首审对照 v1

日期：2026-09-26。结论：本批 10/12 符合预期，仍有两次输出校验错误，未达到稳定通过。
没有证据支持仅更换模型就能解决当前问题；不切换正式客服模型。

## 冻结范围

使用 deepseek-flash，https://api.deepseek.com，原 typed 首审及 specificity 独立复核。
从上一轮受控诊断直接复用六种输入和预期标签，各运行两轮，共十二次。
调用前核对 grounding-check 全部 Go 源码和两阶段提示哈希与此前 GLM 版本一致；
结束后再次核对源码，全部一致。没有改提示、修复字段、放宽证据、重试或追加案例。

仅读取本地 DEEPSEEK_API_KEY，子进程环境独立构建，不继承原 AI_API_KEY 或 GLM 配置。
不修改 .env.local、生产链或 CRMEB。最大预算 24 次请求，实际 20 次；另一次此前连通性测试不计入本轮。
未修改的检查器不采集 token usage，因此本轮不能报告准确 token 数、金额或成本优势。

比较限制：此前 GLM 使用 reasoning_effort=low；本轮 DeepSeek 不发送 reasoning_effort 或 thinking，
使用供应商默认行为，默认实际推理模式未独立确认。这是不同模型配置的实际表现比较，
不是等算力、同期随机试验；两轮、单一设备案例家族也不是跨行业准确率。

## 结果

| 模型配置 | 符合预期 | 输出错误 | 最终误报 | 最终漏放 | 请求数 |
| --- | --- | --- | --- | --- | --- |
| DeepSeek 本轮 | 10/12 | 2 | 0 | 0 | 20 |
| GLM 此前原首审 | 11/12 | 1 | 0 | 0 | 19 |

DeepSeek 四次负例执行中两次正确检出、两次错误，八次正例全部正确放行。
“零漏放”不等于所有负例成功检出，错误不计通过。

| 输入 | DeepSeek 第一轮/第二轮 | GLM 第一轮/第二轮 |
| --- | --- | --- |
| 完整负例 | unsupported / error | unsupported / unsupported |
| 完整正例 | clear / clear | clear / clear |
| 能力短片段负例 | error / unsupported | unsupported / unsupported |
| 能力短片段正例 | clear / clear | clear / error |
| 助手限制片段 | clear / clear | clear / clear |
| 有依据步骤片段 | clear / clear | clear / clear |

## 两个错误的原始定位

归档：eval_runs/2026-09-26/deepseek-typed-v1。

- `2.json`：第一轮能力短片段负例。knowledge_limit 多出 `reason_extra` 字段，
  首审严格解码报 invalid_typed_primary。这是合法 JSON，但不是合法字段契约。
- `6.json`：第二轮完整负例。quote 包含原回答不存在的换行，例如 `您好！\n`，
  原回答这里实际直接接“很抱歉”。非事实原句检查报 invalid_general_advice。
  这属于原文摘录不一致，不是 JSON 语法错误，也不是资料证据被证明正确。

两个原始输出都将查询能力声明列为 system_fact/unsupported、关系 not_stated，
说明本例中语义判断已出现于原文，但输出不合法，整个执行仍为 error；没有删字段或调整空白后重算成绩。
这两个错误发生于 primary / typed_primary_parse，均未进入复核。

## 判定与下一步边界

本批没有呈现稳定性改善，不依据 10/12 与 11/12 的小差异断言哪个模型整体更强。
已能明确区分本轮失败点：额外字段，以及原文摘录被改写。不得继续笼统归因为模型不懂业务。
本轮诊断结束，不追加完整二十题，不切换正式客服；原首审仍只是实验基线。
若继续，应先评估能否用明确支持的结构约束和可验证引用方式避免这些机械输出错误，
同时保留独立的语义验收；不能把客户端宽松修补当作模型修复。

## 验证与材料

调用前 `go test ./cmd/grounding-check` 通过，采样后源码哈希一致；本轮只新增报告与归档。

- [冻结计划](eval_runs/2026-09-26/deepseek-typed-v1/freeze.json)
- [逐次结果](eval_runs/2026-09-26/deepseek-typed-v1/summary.json)
- [比较与失败原文](eval_runs/2026-09-26/deepseek-typed-v1/audit.json)
- [归档运行脚本](eval_runs/2026-09-26/deepseek-typed-v1/runner.cjs)

归档脚本默认离线；显式 --allow-model-api 才联网。为防止重复付费，已有目标目录会拒绝再次执行。
