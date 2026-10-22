# 已保存但发送确认丢失：浏览器验收

2026-10-22。本轮使用通用空白业务、虚构账号、专用 PostgreSQL、固定模拟回复。没有修改 CRMEB，没有调用真实模型，没有推进发布。

## 实际结果

| 场景 | 浏览器结果 | PostgreSQL 客户消息 / 关联回复 |
| --- | --- | --- |
| BROWSER-LOSS-RETRY：成功响应丢失后手动重试 | 待确认 → 重试 → 消息已发送 | 1 / 1 |
| BROWSER-LOSS-RELOAD：成功响应丢失后刷新页面 | 查询回执后清除待确认，无需再次发送 | 1 / 1 |
| BROWSER-LOSS-RECEIPT-OUTAGE：确认丢失且回执返回 503 | 刷新后保留待确认并明确说明不会自动重发；恢复回执后点击刷新记录即确认送达 | 1 / 1 |

第三种情况下历史已包含消息和回复，但回执不可用，页面仍保留待确认副本；这是保守的确认状态，不是数据库重复保存。恢复后待确认副本清除。回执只证明接收，不等同于已经回复。

断开连接确实产生了重复的上游发送请求（首次发送记录到两次成功响应被丢弃），因此不能声称网络层只发送一次。服务端最终只保留一条客户消息和一个关联回复。本轮无真实模型，证明的是回复持久化去重，不推广为任意外部模型调用恰好一次。

## 本轮改动

只扩展隔离测试工具：edge 在读取完整上游响应后增加 afterResponse 钩子，测试开关只丢弃 POST messages 的成功 200 响应。查询回执故障由独立模式在转发前返回 503。登录、身份、历史和非成功响应不被误丢弃。

控制通过临时本机文件与终端命令完成，不提供 HTTP 控制端点；仅浏览器验收 worker 装配故障钩子。未修改生产聊天逻辑或页面构建产物。

## 复跑与阅读

启动 node deploy/private-containers/check.mjs --allow-isolated-test-writes --report /tmp/NEW-REPORT.json --browser。

- drop-response：丢弃成功发送响应。
- drop-response-block-receipt：同时让回执查询失败。
- release：恢复正常响应和回执。
- gate-audit：只看路径、状态与故障类型，不输出凭据或消息正文。
- stop：清理本轮专用环境。

阅读顺序：本说明 → [浏览器与数据库证据](evidence/receipt-loss-browser-2026-10-22.json) → browser-gate.mjs / browser-gate.test.mjs → edge.mjs → business-worker.mjs → check.mjs。

[环境验收报告](evidence/receipt-loss-lab-2026-10-22.json) 的 passed 和 cleanup_passed 均为 true；其中 browser_verified=false 是脚本不自行执行 UI 的保守标志，UI 验收单独记录在浏览器证据中。专用容器、网络、镜像、临时凭据和虚构数据已清理，虚构数据不可恢复。

截图：/tmp/omni-receipt-outage-20261022.png（回执故障）与 /tmp/omni-receipt-recovered-20261022.png（恢复后），均为临时本机文件。
