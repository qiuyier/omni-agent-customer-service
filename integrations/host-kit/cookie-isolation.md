# 业务登录 Cookie 命名与并行验收

## 配置与职责

Cookie 属于业务登录层，不是客服会话标识。客服核心、共享 adapter 和代理不签发或重命名既有业务登录 Cookie。真实接入应沿用业务身份服务；不要为了接客服复制样例的密码认证服务。

两个本机样例的业务启动函数支持 `cookieNamespace`：

```js
await startPortal({...officeConfig, cookieNamespace: 'office_team_a'});
await startBusiness({...freshConfig, cookieNamespace: 'fresh_team_b'});
```

这是服务端 JavaScript 配置项，不是 URL、请求头或浏览器输入，也没有自动读取同名环境变量。办公默认 `office`，空白业务默认 `fresh`。签发、读取、退出删除均使用 `${cookieNamespace}_member` / `${cookieNamespace}_operator`。可用值匹配 `^[a-z][a-z0-9_]{0,47}$`，不合法时初始化失败。相同样例的多个独立实例也必须配置不同值；不会自动探测其他进程的名称冲突。

保留原有 Path=/、HttpOnly、SameSite=Strict 和 30 分钟期限，退出只删除本系统对应角色的 Cookie。办公默认名称兼容旧版；空白业务由 office 改成 fresh 后需要重新登录，不读取或删除遗留 office Cookie，以免影响办公系统。切换命名空间也需要重新登录，应作为有计划的配置变更。

## 不是安全域隔离

浏览器不会按端口隔离 Cookie。不同名字避免覆盖，但同主机请求仍可能携带所有匹配的 Cookie；命名空间、Path 和 SameSite 都不能阻止同主机其他服务读取或覆盖这些 Cookie。共享代理本轮仍沿用原 Cookie 转发方式。

两个测试服务是可信本机样例，各自只识别配置名称、独立验证随机会话票据及角色，并继续核对客服身份和会话令牌。不可信业务之间必须另行设计独立主机/安全域、HTTPS、host-only Cookie、服务端身份校验和私有适配网络。此参数不是租户授权边界，不代替网关来源隔离或生产部署安全评审。

## 2026-10-16 验收

新增 `office-portal/cookie-namespace.test.mjs` 共 3 项：非法配置拒绝；自定义名称、重复 Cookie 与跨系统改名票据拒绝；两个真实 HTTP 服务共用浏览器式 Cookie 容器，验证四个角色 Cookie 共存、历史分离、错误系统令牌拒绝、切号与退出互不影响。完整相关自动回归共 **142 项通过**，包含原来的 139 项。

真实浏览器同时打开办公与空白业务（同一浏览器、127.0.0.1、不同端口），通过页面密码登录：

- 两边客户同时登录并发送不同标记消息，各页只显示自身记录。
- 两边坐席同时登录，办公刷新仍保留登录。
- 办公客户切到 B 后显示空历史，空白业务仍保留 A 及其消息。
- 办公客户和坐席退出，空白业务客户刷新历史、坐席刷新队列均成功。
- 办公 A 重新登录恢复原消息；空白业务切到 B 并退出客户/坐席后，办公 A 刷新仍显示原消息。

本轮只重测并行登录、消息和切号/退出，不把上一轮的人工接管/关闭、暂扣响应和服务重建算作本轮新增浏览器证据。测试为独立临时 Go 内存网关与固定模拟回复，不连接数据库、CRMEB 或真实模型；不是共用生产网关的多租户验收。临时服务和四个页面均已关闭。

截图：`/tmp/omni-cookie-isolation-20261016.jpg`、`/tmp/omni-cookie-office-isolation-20261016.jpg`（临时证据，不是长期仓库资产）。复现使用 `proxy-browser-lab.mjs --allow-test-writes office` 与 `fresh`，现在可在同一浏览器同时登录；账户由各启动器随机生成，不要保存口令到文档。
