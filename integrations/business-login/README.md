# 业务密码登录接入验收（2026-10-13）

目的：将已有身份回调模板接到一个真正校验密码、维护登录会话的最小业务认证服务，再通过真实 HTTP 和 Go 网关验证人工闭环。
这是本地维修业务的测试认证服务，不是企业 SSO、生产 IAM，也不是继续使用“任选演示账号”的登录。

## 实现与运行

```sh
node --test integrations/business-login/acceptance.test.mjs
```

测试自动生成两个客户、一个授权员工和一个未配置坐席权限的员工，密码均为本轮随机值，不打印或保存到文件。
business.mjs 使用每账号随机盐及 scrypt 摘要校验密码，服务端保存随机会话标识的摘要；Cookie 为 HttpOnly/SameSite=Strict，客户与员工使用不同名称。
HTTP 仅绑定随机回环端口，未配置 Secure 仅因本地 HTTP 测试；正式环境需要 HTTPS。

业务层通过 businessIdentity({sessions}) 注入现有 startAdapter，未传 demoAccounts。
共享适配器、身份模板、验收器与 Go 核心均未为本轮修改。
业务层仅实现登录/退出、认证回调、测试用状态管理和精确路径代理；代理校验自己的 Host/Origin 后才设置私有适配器的 Host/Origin，不信任转发头。
未知路径包括演示登录和私有网关接口均不代理。

## 实际验收

新增7组子场景及父测试，结合既有宿主/接入包/跨系统测试合计28项 Node 测试；Go webchat/server race 回归另行执行。

- 错误密码、错误角色登录、伪造角色字段、跨源请求拒绝；/demo/login 和 /desk/login 不存在。
- 配置化验收器改用 /auth/customer/login、/auth/staff/login 等业务入口，完成双客户隔离、消息重放、转人工、接管、人工回复、已读、关闭、新咨询和退出。
- 客户切号撤销该 Cookie 的旧登录，旧 Cookie 与新账号搭配旧聊天凭据均拒绝。
- 客户禁用后读取与重新登录均拒绝；重新启用不会恢复旧会话，必须重新登录。
- 员工禁用、重新启用、退出和切换到无坐席映射的员工后，旧工作台请求拒绝；“有员工账号”不等于“有客服权限”。
- 用注入时钟验证登录到期，而不是等待真实30分钟；此时不能续签客户会话或读取工作台。
- 将员工 Cookie 改名伪装客户（及相反情况）既不能越权，也不会误删原角色有效登录。

禁用操作仅由测试进程调用 setEnabled，**没有**公开 HTTP 禁用接口；没有新增管理员密码或公网权限。
同一角色切换撤销的是该请求带来的旧会话；禁用会撤销该账号全部会话。一次普通登录不会自动踢掉其他设备，这两种语义不同。

## 验证边界

“旧页面失效”在本轮指旧页面保存的 Cookie/聊天凭据后续 HTTP 请求被拒绝，不代表已显示的文字能被服务器抹去。
本轮没有新增登录 UI 或执行浏览器视觉验收；请求中退出的迟到响应另有 host-kit 回调测试，此处不混称为新做了真实页面并发验收。
已经被网关接受的写操作可能完成，退出不回滚；私有网关短期凭据仍可能有效到到期，所以不可允许绕过业务代理。

登录数据、消息仓库均为内存，测试结束关闭本轮服务并清理自身临时构建目录。使用已有双来源测试网关的 repair-portal 来源，只开固定回复；不改 CRMEB，不接业务数据库，不读取 .env，不调用模型或 embedding。
使用同步 scrypt、进程内会话、固定总尝试次数限制，只适合本地测试；未提供持久化、分布式撤销、找回密码、MFA、审计、生产限流或容量保证。业务认证调用超时、HTTPS、可信反向代理和会话存储仍需生产集成设计。

## 阅读顺序

1. 本文：范围、结果及未覆盖项。
2. [business.mjs](business.mjs)：密码认证、角色会话、禁用撤销和受限代理。
3. [acceptance.test.mjs](acceptance.test.mjs)：真实 HTTP、随机账号、统一验收器与负向场景。
4. [已有身份模板](../host-kit/identity.example.mjs)和[接入包说明](../host-kit/README.md)：业务认证接入位置。

```sh
node --test integrations/business-login/acceptance.test.mjs integrations/host-kit/adapter.test.mjs integrations/standalone-host/host.test.mjs integrations/cross-system/isolation.test.mjs
go test -race ./internal/webchat/... ./cmd/webchat-server
```
