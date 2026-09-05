# omni-agent-customer-service

面向企业系统的渠道无关智能客服服务，使用 Go、Eino、Hertz 和 Thrift 构建；
模型层可在 OpenAI、DeepSeek、GLM、Kimi、Qwen、OpenRouter、硅基流动及
其他 OpenAI-compatible 端点之间切换。

## 当前进度

第六阶段第五小步已经加入可控的 HTTP 优雅停机：

- 新增匿名 `/readyz`：正常服务时返回 200，进入排空后，仍能到达 Handler 的
  探针返回 `503 unavailable`；`/healthz` 的 Handler 保持返回 200；
- `/v1` 的排空闸门位于鉴权和限流之前，停机后到达的新请求不消耗令牌，也不会
  进入 Handler、数据库或模型；已经越过闸门的普通请求和 SSE 可以继续完成；
- `cmd/server` 不再使用无返回值的 `Spin()`，而是显式监听 `SIGINT`、`SIGTERM`，
  先进入 draining，再调用 Hertz `Shutdown`；
- `SERVER_SHUTDOWN_TIMEOUT` 默认 `90s`，同时控制外层停机预算与 Hertz 内部等待，
  避免 Hertz 默认的 5 秒上限提前结束排空；
- 所有请求继承独立的进程级根 Context。正常停机不会取消它；排空超时或失败时
  才强制取消，使 Eino、SSE 和数据库调用收到取消信号；
- 第一次终止信号开始排空并恢复系统默认信号行为；第二次 `Ctrl+C`/终止信号可由
  操作系统立即结束进程。强制取消后，Close 和 Run 收尾共用额外 1 秒等待预算；
- Zap 会记录 `server.shutdown.started`、`server.shutdown.completed`、
  `server.shutdown.timeout` 或 `server.shutdown.failed`，不记录敏感请求数据。

第六阶段第四小步已经加入基于 Uber Zap 的结构化可观测性：

- `cmd/server` 创建一套共享 Zap Logger，Hertz `hlog`、HTTP 访问日志和 AI 回复
  日志使用相同的级别与格式；
- 每个 HTTP 请求只记录一条 `http.request.completed`，包括 Request ID、方法、
  路由模板、状态码、耗时和安全错误码；
- `customerservice.Service` 只依赖与传输、日志框架无关的 `ReplyObserver`，Zap
  实现位于 `internal/observability`；
- AI 回复记录 started/rejected/finished 生命周期、并发容量、模型耗时、总耗时和
  终止阶段，不为每个流式 delta 单独写日志；
- SSE 访问日志覆盖到 EOF、失败或客户端断开，所有终止路径只完成一次；
- 日志事件不携带客户消息、模型回复、System Prompt、API Key、Bearer Token 或
  供应商原始错误。

第六阶段第三小步已经加入进程级 AI 回复并发上限：

- `cmd/server` 默认最多同时执行 8 个模型生成，可通过
  `AI_MAX_CONCURRENT_REPLIES` 配置；
- 非流式回复和 SSE 流式回复共享同一组名额，不会各自放大供应商并发；
- 并发器位于 `customerservice.Service`，未来增加 gRPC、消息队列或其他入口也
  不能绕过；
- 无效请求、过期 Trigger 和已经保存的幂等回复不会占用模型名额；
- 容量用尽时立即返回 `429 resource_exhausted`，不建立可能过期的无限等待队列；
- 普通调用在模型方法返回时释放；流式调用在 EOF、读取失败或 `Close` 时释放，
  多条终止路径只会归还一次名额。

第六阶段第二小步已经加入 HTTP 请求身份、基础访问控制与单实例请求限流：

