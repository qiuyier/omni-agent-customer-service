# DeepSeek strict 极简开关对照：当前配置未观察到强制约束

日期：2026-09-27。结论：在本批配置和冲突输入下，strict=true 的六次输出全部不满足所提交 Schema。
不能将当前 strict 工具输出作为字段安全保证。没有修改正式客服、CRMEB、模型配置或校验规则。

## 要回答的问题

上一轮严格工具输出出现非法字段。此次不审查客服业务，也不修改提示词去提高语义成绩，
仅判断服务端是否强制遵守字段契约，以及异常是否只发生于 anyOf 复杂结构。

三种契约分别开启/关闭 strict，各两轮，共十二次，不重试。第二轮反转开关先后顺序。
所有请求使用 deepseek-flash、https://api.deepseek.com/beta/chat/completions、thinking=disabled、
指定 submit_probe 工具、stream=false、max_tokens=512。工具只提交诊断数据，不执行动作。
对象字段均 required，additionalProperties=false；原始请求体不含密钥，保存在冻结文件。

系统消息故意要求逐字复制与 Schema 冲突的测试载荷，以区分“模型按提示返回正确格式”和“接口强制格式”。
这是约束压力测试，不能用其失败率估计正常业务错误率；若存在所声明的硬约束，冲突文本也不应生成违规参数。

## 契约与结果

| 契约 | 故意冲突内容 | strict=false | strict=true |
| --- | --- | --- | --- |
| 单字段枚举 | status=forbidden，多出 extra | 2/2 违规 | 2/2 违规 |
| 必填字段、整数范围 | 缺 label、count=9（允许 1–3）、多出 extra | 2/2 违规 | 2/2 违规 |
| 数组内 anyOf | other 分支中夹带 evidence 与 reason_note | 2/2 违规 | 2/2 违规 |

十二次均 HTTP 200、finish_reason=tool_calls、返回模型名 deepseek-flash，且只有一个指定工具调用。
所有参数是合法 JSON，但没有一个满足相应 Schema。不存在将 HTTP 成功误计为契约通过的情况。
实际十二次请求，共 5,305 tokens（接口报告的总 token 累加），不据此推断货币费用。

最小反例见 `1.json`：工具 Schema 要求唯一字段 status，枚举只允许 ok，禁止额外字段；
strict=true 的实际参数却为 `{"status":"forbidden","extra":true}`。
因此不能只归因于数组嵌套、anyOf 或客服语义难度。

## 本地验证

运行前使用独立的精简校验器检查每种 Schema 的合法样例与非法样例，覆盖额外字段、缺字段、
枚举、整数类型与范围、数组和 anyOf 分支。校验器遇到未实现的 Schema 关键字会拒绝，
只用于本次明确列出的 Schema 子集，不宣称是完整 JSON Schema 实现。
没有修改、删除或补齐模型参数后重新计分；冻结计划在调用前写入，结果按每次请求分别保存。

## 决策与限制

- 暂停依赖此配置 strict 能力的客服回归；原文编号 v2 仍只有离线验证，不新增通过声明。
- 保留本地严格字段、原文覆盖、证据检查，不能因为 HTTP 200 就绕过它们。
- 本结果是当前账户、模型、端点、参数和测试输入下的反例，不足以断言所有 DeepSeek 模型或所有接口均不支持 strict。
- 尚未确定内部原因，也未确定是能力未生效、路由/兼容行为还是实现问题；不自行猜测结论。
- 下一步若追查，应先用本报告的最小请求向供应商确认支持条件，或经用户授权测试另一明确支持硬约束的配置。
  未向供应商发送任何消息，也未更换模型或扩大测试预算。

原先普通 JSON 模式不等于严格约束的判断保持不变。此处是实测结果，不依赖对官方保证的推断。

## 可复现材料

- [冻结请求体及顺序](eval_runs/2026-09-27/deepseek-strict-probe-v1/freeze.json)
- [全部结果与分组统计](eval_runs/2026-09-27/deepseek-strict-probe-v1/summary.json)
- [最小 strict 反例](eval_runs/2026-09-27/deepseek-strict-probe-v1/1.json)
- [运行脚本及离线校验器](eval_runs/2026-09-27/deepseek-strict-probe-v1/runner.cjs)

脚本默认只运行离线自测；--allow-model-api 才读取本地 DEEPSEEK_API_KEY 并联网。
为防重复付费，已有输出目录时拒绝再次运行；新实验应另建冻结记录，不覆盖本轮结果。
