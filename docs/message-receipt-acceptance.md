# 原消息只读回执与待确认恢复

2026-10-19：实现和本地验收完成。此能力属于通用客服协议，不依赖电商订单或 CRMEB。

## 协议与安全边界

`POST /chat/v1/message-receipt`，沿用聊天 Bearer 凭证及业务登录绑定。请求仅包含 `event_id`、`content`；采用 POST 是避免原文进入 URL，操作语义严格只读。

响应包含 `conversation_id`、`event_id`、`accepted`；确认接收时返回 `message_id`。按认证来源、当前用户、当前会话、原事件编号和原内容核对，不凭相同文本猜测。内容冲突返回 409，未知事件返回 accepted=false；未知不意味着正在处理的消息永远不会落库。

查询不创建会话、不收取新消息、不调用模型、不重开已关闭会话；发送进行中和会话关闭后仍可查询。不同用户或来源不能获得原消息编号。

页面恢复及手动刷新时核对缓存；仅明确确认且页面身份/轮次、缓存事件/内容仍一致才移除待确认项。未知、网络错误、存储错误保留缓存，不自动重发。确认送达不代表 AI 或人工已经回复。

host-kit adapter/proxy、business-login 和 standalone 链路已允许新路由。旧接入方须在其受认证的代理白名单加入该路由；不支持时仍安全保留缓存。本次未修改 CRMEB 源码，也未部署到线上。

## 验证结果

- 前端、host-kit、standalone 联合测试 88/88；另三套业务接入测试 28/28。
- Go webchat、webchat-server、customerservice race 测试通过。
- PostgreSQL 全包 race 测试通过，包含模型悬挂期间查询及连接池/网关重建后相同回执。
- 覆盖未认证、跨用户/来源、内容冲突、相同文本不同事件、关闭状态、无模型调用、迟到响应、替换缓存及存储失败。
- 前端构建通过。

浏览器使用独立本地 PostgreSQL 和固定模拟回复。`fail-next-receipt` 在原消息成功落库后向页面返回 503；报告中的发送 status=200 是上游结果，receipt_failure_injected=true 表示页面实际收到注入的 503。

1. 第一条发送后出现待确认；手动刷新清除提示，原消息只存在一次。
2. 第二条在等待人工状态发送并丢失成功回执；坐席接管并关闭后重载客户页，显示已确认送达、closed，待确认和重试项消失。
3. 再次重载仍关闭，无待确认；报告总计两次消息发送、两次只读回执，没有恢复触发的重发，外部模型调用 0。

[最终原始报告](../examples/archive/integration/standalone-host/2026-10-19T07-40-21-715Z-a2fd90d6/report.json)的 passed=null/review_status 保留工具原值；上述结论来自人工浏览器检查和请求核对，不伪装成自动验收标记。

[首次探索报告](../examples/archive/integration/standalone-host/2026-10-19T07-38-46-184Z-f67422bf/report.json)使用断连接注入，触发浏览器底层同事件重试，服务端去重成功，但没有留下待确认状态，因此不计为恢复验收。

![关闭会话后恢复并确认送达](../examples/archive/integration/standalone-host/2026-10-19T07-40-21-715Z-a2fd90d6/receipt-recovered.png)

临时页面、测试进程和本轮数据库容器已停止；数据库及报告保留。

## 阅读顺序

1. 本文：问题、协议、结果与边界。
2. `internal/webchat/receipt.go`、`receipt_test.go`：服务端只读核对。
3. `web/chat/src/pending-receipt.mjs`、测试和 `app.jsx`：安全清缓存及触发时机。
4. `integrations/host-kit/adapter.mjs`、`proxy.mjs`：接入路由。
5. `internal/persistence/postgres/human_overlap_test.go` 与最终浏览器报告：持久化和端到端证据。
