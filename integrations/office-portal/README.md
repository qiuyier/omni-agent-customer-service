# 办公门户：按文档独立装配验收

此例模拟另一套已有业务系统，不是维修样例改名。原认证服务使用 member/operator、handle/passphrase、MEM:/OPS: 编号及嵌套 ticket 状态；客服只看到适配后的标准身份。不会访问真实办公系统、CRMEB、业务数据库或模型。

## 差异在哪里处理

| 既有门户 | 客服需要 | 转换位置 |
| --- | --- | --- |
| POST /access/member/open、/access/operator/open，handle/passphrase | 已验证业务会话 | legacy.mjs，保留门户登录 |
| principal.number、principal.realm、lifecycle.active、ticket.deadline/key | subjectID、role、enabled、expiresAt、sessionID | portal.mjs 的 sessions.authenticate |
| MEM:731 等不符合标准字符集的编号 | 稳定安全 ID | 完整 SHA-256 映射；不用显示名作为身份 |
| OPS:42 的业务专员账号 | 配置中的 staff-1 | 服务端显式授权映射；OPS:99 登录成功也不能用工作台 |
| /support/identity/member、/support/identity/operator | identity:{id,role,version} | 业务公开身份适配接口，不返回私有 ticket.key |
| /access/member、/access/operator，/support/chat、/support/desk | 登录页与服务页 | office.jsx 配置装配，不复制 HostSessionBoundary/Chat/Desk |

共享组件、host-kit适配器/验收器和Go核心均不修改。legacy.mjs 是独立的测试认证替身，不能把其代码量算作真实项目每次接入必须重写的认证逻辑。portal.mjs 承担字段转换及受限HTTP代理；真实项目应调用已有身份服务。

测试复用已有 cross-system Go 网关的 B 来源，其内部名称仍是 repair-portal；这不是增加第三个生产租户，也不证明不同技术栈或任意SSO自动兼容。字段/登录路径不同的模拟系统，是本轮所证明的范围。

## 运行与清理

```sh
node web/chat/build-office.mjs
node --test integrations/office-portal/acceptance.test.mjs
node integrations/office-portal/browser-lab.mjs --allow-test-writes
```

依赖沿用 web/chat/package.json；浏览器启动器打印回环URL和随机临时测试账号，不打印宿主/坐席密钥。口令不写入页面或仓库；Cookie Path=/，客户/员工分开。终端可输入 disable member lin 或 disable operator staff，enable 对应账号仅恢复登录资格，不复活旧会话；stop 停止服务并清理临时构建目录。消息和登录仅存在内存。

本地认证有容量、登录次数与请求大小限制，但不是生产IAM/限流设计；仅HTTP回环测试所以未设置Secure，生产必须另行设计HTTPS、Cookie策略、可信代理、身份服务超时和分布式撤销。

## 文档装配中发现的要求

1. 对外Host/Origin先校验，再把到私有适配器的Origin设为adapter.origin；透传业务Cookie和聊天Authorization，不能直接信任外部身份声明。
2. 浏览器版本头与Cookie共同验证。本地样例兼容不带版本头的既有非浏览器验收器，但携带空、重复或陈旧版本均拒绝。版本不是认证凭据。verifyHost不发送该头，不能以其通过宣称浏览器版本隔离通过。
3. verifyHost要求登录HTTP200；退出需幂等200/204，因为流程结束和清理阶段会重复退出。
4. Cookie必须覆盖 /host/*、/chat/v1/*、/desk/*，不是只覆盖登录所在的 /access/；真实浏览器检查不可由简化Cookie验收器替代。
5. 现有Chat仍有返回根路径/，因此本门户提供根路径302到成员入口。白标文字与站点导航不是全部可配置，本轮未改通用组件去掩盖这个约定。

## 自动验收覆盖

9项（8子场景+父测试）：原生嵌套认证、伪造字段/角色拒绝、原verifyHost完整人工闭环、稳定ID与公开字段最小化、同账号重登/陈旧/重复版本头、切号/禁用/重启用隔离、未授权专员拒绝、退出幂等、独立页面路径及到期拒绝。
不通过源码生成注入账号，不启用 demoAccounts，也未复用维修startBusiness实现来冒充独立接入。

阅读顺序：本文 → portal.mjs（业务适配）→ office.jsx（页面装配）→ acceptance.test.mjs（验证）→ legacy.mjs（仅本地身份替身）。

## 实际结果（2026-10-14）

- 新场景9项与既有89项合计98项自动测试通过；Go webchat、UI与webchat-server race回归、git diff --check通过。
- 真浏览器在 /access/member 登录，以 MEM:731 显示名进入 /support/chat；专员从 /access/operator 登录进入 /support/desk，完成接管回复、客户可见回复、关闭和新咨询。
- 第二标签切换 MEM:864 后旧标签身份更新、草稿清空；退出后旧成员页隐藏会话；禁用专员后工作台隐藏身份与记录。
- 页面沿用前端设计技能要求的既有样式、表单标签与错误/加载状态，不新增UI依赖或重做共享页面。此轮未做移动端、SSO或慢网络竞态浏览器验收，不能扩大解释为生产全场景可用。
- git diff确认 internal、cmd、Chat、Desk、HostSessionBoundary、host-kit adapter/verify/identity 均未改变；差异限于新门户样例/构建和接入文档。
- 结束后停止临时服务并清理本轮内存数据与临时构建；没有修改CRMEB、读取.env或调用付费模型。
