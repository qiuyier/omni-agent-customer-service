# omni-agent-customer-service

面向企业系统的渠道无关智能客服服务，当前使用 Go 和 Eino 构建；模型层可在
OpenAI、DeepSeek、GLM、Kimi、Qwen、OpenRouter、硅基流动及其他
OpenAI-compatible 端点之间切换。后续阶段计划接入 Hertz 和 omni-agent-kit。

## 当前进度

第三阶段已经实现“非流式 AI 自动回复闭环”：

- 接收并幂等保存客户消息；
- 根据完整会话历史生成机器人回复；
- 使用 Eino `AgenticModel` 对接 Responses 与 OpenAI-compatible Chat
  Completions；
- 将生成结果与触发它的客户消息关联并原子保存；
- 防止重复回复、并发回复和过期回复写入；
- 用 fake model 完成不联网、不消耗额度的自动化测试；
- 提供 `cmd/console` 端到端运行入口。

使用 OpenAI 时，真实调用若返回 `billing_not_active`，需要在
[OpenAI API Billing](https://platform.openai.com/settings/organization/billing)
激活 API 计费；ChatGPT 订阅与 API 计费相互独立。

## 分层结构

~~~text
cmd/console                         组合入口：读取环境配置、组装依赖
       │
       ▼
internal/ai/eino                   Eino / 模型供应商出站适配器
       │ 实现 ReplyGenerator
       ▼
internal/customerservice           应用用例与流程编排
       │ 依赖 Repository 接口
       ▼
internal/conversation              领域模型、仓储端口与内存实现
~~~

业务层只认识 `ReplyGenerator`：

~~~go
type ReplyGenerator interface {
    Generate(context.Context, ReplyRequest) (Reply, error)
}
~~~

因此模型供应商、本地模型和企业 AI 网关都可以替换，领域模型不依赖 Eino
类型，自动化测试也不需要 API Key。

## 自动回复流程

模型调用位于两个短事务之间：

~~~text
事务一
  → 查找已有回复
  → 检查 bot 是否拥有回复权
  → 验证 trigger 是最新客户消息
  → 读取有序历史并记录 Revision / 消息水位

事务外
  → 调用 ReplyGenerator
  → 拒绝模型错误或空白回复

事务二
  → 再次查找已有回复
  → 复查 Revision、回复权和消息水位
  → 保存带 ReplyToMessageID 的 bot 消息
~~~

模型调用不能放进事务：一次推理可能持续数秒，长时间持锁会阻塞收消息和
转人工。调用前后重复校验，则能在不持有长事务的同时保证结果没有过期。

### 并发保护

- `Conversation.Revision` 在每次状态迁移时递增，可以识别
  `bot_active → waiting_human → bot_active` 这种 ABA 变化；
- 消息水位可以识别模型生成期间新增的客户、系统或工具上下文；
- Repository 对 `ReplyToMessageID` 建立逻辑唯一索引，同一 trigger 最多
  保存一条 bot 回复；
- 两个并发模型调用可能都会发生，但只有一个结果能入库，其他调用返回同一
  条获胜回复；
- 持久化 Repository 的事务内 `FindConversation` 必须锁定会话行，SQL
  实现应使用等价于 `SELECT ... FOR UPDATE` 的机制；数据库仍需为
  `reply_to_message_id` 建立唯一约束。

## 模型供应商与协议适配

项目没有为每个平台分别实现一套客服适配器，而是把配置拆成两个维度：

- `Provider`：厂商预设，只提供默认协议、Base URL 和密钥变量名；
- `Protocol`：实际网络协议，目前支持 `responses` 和
  `chat_completions`。

~~~text
ProviderConfig
    │
    ├── responses ────────────→ Eino ResponsesModel ──┐
    │                                                  │
    └── chat_completions ─────→ Eino ChatModel ───────┤
                                                       ▼
                                               AgenticGenerator
                                                       │
                                                       ▼
                                      customerservice.ReplyGenerator
~~~

两条协议最终都变成同一个 `ReplyGenerator`，所以切换厂商不会修改客服业务层、
会话仓储或回复幂等逻辑。

| `AI_PROVIDER` | 默认协议 | 默认 Base URL | 密钥回退变量 |
| --- | --- | --- | --- |
| `openai` | `responses` | `https://api.openai.com/v1` | `OPENAI_API_KEY` |
| `deepseek` | `chat_completions` | `https://api.deepseek.com` | `DEEPSEEK_API_KEY` |
| `glm` | `chat_completions` | `https://open.bigmodel.cn/api/paas/v4` | `ZHIPUAI_API_KEY` |
| `kimi` | `chat_completions` | `https://api.moonshot.cn/v1` | `MOONSHOT_API_KEY` |
| `qwen` | `chat_completions` | `https://dashscope.aliyuncs.com/compatible-mode/v1` | `DASHSCOPE_API_KEY` |
| `openrouter` | `chat_completions` | `https://openrouter.ai/api/v1` | `OPENROUTER_API_KEY` |
| `siliconflow` | `chat_completions` | `https://api.siliconflow.cn/v1` | `SILICONFLOW_API_KEY` |
| `custom` | `chat_completions` | 必须配置 | 无，可使用无鉴权端点 |

DeepSeek、GLM、Kimi、Qwen、OpenRouter 和硅基流动共用一套 OpenAI Chat
Completions 兼容实现；增加另一个兼容平台通常只需要使用 `custom` 或增加一条
预设，不需要创建新的业务 Adapter。

OpenAI 默认继续使用 Responses API，并设置 `store=false`。Chat Completions
仍受 OpenAI 支持，但 OpenAI 建议新项目优先使用 Responses。不能把“兼容
Chat Completions”理解成“也兼容 Responses”；只有确认目标端点和模型支持时，
才应显式设置 `AI_PROTOCOL=responses`。

非 OpenAI 预设不内置默认模型，必须通过 `AI_MODEL` 指定当前账号、区域和
端点实际可用的模型。模型 ID 的上下架比协议和 Base URL 更频繁，因此不把
短期有效的模型名写死在代码中。Qwen 的生产环境如果使用专属工作空间，也应
通过 `AI_BASE_URL` 覆盖默认共享端点。

配置仅在组合入口读取，API Key 不进入领域对象、消息记录或日志。程序代码还可
通过 `ProviderConfig.Headers` 和 `ProviderConfig.ExtraFields` 添加可信端点需要的
扩展；当前 Eino 版本只对 Chat Completions 开放 `ExtraFields`，并禁止覆盖
`model`、`messages` 等核心字段。

协议与端点可参考各平台官方文档：
[OpenAI](https://developers.openai.com/api/docs/guides/migrate-to-responses)、
[DeepSeek](https://api-docs.deepseek.com/zh-cn/)、
[GLM](https://docs.bigmodel.cn/cn/guide/platform/model-migration)、
[Kimi](https://platform.kimi.com/docs/api/overview)、
[Qwen](https://help.aliyun.com/zh/model-studio/qwen-api-via-openai-chat-completions)、
[OpenRouter](https://openrouter.ai/docs/quickstart)、
[硅基流动](https://docs.siliconflow.cn/cn/userguide/quickstart)。

## 本地运行

`.env.example` 只描述变量名；真实密钥保存在被 Git 忽略的 `.env.local`。
推荐统一使用以下五个配置：

~~~dotenv
AI_PROVIDER=deepseek
AI_PROTOCOL=
AI_API_KEY=
AI_MODEL=
AI_BASE_URL=
~~~

- `AI_PROVIDER` 默认是 `openai`；
- `AI_PROTOCOL` 留空时使用上表中的厂商默认协议；
- `AI_API_KEY` 是推荐的统一密钥变量；
- `AI_MODEL` 对 OpenAI 可留空使用项目默认值，其他厂商必填；
- `AI_BASE_URL` 通常留空，使用 `custom` 时必填。

为了兼容已有本地配置，OpenAI 仍支持 `OPENAI_API_KEY`、`OPENAI_MODEL` 和
`OPENAI_BASE_URL`。统一的 `AI_*` 值优先。其他厂商也可用表中的专属密钥变量
代替 `AI_API_KEY`。

~~~bash
set -a
source .env.local
set +a

go run ./cmd/console -message "我的订单还没到，应该怎么办？"
~~~

也可以用命令行覆盖非密钥配置：

~~~bash
go run ./cmd/console \
  -provider custom \
  -model local-model \
  -base-url http://127.0.0.1:11434/v1 \
  -message "如何修改收货地址？"
~~~

OpenRouter 可选配置 `OPENROUTER_SITE_URL` 和 `OPENROUTER_APP_NAME`，分别转换
为 `HTTP-Referer` 与 `X-OpenRouter-Title` 归因请求头，两者都不是认证信息。

`cmd/console` 每次使用新的内存仓储，只用于验证单轮端到端链路，不是生产
服务入口。

## 本地验证

以下命令均不调用任何真实模型服务：

~~~bash
go test ./...
go test -race ./...
go vet ./...
~~~

测试覆盖领域状态、ReplyTo 约束、事务回滚、模型输入映射、两种 HTTP 协议、
模型失败、空白回复、幂等重试、并发生成、生成期间转人工、ABA 和新增消息等
情况。协议测试使用本地 `httptest` 服务，不需要真实 API Key，也不产生费用。

## 下一阶段

第四阶段将使用 Hertz 暴露 HTTP API，并通过 SSE 返回流式回复。届时会使用
omni-agent-kit 的流适配能力处理 Eino stream、客户端断开和 `Close()` 生命周期，
而不会把流式传输细节放进当前的非流式业务用例。
