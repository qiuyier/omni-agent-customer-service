# 独立业务后端接入样例（Node.js）

可选[人工接管闭环](../../docs/human-handoff.md)已完成本地验收：显式 --allow-human-desk 启动人工队列与工作台，未配置时保持关闭。默认示例仍不提供人工功能。

要接入新的业务系统，请从[配置与启动检查标准清单](../../docs/host-onboarding.md)开始；正式网关支持 -check-config 和 -check-resources，两者均不调用模型或embedding。本目录启动器、模拟登录和故障代理仅用于本地验收。

目的：展示一个没有客服能力的业务后端，如何维护自己的登录，再通过 HTTP 接入通用客服。此样例不调用 cmd/webchat-demo，也不依赖 CRMEB；聊天页面资源和 Go 客服核心直接复用，没有复制。

## 启动

需要 Node.js 20+、Go（以仓库 go.mod 为准），从仓库根目录执行：

```sh
node integrations/standalone-host/run.mjs
```

打开终端显示的回环地址，选择林澈或周宁，进入客服。账号选择是无密码测试登录，不是生产认证。业务端内部 ID 为 member-1001、member-1002，与现有模拟宿主的用户 ID 不同。

Node 业务服务和 Go 私有网关是两个进程、两个回环端口。启动器生成独立随机宿主凭证，通过网关子进程环境变量及 Node 内存传递，不打印、不交给浏览器。不读取 .env，不接数据库或模型，不修改现有 CRMEB/客服实例。

Ctrl+C 停止本轮进程并删除临时构建目录。登录会话和消息都为内存数据：刷新或重新登录可恢复同一网关进程内的消息；进程重启后丢失，不能当作持久化方案。

## 阅读顺序

1. [host.mjs](host.mjs)：独立 Node 业务后端；只通过 HTTP 调用客服，不导入 Go 核心。模拟账户表替换为真实业务认证的位置在 /demo/login 与 loginSession。
2. [run.mjs](run.mjs)：本地进程与凭证装配，默认绑定随机回环端口。
3. [gateway/main.go](gateway/main.go)：测试用装配入口，复用 conversation、customerservice、webchat；无登录、UI、全权限 /v1 路由或真实模型，仅固定回复。
4. [host.test.mjs](host.test.mjs)：真实 HTTP/真实 Go 网关验收，以及延迟响应的退出竞态测试。
5. [通用网页接入契约](../../docs/browser-chat.md)：生产集成前需要理解的权限与交付边界。

## 最小接口清单

| 位置/接口 | 谁实现 | 要点 |
| --- | --- | --- |
| 业务登录与退出 | 业务后端 | 真实认证取代模拟账号选择，服务端维护登录会话，退出/切换账号撤销旧会话 |
| GET /host/me | 业务后端 | 返回当前已认证用户与可信的存储/回复模式，不接受客户端自报身份 |
| POST /host/chat-session | 业务后端 | 只接受空对象，从登录会话取稳定用户 ID；调用私有身份交换，返回短期受限凭证 |
| POST /internal/chat/sessions | 私有客服网关 | 后端独立 Bearer + customer_id；不能通过公共代理公开 |
| GET/POST /chat/v1/messages | 业务后端精确代理 | 同时校验登录 Cookie 与该登录绑定的聊天凭证；POST 只收 event_id/content |
| /chat/、/assets/app.js、/assets/app.css | 业务网站同域挂载 | 复用现有资源；本样例直接从 internal/webchat/ui 读取，不复制构建产物 |

本样例沿用前端的 /demo/login、/demo/logout 模拟接口，以免修改现有聊天页。真实业务应使用自己的登录入口，接入会话生命周期；这些模拟接口不应上线。

## 配置与安全责任