- 所有已注册路由都会生成或安全透传 `X-Request-ID`，并通过标准 Context 向下游传播；
- `/healthz` 保持匿名可用，三个 `/v1` 业务端点统一使用 Bearer Token 鉴权；
- 启动时强制校验 `API_AUTH_TOKEN`，缺失或弱 token 会让服务 fail-fast；
- 服务端只保留 token 的 SHA-256 摘要，并以常量时间比较认证凭据；
- 缺失、格式错误、重复或不匹配的凭据统一返回安全的 `401 Problem Details`；
- 认证后的 `/v1` 请求共享令牌桶，默认每秒补充 20 个令牌、最多突发 40 个请求；
- 超限请求立即返回 `429 resource_exhausted` 和 `Retry-After`，不会进入 Handler；
- Request ID、鉴权和限流仍只属于 Hertz/Bootstrap 边界，业务层、Repository 和
  模型适配器均未感知。

第五阶段已经加入可切换的 PostgreSQL 持久化基础设施：

- 业务层继续只依赖 `conversation.Repository`，运行时可选择 memory 或 postgres；
- 使用 pgx v5 连接池和短事务实现会话、消息、外部幂等键与回复索引；
- 使用会话 advisory lock 保护“尚不存在的会话”并发创建；
- 使用数据库唯一索引选出并发幂等赢家，Service 回读并验证完整请求数据；
- 使用 Tern 管理具名约束、复合外键、稳定消息顺序和 schema 版本；
- 提供固定 PostgreSQL 16.15 的 Docker Compose，以及双连接池真实并发测试。

第四阶段第三小步已经实现“HTTP SSE 流式自动回复闭环”：

- 使用产品自己的 Thrift IDL 定义跨语言 HTTP 契约；
- 使用 `hz model` 只生成轻量 DTO，Router 与 Handler 保持手写和依赖注入；
- 通过 Hertz 暴露存活检查、接收客户消息、非流式回复和 SSE 流式回复 API；
- 使用 omni-agent-kit `fault/problem` 输出安全一致的 Problem Details；
- 将 AI 环境配置集中在 `internal/bootstrap`，供 console 与 server 复用；
- 回复接口使用 `TriggerMessageID` 防止重复回复和过期回复写入；
- 流式回复区分临时 `reply.delta`、已保存的 `reply.completed` 与未保存的
  `reply.superseded`；
- 客户端断开或生成超时会取消 Eino stream 并关闭底层 Reader，不保存半截回复；
- 首次生成返回 201，重复请求返回 200、同一个 Message ID 和已保存内容；
- 提供不启动端口的 Router、Handler、错误语义和真实业务链路测试。

第三阶段的“非流式 AI 自动回复闭环”继续保留：

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
请求流：
外部渠道 → Request ID → Zap AccessLog → 排空闸门 → Bearer 鉴权 → 令牌桶限流 → Hertz Handler
                                                                                 │
                                                                                 ▼
                                                             internal/customerservice
                                                               ├── AI 回复并发槽
                                                               ├── ReplyObserver（业务端口）
                                                               ├──→ internal/conversation
                                                               ├──→ conversation.Repository
                                                               │          ├── MemoryRepository
                                                               │          └── postgres.Repository → PostgreSQL
                                                               └──→ ReplyGenerator / ReplyStreamGenerator
                                                                                   │
                                                                                   ▼
                                                                           internal/ai/eino → 模型供应商

组装流：
cmd/server → internal/bootstrap → 创建 Zap/hlog + 中间件 + Repository + ReplyGenerator
     └──→ 注入 ReplyObserver、两个 AI 接口与共享并发上限 → 创建 Handler → 注册路由
     └──→ SIGINT/SIGTERM → draining → Hertz Shutdown → completed / forced cancel

cmd/console → internal/bootstrap → 单轮 AI 链路
~~~

业务层只认识 `ReplyGenerator`：

~~~go
type ReplyGenerator interface {
    Generate(context.Context, ReplyRequest) (Reply, error)
}
~~~

因此模型供应商、本地模型和企业 AI 网关都可以替换，领域模型不依赖 Eino
类型，自动化测试也不需要 API Key。

可观测性使用同样的依赖倒置：业务层发布有限、无正文的 `ReplyObserver` 事件，
`internal/observability` 才负责把事件写成 Zap 日志。因此替换日志库、增加指标或
接入 Trace 时，不需要修改客服业务规则。

