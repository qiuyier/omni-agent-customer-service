# 真实密码业务宿主的标准验收接入（2026-10-21）

## 本轮做了什么

复用既有 `integrations/business-login/business.mjs` 的密码认证服务，把它接入本轮标准验收工具，而非重新实现身份系统。新适配器 `business.mjs` 负责 `/auth/customer/*`、`/auth/staff/*` 和不同角色 Cookie 的映射；CLI 新增静态选项 `business-password`。

业务示例新增可选 `storage`、`replyMode` 配置并传入既有 host-kit 适配器；默认仍为 memory/fake，保留旧示例行为。此次显式配置 PostgreSQL/fake，元数据与真实网关一致，未将 memory 标签伪装成 PostgreSQL。

通用 `core.mjs`、host-kit 身份回调和 Go 客服核心均未修改。报告保存 core 前后相同 SHA-256。

## 验证结果

- 三套验收适配器/通用流程自动测试：29 项通过；已有业务登录及 host-kit 身份回归：14 项通过，共 43 项（Node runner 计数，包含业务测试父项）。
- 专用新 PostgreSQL + 真实 Go 网关 + 原业务密码登录 HTTP 服务：连续两轮，每轮 17 项检查通过，包含客户隔离、混用凭据拒绝、回执/去重、转人工、接管、回复、关闭和历史一致。
- 23 项身份检查通过：错误密码拒绝；演示登录入口不存在；已登录请求指定他人 customer_id 拒绝；客户停用后历史读取、发送、续签和登录均拒绝；员工停用后队列和登录拒绝；重新启用不恢复旧会话，必须重新登录；退出后拒绝；登录过期后客户续签/历史和员工队列均拒绝。
- 过期由注入时钟推进 30 分钟以上验证，并非等待真实 30 分钟。停用仅调用测试进程私有 `setEnabled`，没有公开管理 API。
- 两类预期失败通过：仍有效的旧 Cookie 使验收立即失败；真实发送之后 receipt 方法注入异常，流程停止并注销本轮登录，保护接口复查返回 401。
- 既有回归另覆盖无坐席权限员工、角色 Cookie 混淆、账号切换及浏览器版本标识检查；不将这些旧场景混写成新 PostgreSQL 报告的 23 项。

证据：[business-report-2026-10-21.json](business-report-2026-10-21.json)。整个报告不含密码、Cookie、宿主密钥或连接串。

## 身份与范围

密码由启动器随机生成，仅留在进程内存；业务服务实际执行随机盐+scrypt 验证，服务端根据已验证会话决定用户，不接受聊天请求传入任意用户 ID。业务账号与客服权限仍是不同概念。

这是真实执行密码验证的**本地测试认证服务**，不是连接了外部企业 SSO 或生产 IAM。会话仍为内存存储，不具备生产分布式撤销、MFA、找回密码或持久审计能力；同步 scrypt 和本地 HTTP 也不能直接当作生产配置。

本轮聊天库为专用新数据库，未导入备份，`database_restored: false`；旧凭据通过业务正常退出撤销，不声称验证了第二种业务数据库的恢复/权限版本轮换。内层 `post_restore_http` 只是复用验收流程的既有名称。

全程固定回复，外部模型调用为 0；未新增浏览器交互，报告保留 `browser_verified: false`。停用后的拒绝是后续服务端请求拒绝，不意味着能删除浏览器已经显示过的文字；已接受的写入不因退出回滚。

## 运行与清理

```sh
node --test integrations/restore-acceptance/acceptance.test.mjs integrations/restore-acceptance/standalone.test.mjs integrations/restore-acceptance/business.test.mjs
node --test integrations/business-login/acceptance.test.mjs integrations/host-kit/adapter.test.mjs
node integrations/restore-acceptance/business-check.mjs --allow-isolated-test-writes --report /tmp/NEW_UNIQUE_BUSINESS_REPORT.json
```

需要 Docker、本地 `postgres:16-alpine` 和 Go 工具链；报告文件必须不存在。脚本不读 `.env`，不连接固定数据库或端口，不打印测试密码。创建资源前必须显式允许本轮测试写入。

正常成功或捕获异常均尝试停止自有业务服务和网关，删除本次创建的 `omni-business-acceptance-*` 容器、匿名数据卷及临时二进制。本轮这些清理全部成功，临时测试数据已删除，不影响已有系统。异常清理失败会使报告失败；强制杀进程、断电或 Docker 不可用时仍需人工核查具体资源，不能批量删除未知容器。

已有隔离业务示例可直接使用 `cli.mjs`：配置为 `adapter: "business-password"`、`environment: "isolated-business-lab"`、回环 origin 和超时；`accounts.a/b/staff` 提供 account/password，`old_cookies.a/b` 为 repair_customer Cookie，staff 为 repair_staff Cookie。所有凭据只经 stdin 提供，不放命令参数、Git 或报告。旧 Cookie 必须有先前有效的证据，不能拿随机无效字符串冒充撤销验收。

工具默认只输出离线计划；运行仍需 `--run --confirm-isolated-copy --confirm-public-entry-offline`。确认是操作者承诺，不是自动网络隔离证明。工具从不开放入口，始终返回 `support_may_open: false`。

## 阅读顺序

本文 → `business.mjs`（验收适配）→ `../business-login/business.mjs`（已有密码认证）→ `business-check.mjs`（真实环境检查）→ 脱敏报告。
