# 已接收、回复失败：状态修正与恢复验收

2026-10-22。本轮发现消息已落库、生成器失败返回 503 时，页面仍只说无法确认响应。现改为失败后执行一次只读回执核验；核验精确匹配当前咨询、原事件、非空消息 ID 且 accepted=true 时，显示“消息已送达，但回复结果尚未确认”，重试按钮改为“重试原消息处理”。

不从失败状态码推断是否接收，也不从接收回执推断回复已生成。回执失败或不匹配时保留原来的不确定状态。身份改变、取消、无效请求、事件冲突不会触发恢复性查询或发送。仅用户点击重试才再次处理原事件，查询不调用生成器。初始化清除临时接收提示状态，避免跨身份沿用。

## 本轮结果

- 网页与接入回归共 133 项通过，含新接收核验测试 2 项。
- Go 网关和客服服务 -race -count=1 全部通过。
- 真实浏览器＋PostgreSQL：模拟回复返回 503 后显示已送达；恢复模拟服务并点击原消息处理，成功返回回复。请求日志中的两次 event_id 完全相同，数据库为 1 条客户消息、1 条关联机器人回复。
- 真实浏览器中暂停模拟回复，用户申请人工，再释放回复：结果 superseded，数据库为 1 条客户消息、0 条机器人回复。
- 独立临时 PostgreSQL 执行 TestDatabaseDelayedBotAcrossHumanLifecycle，-race、-count=3；waiting / active / closed 共 9 个子场景通过，验证迟到回复被拦截、重连与重试不恢复旧回复。
- PostgreSQL 初次运行因库名未以 _test 结尾被保护机制拒绝，改在同一专用容器中新建 reply_boundary_test 后通过；未关闭或绕过保护。

## 范围边界

浏览器覆盖回复失败重试和等待人工；坐席接管、关闭咨询由真实 PostgreSQL＋HTTP 集成测试覆盖，不声称已用坐席页面逐个点击。延迟通过本机模拟生成器显式阻塞/释放，不声称已完成真实供应商超时验收。模型外部调用为 0；页面显示 AI 模式，但此次端点只接本机 mock。

接收提示是本页临时状态，刷新后仍遵循既有回执规则：确认接收即清除待确认缓存，并注明不代表已回复。本轮未增加持久化回复任务列表或跨刷新“重新生成回复”按钮。

共享页面源和四个通用示例 JavaScript 已更新，未调整布局和样式、未修改 CRMEB。本轮没有发布。

## 阅读顺序与证据

1. 本说明。
2. web/chat/src/pending-receipt.mjs：probeAccepted 只读核验，不删除原事件。
3. web/chat/src/app.jsx：失败后核验及接收/回复状态文案。
4. web/chat/src/pending-receipt.test.mjs：匹配、取消和身份变化边界。
5. [浏览器与数据库证据](evidence/reply-failure-2026-10-22.json)。
6. internal/persistence/postgres/human_overlap_test.go：迟到回复人工生命周期隔离。

截图：/tmp/omni-reply-failure-20261022.png、/tmp/omni-reply-handoff-20261022.png。

测试服务与本机 mock 均已停止。新建临时数据库容器及 tmpfs 数据已删除，不可恢复；既有 standalone 验收数据库已停止，其持久卷和本轮虚构记录保留。原始实验报告保留在 examples/archive/integration/standalone-host/2026-10-22T15-45-09-225Z-d1270c6d/report.json；它保留 pending_manual_browser_review，不冒充脚本自动验收，实际 UI 结论见独立证据文件。