## 自动回复流程

模型调用位于两个短事务之间：

~~~text
事务一
  → 查找已有回复
  → 检查 bot 是否拥有回复权
  → 验证 trigger 是最新客户消息
  → 读取有序历史并记录 Revision / 消息水位

事务外
  → 非阻塞获取进程内 AI 回复名额
  → 调用 ReplyGenerator
  → 模型调用结束后释放名额
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
- 并发模型调用总数受进程级共享槽位限制；同一 Trigger 的两个调用仍可能在额度
  内同时发生，但只有一个结果能入库，其他调用返回同一条获胜回复；
- 非流式调用只在 `Generate` 执行期间持有名额；流式调用从 `Stream` 启动前持有，
  到底层流 EOF、报错或关闭时释放；
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

## HTTP 接入层

Thrift IDL 是 HTTP DTO/schema 的唯一真源，生成类型和手写代码彼此隔离：

~~~text
idl/customer_service.thrift
       │ hz model
       ▼
internal/transport/hertz/model/customer_service.go  生成 DTO，不手改
       │
       ▼
internal/transport/hertz/handler.go                  DTO ↔ Command/Result
       │
       ├── error.go                                  业务错误 ↔ fault
       ├── middleware.go                             Request ID + Bearer 鉴权
       ├── rate_limit.go                             单实例令牌桶 + 429
       ├── access_log.go                             Zap 请求生命周期日志
       ├── availability.go                           readiness + 排空闸门
       └── router.go                                 URL ↔ Handler
~~~

没有使用 `hz new` 生成整套项目骨架，因为现有应用已经有明确的 Service、
Repository 和构造函数注入边界。`hz model` 只生成 HTTP 所需类型，避免把全局
Handler 或 Thrift RPC processor 带进业务代码。`hz model` 不生成 Router，
因此 `router.go` 手工镜像 IDL 的 URL；路由和状态码行为由传输层测试锁定。

当前端点：

| Method | Path | 用途 |
| --- | --- | --- |
| `GET` | `/healthz` | 进程存活检查，不访问数据库或模型 |
| `GET` | `/readyz` | 是否应继续接收新业务流量；排空期间返回 503 |
| `POST` | `/v1/conversations/:conversation_id/messages` | 幂等接收一条客户消息 |
| `POST` | `/v1/conversations/:conversation_id/messages/:trigger_message_id/reply` | 为指定客户消息生成或读取机器人回复 |
| `POST` | `/v1/conversations/:conversation_id/messages/:trigger_message_id/reply/stream` | 以 SSE 流式生成回复并在完成后保存 |

`/healthz` 与 `/readyz` 不需要认证；所有 `/v1` 请求必须携带
`Authorization: Bearer <API_AUTH_TOKEN>`。所有成功和 Problem 响应都会包含
`X-Request-ID`：安全的调用方值会被透传，缺失、重复、超过 128 字节或包含不安全
字符时由服务端重新生成 UUID。Bearer 认证失败会在读取请求体、调用业务层、访问
数据库或模型之前返回 `401 unauthenticated`，并带 `WWW-Authenticate`。

收到 `SIGINT` 或 `SIGTERM` 后，服务先把 readiness 切为失败，并在鉴权之前用
`503 unavailable` 拒绝仍能到达路由的新 `/v1` 请求，然后立即调用 Hertz
`Shutdown` 关闭监听端口。端口关闭后，新连接会被拒绝；此时不能依靠新建连接
访问 `/healthz` 或 `/readyz`。目前没有内置等待负载均衡器更新的延迟窗口。

已经进入 Handler 的请求不会因第一次信号立即取消，而是获得最多
`SERVER_SHUTDOWN_TIMEOUT` 的排空时间。超时或 Shutdown 失败后取消请求根
Context，再给 Close 和 Run 收尾共用最多 1 秒等待预算。业务取消依赖下游遵守
Context；强制路径不会再同步等待可能阻塞的 PostgreSQL 连接池关闭。同步日志
写入和正常资源关闭不属于该等待预算，因此它不是进程退出的绝对墙钟上限。

