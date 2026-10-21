# 第二种宿主接入验收（2026-10-21）

## 结论

已有的独立宿主示例（没有原生客服能力）通过新的 `standalone-demo` 适配器复用了标准验收流程。`core.mjs` 没有修改，运行前后 SHA-256 相同；仅给 CLI 增加静态适配器选择，并新增 Cookie/接口语义映射。

这证明目前的共同流程能覆盖 CRMEB 和独立宿主两种身份接入方式，不等于任意系统无需适配即可接入。

## 实测范围

- 使用原 `standalone-host/host.mjs`、真实 Go `webchat-server` 和新建专用 PostgreSQL；不是用假 HTTP 服务代替网关。
- 真实 HTTP 连续运行两轮，每轮 17 项检查通过，覆盖新登录、客户隔离、跨登录混用能力凭据拒绝、消息回执和去重、人工接管/回复/关闭、历史重读一致。
- 旧 Cookie 先证明有效；此时运行工具，应在 `old_a_denied` 失败。调用宿主正常退出接口后，两轮旧凭据检查均拒绝。
- 第二种故障发生在真实消息已接受之后：验收适配器的 receipt 方法抛出预设异常。通用流程在 `receipt` 阶段停止；清理注销三个新登录，并通过保护接口重新检查均返回 401。这是适配器异常注入，不是数据库断电或网络故障测试。
- 宿主、Go 网关均关闭，专用临时容器及其匿名数据卷、临时二进制删除。保留脱敏 JSON 证据；没有删除任何原有数据库或用户数据。

原始证据：[standalone-report-2026-10-21.json](standalone-report-2026-10-21.json)。自动适配器/通用测试共 22 项通过，分别为原有 16 项和新增 6 项。

## 必须保留的边界

这里使用示例账号选择登录，不是密码认证、SSO 或生产身份提供方。独立宿主并没有借用 CRMEB 登录、数据库或代码。

本轮创建新数据库，没有导入备份，报告显式记录 `database_restored: false`。它验证的是通用验收复用，而不是第二业务系统的备份恢复或持久身份版本轮换。旧 Cookie 由正常注销撤销，而非数据库版本轮换。内层报告的 `post_restore_http` 是既有流程名称，不能据此推断发生了恢复。

全程 fake 回复，模型调用 0；未新增浏览器操作，`browser_verified` 仍为 false。公网停流仍是部署层责任，工具无开放入口操作。

## 适配差异

| 操作 | CRMEB | 独立宿主示例 |
| --- | --- | --- |
| 新登录 | 密码 → Bearer token | 示例账号选择 → HttpOnly Cookie |
| 客户会话 | `/api/omni-chat/session` | `/host/chat-session` |
| 聊天能力凭据 | `X-Omni-Chat-Token` | `Authorization: Bearer ...` |
| 旧客服身份检查 | `/api/omni-chat/desk/me` 返回 401 | `/desk/queue` 返回 401；不能用可能返回 200/null 的 `/desk/me` |
| 注销成功 | JSON 状态 200 | HTTP 204，并复查保护接口 401 |

不同业务的身份语义留在适配器，共同的客户隔离、消息一致性和人工流程留在 core。

## 重复运行

前提：Docker 可用，本地具有 `postgres:16-alpine` 镜像，Go 工具链及依赖可用。不会读取项目 `.env`，不会连接固定端口或已有数据库，也不传入模型配置。

```sh
node --test integrations/restore-acceptance/acceptance.test.mjs integrations/restore-acceptance/standalone.test.mjs
node integrations/restore-acceptance/standalone-check.mjs --allow-isolated-test-writes --report /tmp/NEW_UNIQUE_REPORT.json
```

输出路径必须不存在，脚本不会覆盖旧报告。默认缺少显式测试写入授权时不创建资源。数据库容器名和端口随机；清理只操作本次成功创建并登记的容器，不按模糊名称批量清理。

脚本捕获正常运行错误并进入 finally 清理；强制杀进程、机器断电、Docker 不可用等情况不能保证清理，仍需操作者检查本轮 `omni-standalone-acceptance-*` 资源后逐一处理，不能删除整个前缀下的未知容器。

## 接入已运行的独立示例

同一个 `cli.mjs` 支持 `adapter: "standalone-demo"`，配置需包含：

- `environment: "isolated-standalone-lab"`、回环 `origin`、`timeout_ms`。
- `accounts.a.account`、`accounts.b.account`、`accounts.staff.account`：示例账号，两个客户必须不同；不需要密码。
- `old_cookies.a/b` 为 `standalone_login=...`，`old_cookies.staff` 为 `standalone_desk=...`。凭据从 stdin 提供，不写进命令参数或报告。

先保留旧 Cookie 当时有效的证据，再在宿主撤销它们。默认 CLI 仅离线输出计划；执行时仍需 `--run --confirm-isolated-copy --confirm-public-entry-offline`。其中隔离确认包含本轮专用示例环境，不代表工具自动验证了后端数据库归属。

阅读顺序：本文 → `standalone.mjs` → `cli.mjs` → `standalone-check.mjs` → 脱敏报告。
