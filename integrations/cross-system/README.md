# 跨系统接入与隔离验收

## 2026-10-16 密码登录与共享代理补充验收

`isolation.test.mjs` 现在复用同一套隔离测试矩阵运行两组装配：

- `demo`：保留原有两个 standalone-host，用户/坐席标准 ID 完全相同。
- `password`：office-portal 与 fresh-onboarding 使用各自原生密码登录及共享安全代理，连接同一个临时 Go 网关、同一个内存仓库，分别配置 A/B 来源凭据。不是两个独立网关，也未新建行业样例。

两组各自在单个网关上验证：不同来源会话不同；跨系统 Cookie/令牌和伪造来源字段被拒绝；相同消息事件编号分别保存；坐席队列仅包含本来源会话；对另一来源会话的 history/claim/reply/read/close 全部返回 403；本来源接管、回复、重试去重与已读成功；关闭/新开咨询不推进另一来源会话；切号/退出不影响另一来源。

新增切号断言在 A 从 lin 切至 zhou 后，验证旧登录/旧聊天凭证被拒绝、新客户历史为空，并对 B 的人工历史完整响应做前后相等检查，确认消息、revision 和归属没有变化。

密码组两边原始业务编号均为 MEM:731，坐席映射均为 staff-1。客户标准 ID 沿用现有业务映射（member_ 与 person_ 前缀不同），不为测试改造业务身份；标准客户 ID 完全碰撞的证据仍由 demo 组提供。随机密码与宿主/坐席密钥均不出现在登录和会话响应中。

专项 **21 项通过**（含两个父测试）。正常退出后清理测试服务器、网关及本轮临时构建目录。复现仍用下方原命令。

本次是自动化真实 HTTP 验收，不是新增浏览器现场验收；每个请求显式传入对应 Cookie。此前的同浏览器 Cookie 并行证据见 [Cookie 隔离验收](../host-kit/cookie-isolation.md)，两份证据不能拼成“已在浏览器完成共用网关的全流程”。未使用数据库、真实模型、CRMEB 或生产服务，未修改客服核心/业务实现。

尤其注意：多来源网关是测试夹具直接装配已有能力。正式 `cmd/webchat-server` 仍通过单个 HostID/HostSecret 装配一个宿主；本轮不代表已经交付生产多宿主配置，也不证明多租户知识、模型、持久化和配额隔离。以下保留早期验收记录。

2026-10-13。目的：确认接入能力不依赖 CRMEB，且不同来源即使用户、坐席、事件编号相同，也不能串用会话权限。

仓库已有非电商的设备维修样例和独立 Node 业务宿主，本轮不再复制一套系统。使用现有 `standalone-host/host.mjs` 启动两套独立 HTTP 业务后端，连接同一临时 Go 网关、同一内存仓库，让隔离依赖来源授权而非“恰好在两个数据库”。

两个来源为 `commerce-fixture` 和 `repair-portal`。前者是电商侧的**协议替身**，不是实际 CRMEB PHP 适配器，也不读取 CRMEB 账号、配置或消息。真实 CRMEB 登录接入已在 [CRMEB 人工验收](../crmeb/Human-acceptance.md) 单独验证；不能把两份验收描述成真实 CRMEB 与另一生产系统同机共享的现场验收。

## 运行

需要仓库要求的 Go 与 Node.js 20+。在仓库根目录：

```sh
node --test integrations/cross-system/isolation.test.mjs
```

测试构建临时二进制、分配随机回环端口、生成四份独立随机凭据，只放入服务端内存/子进程环境。不读取 .env，不接数据库，不调用外部模型或 embedding，不修改当前 CRMEB 或常驻服务。正常通过或断言失败后关闭本轮服务并删除自身 mkdtemp 临时构建目录，消息随内存实例结束而消失。

两个 HTTP 客户端显式管理各自 Cookie，不使用浏览器共享 Cookie 存储。因此本轮不验证同一域名不同端口的浏览器 Cookie 隔离；Cookie 不按端口隔离，真实部署需使用合适的域名、Cookie 名/路径及会话策略。无密码账号选择仅用于测试，不能用于正式身份认证。

## 2026-10-13 结果

新增 8 组子场景及 1 个父测试通过；合并已有宿主回归共 15 个 Node 测试通过。Go webchat/UI/server 的 race 回归通过，测试网关编译通过。

| 场景 | 结果 |
| --- | --- |
| 两系统相同用户 ID、相同坐席 ID | 会话编号不同，宿主/坐席密钥不返回客户端 |
| 跨系统 Cookie、自己的 Cookie 配另一系统聊天凭证 | 401 拒绝 |
| 前端自报 source_id/customer_id，公开调用私有身份交换 | 拒绝 |
| 相同消息 event_id，不同内容 | 各系统独立保存，无错误冲突或串历史 |
| 两边申请人工、查看队列 | 各自仅见本来源会话 |
| 知道对方会话 ID，尝试 history/claim/reply/read/close | 均 403，无对方历史正文 |
| 本来源接管、人工回复、原事件重试、已读 | 成功；人工回复只保存一次 |
| A 关闭、开启新咨询 | 旧凭证失效，新历史为空，原历史保留；B 仍在原人工会话 |
| A 客户/坐席退出 | A 后续请求拒绝；B 客户与坐席仍有效 |
| 客户凭证或宿主凭证充当私有坐席凭证 | 401 拒绝 |

复跑命令：

```sh
node --test integrations/standalone-host/host.test.mjs integrations/cross-system/isolation.test.mjs
go test -race ./internal/webchat/... ./cmd/webchat-server ./integrations/cross-system/gateway
```

## 结论与限制

本轮没有修改客服核心、已有 Node 宿主或 CRMEB；只新增测试装配、端到端回归和接入说明。
设备维修等没有原生客服的系统可以通过自己的身份适配接入，人工能力由通用客服提供。

测试网关直接装配已有 `webchat.Config.Hosts` 的多来源能力，**不是**给正式 `cmd/webchat-server` 增加多宿主启动配置。
正式入口目前仍是一实例一宿主、一套模型/知识配置；不同业务系统应按独立网关、存储及授权知识配置部署。
本轮不证明多租户知识隔离、真实企业 SSO、生产安全、持久化恢复、高可用或任意规模容量。

阅读顺序：[统一接入路径](../../docs/system-integration.md) → [HTTP 回归](isolation.test.mjs) → [测试网关装配](gateway/main.go) → [现有业务适配器](../standalone-host/host.mjs)。