startHost 接受后端可信 gatewayOrigin、hostSecret，以及 storage（memory/postgres）、replyMode（fake/ai）和 port（回环监听端口）。默认 storage=memory、replyMode=fake；参数不接受浏览器覆盖，本地只允许字面量127.0.0.1 HTTP。replyMode 必须与实际网关一致，不是模型可用性探针；AI 验收启动器统一设置网关和业务后端。AI 发送代理等待上限120秒（网关处理上限110秒），其余请求仍为15秒。默认 run.mjs 和原固定回复验收不读取 .env、不调用模型。

- 前端只能拿到短期受限凭证；业务后端缓存并绑定到登录会话，不能用其他账号的 Cookie 搭配该凭证。
- Cookie 为 HttpOnly、SameSite=Strict；仅因回环 HTTP 演示不设置 Secure。生产必须 HTTPS、Secure Cookie、真实认证、CSRF 防护和独立会话存储。
- 严格 Host/Origin、JSON 字段及精确路由白名单；不复制浏览器请求头到上游，不转发上游 Cookie、重定向或错误正文。不支持通配代理、任意路径、客户端指定会话/角色。
- 退出后再校验上游返回前的登录状态，避免迟到历史泄漏。已准入的发送可能完成，退出不回滚消息或业务事务。私有网关凭证仍有效至到期，生产必须使网关无法被浏览器绕过业务代理直接访问。
- 登录会话最多128个，每小时最多128次登录，单会话最多32次身份交换，同时最多16个公共请求；这些是本地演示保护，不是分布式限流/会话服务。
- 生产应接 cmd/webchat-server 并配置 WEBCHAT_HOST_ID、独立 WEBCHAT_HOST_SECRET、独立 DATABASE_URL，以及明确的回复与知识设置。本样例网关不支持这些生产配置；不要直接开放到公网。

不能从本样例推断共享多租户知识隔离、数据库灾备、附件、业务查询、工单或人工接管已实现。

## 验证

最新完成[独立业务接入真实 AI 验收](ai-acceptance.md)：6次真实模型调用，覆盖知识、多轮、未知问题、人工能力边界、模型故障恢复、回复重放与双账号隔离。默认启动方式不变，只有下面的显式模式读取根目录 .env 中的 AI_PROVIDER、AI_PROTOCOL、AI_MODEL、AI_BASE_URL、AI_API_KEY（不执行shell、不加载数据库或知识配置）：

```sh
node integrations/standalone-host/browser-lab.mjs --allow-local-postgres --allow-real-ai
```

本轮使用 Node.js 26.7.0；AI 验收工具需要支持 node:util 的 parseEnv。仅支持显式 HTTPS Chat Completions 端点与通用 AI_API_KEY，真实请求最多12次。使用本目录 ai-faq.json 虚构资料、keyword 检索及核心默认提示；不向 embedding 发送数据。终端 model-down/model-up 控制本机故障注入，drop-next 丢弃下一次成功回复的响应，stop 关闭本轮服务及专用测试数据库并保留数据卷。故障代理仅供回环测试，不可生产部署；报告不自动宣称浏览器验收通过。

进一步已完成[真实浏览器中断恢复验收](browser-acceptance.md)：实际页面上的凭证续期、重新登录、待确认重试和跨标签页账号隔离通过，附截图与网络证据。

后续已完成[独立 PostgreSQL、双进程重启及响应丢失验收](persistence-acceptance.md)。运行 `node integrations/standalone-host/persistence-check.mjs --allow-local-postgres` 会启动专用本地数据库、执行迁移和验收；结束后停止容器并保留数据卷，不调用真实模型。

本轮执行结果见[验收记录](acceptance.md)，包括通过项和未覆盖边界。

```sh
node --test integrations/standalone-host/host.test.mjs
go test ./integrations/standalone-host/gateway ./internal/webchat/... ./internal/customerservice ./internal/conversation
```

测试覆盖未登录、伪造字段、跨源、两个账号串用凭证、刷新、同事件重放、同名事件独立、退出/切换账号失效、重新登录历史恢复和退出时迟到响应。HTTP 页面资源检查不等于真实浏览器视觉验收。