当前 Hertz v0.10.5 的 standard 网络实现还有内部约 30 秒的排空上限；使用该
实现时，即使配置 `90s`，也可能提前报错并取消请求。默认 netpoll 实现按配置
预算等待。部署时应为实际使用的网络实现验证停机行为，并让外层进程管理器的
强制终止期限覆盖排空和收尾时间。

认证通过后，请求按照 `API_RATE_LIMIT_RPS` 和 `API_RATE_LIMIT_BURST` 进入一个
进程内共享令牌桶；默认值分别是 `20` 和 `40`。令牌不足时立即返回普通
`429 application/problem+json`，错误码为 `resource_exhausted`，并用
`Retry-After` 告知至少等待多少秒。已认证但请求体无效的请求仍会消耗令牌，因为
限流发生在 JSON 解析之前。一次 SSE POST 只消耗一个令牌，流中的 delta 不重复
计数；客户端重连是一次新请求，会再次参与限流。

请求令牌桶之后还有独立的 AI 生成容量保护。`AI_MAX_CONCURRENT_REPLIES` 默认是
`8`，只限制真正需要调用模型的新回复；接收消息、健康检查、无效/过期 Trigger
和已持久化回复的幂等读取不会占用名额。非流式与 SSE 共享同一组进程内槽位，
容量满时在调用 Eino/供应商之前立即返回 `429 resource_exhausted`。并发槽何时释放
取决于正在执行的模型调用，无法可靠预测等待秒数，因此这种 429 不伪造
`Retry-After`；SSE 也会在响应流开始前返回普通 Problem Details。

Handler 只按 `application/json` 解码，不允许 Query 或 Form 覆盖消息字段。
Thrift 的 `required` 表达 IDL/schema 约束，运行时基础非空校验来自 `api.vd`；
纯空格等业务无效值仍由 `customerservice.Service` 完成最终校验。URL 中的
`conversation_id` 是权威值，不接受请求体覆盖。

接收消息的响应语义：

- 新消息：`201 Created`，`duplicate=false`，返回当前 `next_action`；
- 完全相同的渠道重试：`200 OK`，`duplicate=true`，并返回原 Message ID；
- 同一个外部 Message ID 携带不同数据：`409 Conflict`；
- 参数错误：`400 Bad Request`；
- 未分类内部错误：`500 Internal Server Error`，响应不会包含 cause 或密钥。

生成回复的响应语义：

- 第一次生成并保存：`201 Created`，`duplicate=false`；
- 已存在同一 Trigger 的回复：`200 OK`，`duplicate=true`，返回数据库中原来的
  Message ID 和内容，不会再次调用模型；
- Trigger 已不是最新客户消息，或生成期间会话发生变化：`409 Conflict`，临时生成
  内容不会保存；
- 当前进程的 AI 回复槽位已满：`429 Too Many Requests`，不会排队或调用模型；
- AI 未配置、供应商调用失败或模型返回空内容：`503 Service Unavailable`；
- 客户端取消和请求超时分别保持 `408 Request Timeout` 与
  `504 Gateway Timeout`；
- Provider 的原始错误、响应正文和 API Key 都不会进入公开 Problem 响应。

SSE 回复的事件语义：

- `reply.delta`：模型新生成的临时文字片段，尚未保存；
- `reply.completed`：最终内容已通过二次会话校验并保存，该事件的完整内容是权威结果；
- `reply.superseded`：生成期间出现了更新的客户消息，之前的 delta 作废且不会保存；
- `error`：流开始后的超时、供应商或内部错误，只包含安全的 `code` 和 `message`。

如果首个事件产生前失败，接口仍返回普通 `application/problem+json`；SSE 一旦
开始，HTTP 状态码和响应类型已经发送，后续错误只能通过终止 `error` 事件表达。

错误统一使用 `application/problem+json`，例如：

