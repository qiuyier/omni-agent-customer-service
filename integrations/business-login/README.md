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

## 浏览器接入补充（2026-10-14）

现在新增真实密码登录页，客户入口为 /，员工入口为 /staff-login；分别进入 /chat/ 与 /desk/。
聊天和工作台复用 web/chat 中原有组件，未复制聊天协议或为维修业务修改 Go 核心、通用适配器或 CRMEB。
页面沿用原有布局，补齐登录失败、身份核对和失效状态，明确区分密码认证与固定模拟回复。

运行（需要先在 web/chat 安装已有依赖）：

```sh
node web/chat/build-business.mjs
node integrations/business-login/browser-lab.mjs --allow-test-writes
```

启动器仅监听回环随机端口，生成临时客户 lin/zhou 和员工 staff，在终端打印这三个测试登录密码；不打印宿主/坐席密钥，不读 .env。
密码不写入页面、源码或文件，停止后失效。请勿把本地测试登录输出当作生产凭据管理方式。
终端输入 disable customer lin、enable customer lin、disable staff staff 或 enable staff staff 可操作测试账号；输入 stop 结束并清理临时 Go 构建目录。
serveUI 默认关闭，只有显式开启才挂载精确的页面和静态资源路径，响应禁止缓存并设置 CSP。

身份保护：

- /auth/customer/me 与 /auth/staff/me 返回公开的登录版本标识，不返回 Cookie 或认证令牌；每次登录都重新生成，包括同账号重登。
- 页面请求通过 X-Business-Session 锁定该版本，服务端同时核对真实 Cookie；单有版本标识不能认证。无该头的既有非浏览器接入仍保持兼容。
- 跨标签页登录/退出通知、focus/pageshow/visibilitychange 与可见页约3秒轮询负责核对身份；隐藏/离开页面先卸载敏感内容，过期请求不恢复旧 UI。轮询不是即时推送，不保证网络故障时能在3秒内得知禁用。
- 聊天本地待确认消息按登录版本分区，切号或同账号重登不恢复上一次登录的待确认内容；本轮普通未发送草稿也随卸载清空。旧分区不会物理擦除，勿将此视为浏览器取证安全擦除。

真实浏览器已验证：客户登录、发送维修测试问题、固定回复、转人工、员工密码登录、查看/接管/回复、客户看到人工回复、结束会话、新咨询、上一轮只读记录。
两个客户标签之间切换 lin → zhou 后旧页身份更新且未发送草稿清空；退出 zhou 后其他客户页只显示失效提示，员工页不受影响。
由进程内测试控制禁用 lin 与 staff 后，两类页面均隐藏身份、记录和发送控件。本次浏览器没有注入故意延迟网络，迟到响应拒绝由自动测试覆盖，不能混称为浏览器竞态压测。
重新启用账号后旧页面仍保持失效，重新密码登录后才能进入。

最终回归：前端54项与四组接入测试30项，合计84项通过；Go webchat、内嵌 UI、webchat-server 的 race 回归通过，git diff --check 通过。

## 验证边界

“旧页面失效”在本轮指旧页面保存的 Cookie/聊天凭据后续 HTTP 请求被拒绝，不代表已显示的文字能被服务器抹去。
最初密码接入只覆盖 HTTP；本次浏览器范围见上文。请求中退出的迟到响应另有 host-kit 回调测试。
已经被网关接受的写操作可能完成，退出不回滚；私有网关短期凭据仍可能有效到到期，所以不可允许绕过业务代理。

登录数据、消息仓库均为内存，测试结束关闭本轮服务并清理自身临时构建目录。使用已有双来源测试网关的 repair-portal 来源，只开固定回复；不改 CRMEB，不接业务数据库，不读取 .env，不调用模型或 embedding。
使用同步 scrypt、进程内会话、固定总尝试次数限制，只适合本地测试；未提供持久化、分布式撤销、找回密码、MFA、审计、生产限流或容量保证。业务认证调用超时、HTTPS、可信反向代理和会话存储仍需生产集成设计。

## 阅读顺序

1. 本文：范围、结果及未覆盖项。
2. [business.mjs](business.mjs)：密码认证、角色会话、禁用撤销和受限代理。
3. [acceptance.test.mjs](acceptance.test.mjs)：真实 HTTP、随机账号、统一验收器与负向场景。
4. [浏览器入口](../../web/chat/src/business.jsx)、[请求版本隔离](../../web/chat/src/business-session.mjs)、[测试启动器](browser-lab.mjs)。
5. [已有身份模板](../host-kit/identity.example.mjs)和[接入包说明](../host-kit/README.md)：业务认证接入位置。

```sh
node --test web/chat/src/*.test.mjs integrations/business-login/acceptance.test.mjs integrations/host-kit/adapter.test.mjs integrations/standalone-host/host.test.mjs integrations/cross-system/isolation.test.mjs
go test -race ./internal/webchat/... ./cmd/webchat-server
```
