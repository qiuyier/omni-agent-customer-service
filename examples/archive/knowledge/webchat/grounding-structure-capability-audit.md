# 结构输出能力核查

核查日期：2026-09-24。本轮是能力核查与历史输出离线回放，不是模型修复验收。

## 结论

当前实验传输没有发送 response_format，字段契约依赖提示和本地严格校验。
智谱官方 OpenAPI 的 ChatCompletionTextRequest.response_format.type 枚举仅列出
text、json_object，未列出 json_schema 或 strict 参数。结构化输出指南把 Schema 放入提示，
并在客户端调用 jsonschema.validate；这不是服务端严格 Schema 约束的证据。

来源：[官方 OpenAPI](https://docs.bigmodel.cn/openapi/openapi.json)、
[结构化输出指南](https://docs.bigmodel.cn/cn/guide/capabilities/struct-output)、
[当前模型页面](https://docs.bigmodel.cn/cn/guide/models/vlm/glm-5.3-flash)。
文档接口说明只称文本模型支持 response_format，当前模型页面定位为多模态模型；
因此不能仅凭通用指南断言当前 Flash 模型及实际端点已支持 JSON 模式。
本轮未发送模型能力探测请求，也未证实运行端点拒绝严格 Schema。

## 四次错误重新分类

原始归档：eval_runs/2026-09-24/inventory-v1-repeated。

| 报告 | 阶段 | 拒绝原因 | 层次 |
| --- | --- | --- | --- |
| 3.json | 首审 | 非事实携带 assessment | 条件字段契约 |
| 8.json | 首审 | 非事实携带 assessment | 条件字段契约 |
| 13.json | 复核 | 非事实对象含额外 claim 字段 | 字段契约 |
| 17.json | 复核 | entailed 的证据为空 | 证据约束 |

四份实际原始输出均为合法 JSON。新增 TestArchivedInventoryContracts 不修改归档，
逐项断言 JSON 合法且仍被相应校验拒绝，并回放 9.json 的首审与复核作为合法结构对照。
这些测试保护校验边界，不代表模型能稳定生成合法结果，也不验证分类语义正确。

## 决策

不把 json_object 当成这四次失败的修复，不引入未有官方依据的 strict/json_schema 参数，
不静默删字段、不将空证据降级为通过、不增加审核调用。
本轮没有改提示、生产链、CRMEB 或模型传输，没有付费模型调用。

若保持当前模型，下一项候选应是降低输出契约的条件分支复杂度，并以原校验等价转换验证，
仍需固定正负配对重复验收；这只能是减少生成错误的实验，不能承诺严格生成。
若要求服务端强约束，需先选择明确支持该契约的模型/端点并验证能力，不能自动更换供应商。
原有语义误判和结构稳定性均未解决。