~~~json
{
  "type": "about:blank",
  "title": "Bad Request",
  "status": 400,
  "code": "invalid_argument",
  "detail": "invalid request"
}
~~~

## 结构化日志与可观测性

`cmd/server` 使用一套共享的 Uber Zap Logger。`LOG_LEVEL` 支持 `debug`、`info`、
`warn`、`error`，默认 `info`；`LOG_FORMAT` 支持 `json` 和 `console`，默认
`json`。生产环境推荐 JSON，便于日志平台按字段查询；本地调试可以使用 console。
非法值会让服务在监听端口前直接启动失败。

| 变量 | 可选值 | 默认值 | 用途 |
| --- | --- | --- | --- |
| `LOG_LEVEL` | `debug` / `info` / `warn` / `error` | `info` | 最低输出级别 |
| `LOG_FORMAT` | `json` / `console` | `json` | 结构化 JSON 或本地可读格式 |

HTTP 层在请求完整结束后写一条 `http.request.completed`，稳定字段包括
`request_id`、`http_method`、`http_route`、`http_status`、`duration_ms` 和
`streaming`；已分类失败还会包含 `fault_code`。`http_route` 使用 Hertz 路由
模板，未匹配请求固定记录为 `unmatched`，不会把原始 URL、Query 或路径参数写入
日志。4xx 使用 warn，5xx 使用 error，其余使用 info；SSE 已经开始但最终失败时，
会按 `fault_code` 对应的失败级别记录，而不是把无法改变的 HTTP 200 当成成功。

SSE 写出是同步生命周期，因此访问日志不会在首个 delta 后提前结束：
`duration_ms` 覆盖到 EOF、流错误或客户端断开。SSE 已经开始后，即使 HTTP 状态
保持 200，日志仍可通过安全的 `fault_code` 表达真实终止原因。每个 delta 不单独
写日志，避免日志量随模型输出长度增长。

AI 层通过 `customerservice.ReplyObserver` 发布 `ai.reply.started`、
`ai.reply.rejected` 和 `ai.reply.finished`。`active_replies` 与
`max_concurrent_replies` 表示容量；`model_duration_ms` 只表示模型资源占用时间，
`total_duration_ms` 还包含生成后的校验与持久化；`outcome` 和
`terminal_phase` 是有限、安全的终止分类。

日志只保留定位故障所需的元数据，不写客户消息、模型回复、System Prompt、
API Key、Authorization、原始供应商错误或响应正文。排查时可使用 `request_id`
关联 HTTP 与 AI 生命周期，再结合 `outcome`、`terminal_phase` 和 `fault_code`。
AI 事件会记录 `conversation_id` 和 `trigger_message_id` 作为关联字段，因此调用方
必须使用不含手机号、邮箱等个人信息的不可读 ID。

## 本地运行

项目要求 Go 1.25 或更高版本；本地 PostgreSQL 流程还需要 Docker Desktop 或兼容
Docker Engine。当前代码使用 pgx v5.10.0、Tern v2.4.3 和 PostgreSQL 16.15。

### PostgreSQL 与迁移（可选）

默认 `STORAGE_DRIVER=memory`，不需要数据库，但进程退出后数据会消失。需要持久化
或多实例共享幂等键时，先启动项目专用 PostgreSQL：

~~~bash
docker compose -f deploy/local/compose.yaml up -d --wait postgres
~~~

本地 Compose 默认监听 `127.0.0.1:15432`，数据库仅使用开发凭据。应用迁移使用
同一个 `DATABASE_URL`：

~~~bash
export DATABASE_URL='postgres://omni_agent:omni_agent_local@127.0.0.1:15432/omni_agent_customer_service?sslmode=disable'
go run ./cmd/migrate
~~~

迁移命令可重复执行，Tern 只运行尚未应用的版本。不要对
`internal/persistence/postgres/migrations/*.sql` 直接运行 `psql -f`：每个 Tern
文件同时包含 up 和 down 两部分，只有 Tern 会按分隔标记选择正确方向。
PostgreSQL 模式启动时还会校验 Tern 版本和两张业务表；未迁移、版本不匹配或表
缺失时 server 会直接拒绝启动，而不是等到首个业务请求才报错。

