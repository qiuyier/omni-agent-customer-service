# 最小使用交付：从体验到业务接入

更新：2026-10-26。面向通用客服，不绑定 CRMEB。本指南是统一使用入口；交付范围见 [1.0.0 基线](v1-scope.md)，不是已发布安装包，也不承诺任意系统免适配。

## 推荐完整路径

1. **准备与首次启动**：按 [固定参考部署](../deploy/reference/README.md#首次准备)检查 Go、Node、Docker，初始化私密配置、构建、启动专用库、迁移、预检，再 start。已有配置不重复 init；不要使用业务原库。
2. **跑通完整接待**：按同文档“日常使用”完成客户消息 → 申请人工 → 坐席接管回复 → 结束 → 新咨询，再核对第二客户隔离。默认 fake，不花模型费用。
3. **接入自己的系统**：按下方 B 替换演示身份与授权并挂载页面；没有客服模块也可以接入。目标身份、退出及隔离必须重新验收，不能复制参考系统的通过结论。
4. **启用 AI 与资料**：参考系统按 [显式 AI 配置](../deploy/reference/AI.md)，目标系统按下方 C；先确认数据授权与费用，执行 check-ai 后再 start-ai。fake 验收脚本不能用于 AI；按授权的小样本人工核对，不自动追加重试。
5. **验收与交接**：参考 fake 使用其 verify.mjs 写验收及实际重启后的只读比较；目标系统按 [交接表](../integrations/host-kit/starter/handoff.md)记录版本、身份隔离、人工闭环和重启恢复。AI 另记录模型、资料和实际请求数。
6. **停止与恢复**：停止应用后停止专用库，保留配置、卷和稳定来源；恢复先开库、预检再启应用。AI 使用 check-ai/start-ai，fake 使用 check/start；重启后重新登录，不清库、不重建身份。

参考部署从准备到停止的命令都在同一份部署说明中；下方 A 是可选的内存快速体验，不是完整系统交付路径。

## 先选路径

| 你要做什么 | 使用入口 | 不能混淆的边界 |
| --- | --- | --- |
| 跑通持久化＋人工完整系统（推荐） | [固定参考部署](../deploy/reference/README.md) | 演示认证；默认 fake，AI 显式配置 |
| 立即体验客户聊天 | 下方 A：webchat-demo | 模拟登录、内存记录、固定回复，默认没有人工 |
| 在现有本机 CRMEB 体验完整接待 | [联合验收与操作说明](../integrations/crmeb/Joint-flow-acceptance.md) | 已有身份适配的实例，目前恢复为 fake；不是通用安装器 |
| 接入自己的业务系统 | 下方 B：webchat-server + 身份适配 + 页面 | 必须有可信业务认证及客服专用 PostgreSQL |
| 接入业务资料和真实 AI | 下方 C | 实例级配置，需另行确认数据范围及费用 |

## A. 不用密钥、数据库或临时启动脚本的体验

从仓库根目录执行，需要 Go 1.25 或兼容版本；首次构建可能下载 Go 依赖。页面已内嵌，不需要先启动前端开发服务器。

```sh
delivery_bin=$(mktemp -d /tmp/omni-minimal-delivery.XXXXXX)
go build -o "$delivery_bin/webchat-demo" ./cmd/webchat-demo
go build -o "$delivery_bin/webchat-server" ./cmd/webchat-server
go build -o "$delivery_bin/migrate" ./cmd/migrate
"$delivery_bin/webchat-demo" -check-config
"$delivery_bin/webchat-demo" -address 127.0.0.1:0
```

打开终端输出的实际地址，选择演示账号，进入聊天，发送虚构消息，刷新确认原消息和回复仍在。端口 0 表示系统分配空闲端口；不要沿用历史报告里的随机地址。需要固定端口时可改为 `127.0.0.1:8092`，先确认没有占用。

停止：在该终端按 Ctrl+C。内存演示停止后消息不保留；浏览器刷新保留不等于重启持久化。构建目录只是本机临时产物，正式交付应放到部署方管理的版本目录。这里不启动 Docker、不读取根 .env、不调用模型。

## B. 接入真实业务：网关与网页是两层

需要先跑一套固定、持久化且带人工的完整参考系统，可按 [通用本机参考部署](../deploy/reference/README.md)执行；独立数据库、稳定配置与启动/停止入口不依赖 browser-lab。它仍是演示认证，默认 fake、显式支持 AI，不能代替下方真实业务接入。

正式网关只有 API，不提供登录页，也不直接提供客户/坐席页面。不能把网关地址当成可登录客服网站。

1. 确定稳定系统标识、客户 ID、授权员工 ID；业务登录/退出/禁用由原系统负责。
2. 准备客服专用 PostgreSQL 和独立服务端密钥，按 [配置模板](../deploy/webchat-server.env.example)由进程管理器或密钥管理器注入。程序不会自动加载 `.env`；不要把根 .env 整份复制到生产，也不要把密钥放到命令参数、网页或报告。
3. 构建上面的 webchat-server、migrate。至少注入 DATABASE_URL、WEBCHAT_HOST_ID、WEBCHAT_HOST_SECRET；先使用 WEBCHAT_REPLY_MODE=fake。需要人工时配置 WEBCHAT_AGENTS_JSON，映射到已授权员工。
4. 在同一受保护环境运行以下命令。迁移有写入，仅在数据库负责人确认目标库、权限和备份后执行，绝不把业务原库当测试库。

```sh
"$delivery_bin/webchat-server" -check-config
# 首次部署、目标数据库已确认且获得迁移授权后，才执行：
"$delivery_bin/migrate"
"$delivery_bin/webchat-server" -check-resources
"$delivery_bin/webchat-server"
```

`-check-config` 检查格式；`-check-resources` 连接数据库/检查表结构，在 AI 模式读取 FAQ，但不调用模型。缺表不能靠切成内存模式绕过。迁移输出需按敏感运维日志保管，不能直接公开底层错误。

5. 在业务服务挂载身份适配及同源路由，提供客户聊天页和坐席页。Node 优先使用 [host-kit 最短装配](../integrations/host-kit/quickstart.md)，默认 fake＋人工；显式 ai 需 postgres、已配置的 AI 网关及一致的模式元数据，参照 [固定部署 AI 说明](../deploy/reference/AI.md)。PHP/Java 按 [接口与职责](system-integration.md)实现等价边界，不能只改模式提示代替实际配置。
6. 前端身份、登录、退出 URL 与原系统保持一致；私有宿主/坐席密钥只在服务端，禁止公开 `/internal/*`。网关只监听回环；跨容器不等于把它改成公网监听，参考 [私有通道实验的边界](../deploy/private-containers/README.md)。
7. 按 [交接表](../integrations/host-kit/starter/handoff.md)验收真实登录、两个客户隔离、转人工、接管、回复、关闭、新咨询、注销与重启恢复。示例通过不能代填你的系统。

日常接待：客户在原业务登录后进入已挂载聊天页 → 申请人工 → 已授权员工登录原业务并进入工作台 → 选择会话并接管 → 回复 → 结束；客户看到结束后再发起新咨询。具体 URL 由接入方配置，不要求叫 `/chat/` 或 `/desk/`。

## C. 配置模型与业务资料

先跑通 B，再由服务端显式启用 WEBCHAT_REPLY_MODE=ai，配置 AI_PROVIDER、AI_PROTOCOL、AI_MODEL、AI_BASE_URL、AI_API_KEY。API 根地址不是完整 `/chat/completions` 地址；外部端点使用 HTTPS，密钥不能进入前端。网关与业务后端返回的 reply_mode 必须一致。

关键词知识检索最小配置：KNOWLEDGE_RETRIEVER=keyword，KNOWLEDGE_FAQ_PATH 指向受控 UTF-8 JSON 文件。结构示例（仅虚构资料）：

```json
[
  {"id":"service-hours","title":"测试服务时间","source":"fixture/hours","content":"虚构服务台周一至周五09:00—17:30开放；节假日安排未提供。","keywords":["服务时间","开放时间","节假日"]}
]
```

文件上限 1 MiB；留空路径表示不开知识检索，`[]` 表示启用了知识但当前无资料，坏 JSON 不是撤回。先运行两层预检，明确模型接收的数据与预算，再发送有据、无据两类虚构问题人工核对。预检通过不等于密钥有效或回答准确。

AI 业务适配须满足 [AI 等待预算与失败语义](webchat-ai.md)，可参考 [独立宿主实现](../integrations/standalone-host/README.md)或 CRMEB 的已安装适配；这些示例不替代目标业务认证。不同业务需要独立 AI/知识时按独立实例部署，当前多宿主共享实例只支持 fake/人工。

更新资料时先安全替换文件，再按 [知识重载说明](knowledge-reload.md)对确认属于目标实例的实际服务 PID 发送 HUP，并核对成功事件及版本。不能向 `go run` 父进程或未知 PID 发信号。重载不改写历史；模型、来源、文件路径变化须更新进程配置并重启。

## 停止、恢复与排错

- 前台进程 Ctrl+C；托管进程按部署方的服务名停止。仅操作自己的客服服务，不重启共享业务容器。
- 停止前暂停新请求并处理正在进行的咨询；保留 PostgreSQL 数据、稳定来源标识与身份映射。不要用删库/删卷当回退。
- 恢复先启动专用数据库、核对资源，再启动网关及业务适配；旧短期凭据可能失效，重新验证原业务登录并换取凭据。
- 401：检查原业务登录/授权，重新登录；不要绕过鉴权。503：检查网关就绪、数据库或模型，禁止悄悄降级假回复。
- 发送结果不明：先查回执/刷新，必要时沿用原 event_id 和正文手动重试，不盲目新建事件。
- 关闭后不能发送是正常行为，应开始新咨询。队列为空先检查客户是否已申请人工、坐席来源/授权是否正确。

## 历史复跑记录与最新证据

2026-10-24 构建 demo、server、migrate 成功，demo 配置检查通过；通用装配三个测试文件合计 14 项通过，覆盖原业务登录、挂载、人工闭环、隔离与适配层重启。2026-10-25 续跑直接二进制启动，浏览器选账号、发送、固定回复、刷新保留通过，最后 Ctrl+C 正常退出。未使用临时实验启动器完成 A；临时目录只存编译产物。

本轮没有调用模型、迁移数据库、重启 CRMEB，未重新完成 B 的独立持久化部署。B/C 是已核对源码及现有验收证据的部署步骤，不冒称本轮在全新生产环境执行过。完整 AI/人工浏览器证据见 [核心闭环](core-flow-acceptance.md)。

还差什么见 [1.0.0 发布前清单](release-readiness.md)，非阻断完善见 [发布后优化录](post-1.0.0-improvements.md)。

2026-10-26 补充：固定参考部署已完成 [真实 AI → 人工 → 新咨询 → 应用/数据库重启](../deploy/reference/AI-acceptance.md)验收，2 次真实请求均成功。此前关于未重跑 B 的表述仅指 2026-10-24/25 那轮，不是当前固定部署状态。本轮范围整理只核对文档与入口，不重复调用模型或宣称生产验收。
