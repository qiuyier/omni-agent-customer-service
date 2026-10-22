# 令牌恢复并发验收（2026-10-22）

## 发现与修复

上一轮修复允许同一业务身份更新聊天令牌。本轮发现并发操作仍各自签发令牌，而且较晚返回的旧令牌 401 会再次更新；clear 原来只通过代次检查丢弃结果，没有主动取消更新请求。

现在每个 createChatAPI 实例共享一次进行中的更新。较晚返回的旧令牌 401 复用已更新令牌，不再次签发。每个等待者独立取消：取消历史查询不影响仍在等待的发送；最后一个等待者离开或 clear 时中止更新。即使底层传输忽略中止并迟到返回，也不能恢复旧能力或覆盖新账号。共享失败不重放消息，后续显式操作可重新尝试。

不跨标签页合并，不放宽身份核验，不合并不同用户或咨询轮次。第二次请求仍失败时不会循环重试。

## 验证

- 新增 10 项可控异步时序测试：并发共享、迟到 401、单个取消、全部取消、clear 取消、轮次改变、换号后迟到响应、共享签发拒绝、重复事件正文保持、失败后释放更新槽。
- 网页逻辑测试共 102 项全部通过；既有身份核验组合测试仍包含退出、换号、权限拒绝及服务异常。
- 分容器与两个业务接入的 24 项 Node 集成测试通过。
- go test -race -count=1 ./internal/webchat ./internal/customerservice 通过。包含 32 路重复消息接收去重，以及并发更新令牌不能绕过忙碌保护。
- 页面 send 在第一次 await 前同步设置 busyRef，连续点击被拦截，无需修改。协议级重复请求保留相同 event_id 和正文，服务端继续承担最终去重。
- 重建通用演示、fresh-onboarding、business-login、office-portal 的 JavaScript，未更改样式或 CRMEB。

本轮使用确定性延迟控制和本地集成测试，没有声称完成浏览器连点实测或新一轮 PostgreSQL 实测；没有调用真实模型或推进发布。测试网络请求使用模拟回复。

## 阅读顺序

1. 本说明。
2. web/chat/src/protocol.mjs：共享更新、独立取消、代次隔离和迟到响应处理。
3. web/chat/src/renewal-concurrency.test.mjs：新增可控并发用例。
4. web/chat/src/chat-token-recovery.test.mjs：上一轮身份边界回归。
5. internal/customerservice/service_test.go 与 internal/webchat/gateway_test.go：服务端去重与忙碌保护。
