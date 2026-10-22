# 分容器浏览器验收 — 2026-10-22

范围：干净通用业务原生密码登录 + 现有聊天页/人工工作台 + mTLS 通道 + Go 网关 + 独立 PostgreSQL。没有修改 CRMEB，没有调用模型。

## 实际结果

- 客户 lin / zhou 分别登录并发送不同标记，切换后只显示自身记录；本轮为同浏览器顺序切换，不宣称两个独立浏览器同时登录验收。
- 客户申请人工，staff 接管并回复，客户刷新可见人工回复。
- 停止专用通道后发送，页面显示无法确认响应并保留原待确认消息；恢复通道后点击重试，消息被接受。
- 重启专用网关和通道后，客户历史、坐席接管状态和回复恢复。
- 坐席关闭，客户刷新后输入禁用；发起新咨询后列表为空，不继承人工状态。
- PostgreSQL 只读核对：A、B、H、R 四条标记各一份，B 与 A/H/R 分属不同会话。
- 入口请求日志覆盖业务登录、会话、消息、人工操作；断连有 503、网关重启后有旧令牌 401 及重新取得会话后的成功请求。
- 页面构建资源不包含本轮宿主/坐席密钥、客户端私钥或 gateway:8443；CSP 限制同源连接，内部路径在入口被拒绝。

证据：[脱敏结果](evidence/private-container-browser-2026-10-22.json)。请求计数包括启动时自动验收及人工浏览器操作，不能解释成纯浏览器抓包；没有采集 HAR，也不宣称完成完整网络外泄审计。

## 实现阅读顺序

1. deploy/private-containers/README.md：运行与验收步骤。
2. deploy/private-containers/edge.mjs：静态资源白名单、正确 MIME、安全头、脱敏日志及固定回环上游。
3. deploy/private-containers/business-worker.mjs：复用原业务、挂载客服、装配 edge。
4. deploy/private-containers/check.mjs：显式交互实验模式及断连/恢复/重启命令。
5. deploy/private-containers/edge.test.mjs：页面边界测试。

打开实验不自动将 browser_verified 置为 true；UI 结论由本记录及独立证据给出。仍不具备生产域名/TLS、证书轮换、正式备份或容量验收结论，不推进发布。