真实数据库集成测试需要显式提供测试 DSN：

~~~bash
export TEST_DATABASE_URL='postgres://omni_agent:omni_agent_local@127.0.0.1:15432/omni_agent_customer_service_test?sslmode=disable'
go test ./internal/persistence/postgres -count=1 -v
~~~

集成测试会重置测试库中的 `customer_service` 业务表，因此测试数据库名必须以
`_test` 结尾；测试代码会拒绝迁移或清理其他数据库。不要把开发库或生产库 DSN
填入 `TEST_DATABASE_URL`，也不要让并行 CI job 共享同一个测试数据库。

### HTTP 服务（需要对应供应商配置）

`cmd/server` 同时暴露接收消息和生成回复接口，因此启动时会校验入站
`API_AUTH_TOKEN`、请求限流参数、AI 配置、AI 并发上限、日志配置和存储配置。
公网模型供应商需要对应 API Key；`custom` 指向无鉴权的本地 OpenAI-compatible
端点时可以不配置模型 Key。缺少必要配置，或者 RPS/Burst/AI 并发上限、日志配置
非法时会直接启动失败，不会等到第一次请求才暴露问题。

先把 `.env.example` 复制为被 Git 忽略的 `.env.local`，填入供应商配置，再加载到
当前 shell。`API_AUTH_TOKEN` 至少 32 字节且不能包含空白，可以用
`openssl rand -hex 32` 生成后填入；它是外部系统调用本服务的凭据，不能复用
`AI_API_KEY`。`API_RATE_LIMIT_RPS` 和 `API_RATE_LIMIT_BURST` 可以省略并使用
`20/40` 默认值；`AI_MAX_CONCURRENT_REPLIES` 可以省略并使用 `8`，也可以在
`.env.local` 中覆盖。`LOG_FORMAT=console` 更适合本地阅读，生产通常保留默认的
`json`。程序不会自行读取 dotenv 文件：

~~~bash
set -a
source .env.local
set +a

# 以下两项只在选择 PostgreSQL 时设置；memory 模式不需要。
export STORAGE_DRIVER=postgres
export DATABASE_URL='postgres://omni_agent:omni_agent_local@127.0.0.1:15432/omni_agent_customer_service?sslmode=disable'

go run ./cmd/server -address 127.0.0.1:8080
~~~

`cmd/server` 现在包含多个 Go 文件，启动时需要运行整个 package：
`go run ./cmd/server`。GoLand 的 Run Configuration 也应选择 Package/Directory，
仅运行 `cmd/server/main.go` 会遗漏同目录中的停机协调器。

`-address` 优先于 `HTTP_ADDRESS`；两者都不设置时默认监听 `127.0.0.1:8080`。

发送消息：

~~~bash
curl --request POST \
  --url http://127.0.0.1:8080/v1/conversations/conversation-001/messages \
  --header "Authorization: Bearer ${API_AUTH_TOKEN}" \
  --header 'Content-Type: application/json' \
  --data '{
    "customer_id": "customer-001",
    "channel": "web",
    "external_message_id": "web-message-001",
    "content": "我的订单还没到，应该怎么办？"
  }'
~~~

首次请求会返回类似：

~~~json
{
  "conversation_id": "conversation-001",
  "message_id": "44be5db0-3be0-440f-b938-989a89bd0155",
  "status": "bot_active",
  "next_action": "generate_bot_reply",
  "duplicate": false
}
~~~

`next_action=generate_bot_reply` 表示消息接收成功后应进入自动回复流程，不表示
当前 HTTP 请求已经调用 AI。这样渠道 webhook 不会被模型延迟或供应商故障拖住。

把接收响应中的 `message_id` 作为 `trigger_message_id`，再显式请求生成回复。这个
接口不需要请求体：

