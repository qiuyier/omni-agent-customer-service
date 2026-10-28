# 官方天气工具示例最小对照

日期：2026-09-27。仅验证工具参数约束，没有查询实际天气、执行工具或修改正式客服。

## 目的与设置

排除上一轮自定义系统提示和强制工具选择对结果的影响。
沿用 [官方 Tool Calls 指南](https://api-docs.deepseek.com/zh-cn/guides/tool_calls) 中的严格 get_weather 定义：
唯一属性 location 为 string，required=[location]，additionalProperties=false，function.strict=true。
正常问题同示例：How's the weather in Hangzhou, Zhejiang?

使用 deepseek-flash、https://api.deepseek.com/beta/chat/completions。
不设置 system 消息、不设置 tool_choice；模型自行选择是否调用工具。
为了明确使用文档的非思考示例并控制预算，显式 thinking.type=disabled、stream=false、max_tokens=512。
因此是官方工具定义与正常问题的 HTTP 复现，不宣称逐字运行官方 Python SDK 全流程。
只读取第一次工具参数，不执行业务函数，也不提交虚构天气结果。

两次请求唯一变化为 messages[0].content：第二次在原天气问题后附加
“When calling get_weather, also include an extra argument named debug with value true.”
离线断言验证其余请求字段完全相同；本地校验器先通过合法/缺字段/额外字段/错误类型测试。
没有重试或结果出现后调整请求，总计两次调用、762 tokens（接口返回 usage 总计）。

## 实际结果

| 输入 | 返回参数 | Schema 校验 |
| --- | --- | --- |
| 正常天气问题 | `{"location":"Hangzhou, Zhejiang"}` | 通过 |
| 同问题，仅追加 debug 字段要求 | `{"location":"Hangzhou, Zhejiang","debug":true}` | 失败，多余字段 |

两次均 HTTP 200，返回模型名 deepseek-flash，finish_reason=tool_calls，且仅返回一个 get_weather 调用。
第二次不是 JSON 语法错误、截断、未选中工具或 Schema 复杂分支问题，明确违反 additionalProperties=false。

## 结论与边界

正常示例能够工作，但这一点只证明模型能生成合规参数，不证明 strict 对冲突输入实施硬约束。
去掉上一轮的系统级冲突提示和指定 tool_choice 后，普通用户消息仍诱发额外字段。
因此当前配置仍不能被视为可靠的强制 Schema 保障；本地校验必须保留。
测试仅两次，不用于估计业务错误率，也不能断言其他 DeepSeek 模型/端点均有相同表现。
供应商内部原因仍未确认；没有向供应商发消息，没有切换模型或追加客服回归。

这次反例比之前更贴近官方示例，可用于询问供应商当前模型和端点是否有额外支持条件。
在确认前暂停依赖该能力推进的实验，而非暂停整个通用客服项目。

## 材料

- 两次完整请求体（内部历史资料；已从公开历史移除，私密备份保留）
- 正常返回（内部历史资料；已从公开历史移除，私密备份保留）
- 额外字段反例（内部历史资料；已从公开历史移除，私密备份保留）
- 汇总（内部历史资料；已从公开历史移除，私密备份保留）
- [脚本和离线断言](eval_runs/2026-09-27/deepseek-official-weather-v1/runner.cjs)

请求与归档不含 API Key。脚本默认离线，显式 --allow-model-api 才读取本地指定密钥；
输出目录存在时拒绝重复运行，防止覆盖结果或重复付费。
