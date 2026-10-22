# 并发恢复：真实页面与 PostgreSQL 验收

2026-10-22，使用通用空白业务、两个虚构客户、本机专用 PostgreSQL / 网关 / TLS / 业务容器。未修改 CRMEB，真实模型调用为 0。浏览器操作使用真实登录、发送、双击重试、退出和切换账号。

## 结果

| 场景 | 页面 / 请求证据 | PostgreSQL |
| --- | --- | --- |
| 断线产生待确认消息，恢复后暂停发送并双击重试 | 只暂停一个 POST messages；放行后发送成功 | BROWSER-CONCURRENT-RETRY 恰好 1 条 |
| 普通机器人状态下后台刷新遇到旧令牌，随后发送 | 发送会取消旧轮询更新，再为发送更新；恢复正常，不把这种情况误称为两者共享 | BROWSER-SHARED-RENEWAL 恰好 1 条 |
| 令牌更新被暂停时退出 | 返回业务登录；请求取消，放行后不发送 | BROWSER-CANCEL-LOGOUT 为 0 条 |
| 令牌更新被暂停时从 A 切换至 B | B 的身份、空历史和空输入正确，不出现 A 的待确认内容 | BROWSER-CANCEL-SWITCH 为 0 条 |
| B 进入等待人工状态，发送和后台刷新同时遭遇 401 | 一个进行中的更新请求后又出现 GET 401，没有新增更新；放行后一次 session 200，随后 GET / POST 均 200，持续刷新恢复 | BROWSER-HUMAN-SHARED 恰好 1 条，所属会话与 A 不同 |

进入发送状态时已有的旧轮询会被取消，此后人工等待状态下的新轮询仍能与正在发送的请求共享更新。日志保留了这一实际顺序，没有把已取消的签发请求算成成功更新。

未发现需要继续修改生产聊天逻辑的问题。本轮新增的是可复用的隔离验收暂停机制与证据。

## 复跑

启动：node deploy/private-containers/check.mjs --allow-isolated-test-writes --report /tmp/NEW-REPORT.json --browser

终端命令：hold-send 暂停发送转发；hold-renewal 暂停聊天令牌签发；release 放行；gate-audit 查看无凭据的暂停日志；restart-gateway 使内存聊天令牌失效但保留业务登录和数据库；records 只读检查 BROWSER- 标记；stop 清理。

暂停控制只在隔离浏览器模式装配，由宿主临时文件控制，无 HTTP 控制入口；身份登录、退出不暂停。关闭请求或 120 秒超时后不转发。开关测试覆盖放行、取消、超时，现有入口白名单与来源校验继续执行。

## 证据与边界

- [浏览器＋数据库核对](evidence/chat-token-concurrency-browser-2026-10-22.json)：本轮 CUA 操作确认，含最后并发阶段的脱敏路径/状态日志与数据库计数。
- [环境验收与清理](evidence/chat-token-concurrency-lab-2026-10-22.json)：passed、cleanup_passed 均为 true。该脚本的 browser_verified 仍为 false，因为脚本只打开环境，不自行执行 UI；浏览器证据单独记录。
- 截图：/tmp/omni-concurrent-browser-20261022.png（临时本机文件）。
- 没有执行跨机器部署、压力测试或发布。结束时专用容器、网络、镜像、临时凭据与虚构数据库均已清理，测试数据不可恢复。

阅读顺序：本说明 → 浏览器证据 → browser-gate.mjs / browser-gate.test.mjs → edge.mjs → business-worker.mjs → check.mjs。