~~~bash
curl --request POST \
  --url http://127.0.0.1:8080/v1/conversations/conversation-001/messages/44be5db0-3be0-440f-b938-989a89bd0155/reply \
  --header "Authorization: Bearer ${API_AUTH_TOKEN}"
~~~

首次成功会返回 `201 Created`：

~~~json
{
  "conversation_id": "conversation-001",
  "trigger_message_id": "44be5db0-3be0-440f-b938-989a89bd0155",
  "message_id": "41074f6f-c798-4f89-bde0-386ff6bca194",
  "content": "很抱歉订单还没到。请提供订单号，我可以根据现有信息继续协助你核对。",
  "duplicate": false
}
~~~

也可以对同一个 Trigger 使用 SSE。`-N` 会关闭 curl 的输出缓冲，让每个事件立即
显示；这是会保存回复的写操作，因此使用 `POST` 而不是 EventSource 的 `GET`：

~~~bash
curl -N --request POST \
  --url http://127.0.0.1:8080/v1/conversations/conversation-001/messages/44be5db0-3be0-440f-b938-989a89bd0155/reply/stream \
  --header "Authorization: Bearer ${API_AUTH_TOKEN}"
~~~

正常响应类似：

~~~text
event: reply.delta
data: {"delta":"很抱歉，"}

event: reply.delta
data: {"delta":"请提供订单号。"}

event: reply.completed
data: {"conversation_id":"conversation-001","trigger_message_id":"44be5db0-3be0-440f-b938-989a89bd0155","message_id":"41074f6f-c798-4f89-bde0-386ff6bca194","content":"很抱歉，请提供订单号。","duplicate":false}
~~~

客户端应把 delta 当作可替换的界面预览；只有收到 `reply.completed` 才把其中的
`content` 当作最终回复。若收到 `reply.superseded`，应丢弃已展示的临时文本并处理
最新客户消息。对已有回复再次调用流式接口时，不会调用模型，而是直接发送一个
`reply.completed`，其中 `duplicate=true`。

回复接口会等待本次模型调用完成；应用层为整次模型生成设置 75 秒 deadline，
这个总预算包含供应商 SDK 内部重试，而不只是某一次 HTTP 尝试。对同一个
Trigger 重试时会直接读取已经保存的机器人消息，返回 `200 OK` 和
`duplicate=true`。当前拆成两个端点，是为了让渠道消息接收与较慢、可能失败的
模型调用具有独立的重试和超时边界。

当前 `cmd/server` 默认只监听本机回环地址，并默认使用 `MemoryRepository`；设置
`STORAGE_DRIVER=postgres` 后，消息、幂等键和回复会在进程重启后保留，并可由
多个服务实例共享。通用 Bearer Token 只负责调用本服务的第一层认证；接入微信、
钉钉等真实 webhook 时，仍需验证各渠道自己的签名/HMAC。生产环境还需要 token
轮换、多调用方身份、消息大小限制、数据库备份和最小权限账号。当前请求令牌桶
和 AI 回复并发槽都位于单个进程内，多实例部署若要求全局统一额度，应改由 Redis、
API Gateway 或专门的任务调度层协调。

### AI 配置与模型控制台

`.env.example` 只描述变量名；真实密钥保存在被 Git 忽略的 `.env.local`。
`cmd/server` 与 `cmd/console` 共用 `internal/bootstrap` 的同一套解析规则，推荐统一
使用以下配置：

~~~dotenv
AI_PROVIDER=deepseek
AI_PROTOCOL=
AI_API_KEY=
AI_MODEL=
AI_BASE_URL=
AI_ALLOW_INSECURE_HTTP=false
AI_SYSTEM_PROMPT=
AI_MAX_CONCURRENT_REPLIES=8
LOG_LEVEL=info
LOG_FORMAT=console
SERVER_SHUTDOWN_TIMEOUT=90s
~~~

- `AI_PROVIDER` 默认是 `openai`；
- `AI_PROTOCOL` 留空时使用上表中的厂商默认协议；
- `AI_API_KEY` 是推荐的统一密钥变量；
- `AI_MODEL` 对 OpenAI 可留空使用项目默认值，其他厂商必填；
- `AI_BASE_URL` 通常留空，使用 `custom` 时必填；
- `AI_ALLOW_INSECURE_HTTP` 默认 `false`，只用于明确接受风险的可信内网；
- `AI_SYSTEM_PROMPT` 可选，留空时使用项目内置的客服提示词；
- `AI_MAX_CONCURRENT_REPLIES` 是 `cmd/server` 的进程级并发上限，默认 `8`；
  `cmd/console` 每次只执行一轮，不需要启用该限制。
- `LOG_LEVEL` 和 `LOG_FORMAT` 只影响 `cmd/server` 的运行日志，不进入 AI 请求。
- `SERVER_SHUTDOWN_TIMEOUT` 使用 Go duration 格式并且必须大于 0；默认 `90s`。
  Kubernetes 等外层终止宽限期应比它至少多留数秒。

模型请求会把 System Prompt 和完整客户会话发送到最终解析出的 Base URL，配置
凭据时还会发送 API Key。为避免误配置造成客户数据明文泄露，所有非回环端点
默认必须使用 HTTPS；HTTP 只直接允许 `localhost`、`127.0.0.0/8`、`::1` 等
本机开发地址。只有在网络本身可信且明确接受窃听、篡改风险时，才设置
`AI_ALLOW_INSECURE_HTTP=true` 放行非回环 HTTP。

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

设置 `TEST_DATABASE_URL` 后，`go test ./...` 还会运行 PostgreSQL 提交、回滚、
重连持久性、约束和跨连接池并发用例；未设置时这些集成用例会明确跳过。

测试覆盖领域状态、ReplyTo 约束、事务回滚、模型输入映射、两种 HTTP 协议、
模型失败、空白回复、幂等重试、并发生成、生成期间转人工、ABA、新增消息、
流完成/取消/超时/过期、Eino 流映射、Hertz 路由、Thrift 绑定、SSE 事件编码、
Request ID、Bearer 鉴权、令牌桶限流、AI 回复共享并发槽、超限 429、流式名额
释放、并发不超卖、Zap 配置、HTTP 单请求单日志、路由模板脱敏、ReplyObserver
生命周期、容量快照、SSE 完整生命周期、终止事件 exactly-once、日志敏感字段
缺失、readiness 状态转换、排空期间新旧请求分流、停机超时强制取消、Retry-After、
Problem 错误响应、HTTP 回复幂等和过期 Trigger 等情况。协议
测试使用进程内测试引擎或本地 `httptest` 服务，不需要真实 API Key，也不产生
费用。

修改 IDL 后，固定生成器版本并重新生成 DTO：

~~~bash
go install github.com/cloudwego/hertz/cmd/hz@v0.9.7
go install github.com/cloudwego/thriftgo@v0.4.3
go generate ./internal/transport/hertz
~~~

`omni-agent-kit` 当前是私有 Go module。新电脑或 CI 在下载依赖前需要配置可读取
该仓库的 GitHub 凭据，并将模块声明为私有，例如：

~~~bash
GOPRIVATE=github.com/qiuyier/omni-agent-kit go mod download
~~~

如果以后将 kit 公开，这组私有模块配置就不再需要。

## 后续候选

当前 SSE、PostgreSQL 持久化、Request ID、基础 Bearer 鉴权、单实例请求限流、
AI 回复共享并发上限、Zap 结构化日志与可控优雅停机已经完成业务层、Eino 适配、
Hertz/Thrift 契约和自动化测试。下一步可以让同一个 `ReplyObserver` 输出
Prometheus 指标或 OpenTelemetry Trace，而不修改客服业务规则；也可以继续增加
多调用方身份、分布式限流/AI 并发协调、生产日志采样与告警规则，或真实浏览器
SSE 客户端。无论选择哪一项，`TriggerMessageID` 的幂等与过期回复保护仍留在
应用层。
