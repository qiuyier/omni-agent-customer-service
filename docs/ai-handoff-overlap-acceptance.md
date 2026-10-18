# AI 生成期间转人工：确定性竞态回归

2026-10-18。仅本地可控模型，无真实模型调用；未改CRMEB或业务逻辑。

现有保护是生成前记录会话版本、生成后事务内重新检查版本；转人工、接管与关闭改变版本，迟到回复返回superseded，不写入历史。原human_test.go已有基础接管竞态，本轮补充human_overlap_test.go三种生命周期场景。

## 验证顺序

用channel阻塞模型，确认客户消息已保存，再执行：

- 等待人工：申请人工后才释放模型。
- 人工处理中：申请、接管、坐席回复后才释放模型。
- 已关闭：申请、接管、坐席回复并关闭后才释放模型。

三个场景均检查：坐席在模型阻塞时可读取原问题；迟到请求HTTP 200且outcome=superseded、无reply_message_id；重复发送原事件沿用原message_id；重新获取同用户会话并读取历史，状态和消息完全不变、没有bot消息；模型调用计数始终为1（本地桩）。等待/接管场景可继续人工回复；关闭场景拒绝新消息。

请求仍未结束时，同一客户再次发送或读取历史受busy保护；本轮保留这一现有约束，未把429当作消息已接受。申请人工与坐席操作不受该独占发送阻塞。生成未结束时的历史基线直接读取仓储支持的快照，生成结束后通过HTTP处理器重新读取。

## 命令与结果

```sh
go test -race ./internal/webchat -run 'TestDelayedBotAcrossHumanLifecycle|TestHandoffSuppressesInFlightBot' -count=20
go test ./internal/webchat/... ./internal/customerservice ./cmd/webchat-server
```

两条均通过。新增3个场景各20轮，原基础竞态另20轮；没有发现Go数据竞争。初版测试误以为发送期间允许第二条客户发送及客户历史读取，按实际busy协议修正测试后通过，没有为通过测试放宽生产限制。

## 范围与阅读顺序

这是Go HTTP处理器（httptest）及内存仓储的确定性回归，不是浏览器视觉验收、真实网络/数据库重启或PostgreSQL锁竞争验收；没有证明上游生成会被取消或费用立即停止，只证明已完成转人工后迟到的成功回复不会保存和返回为机器人回复。未覆盖迟到供应商错误/超时提示。

阅读：internal/webchat/human_overlap_test.go → internal/webchat/human_test.go → internal/customerservice/reply.go → internal/webchat/messages.go。下一层可在独立PostgreSQL测试库重跑同一顺序，验证数据库事务行为；不需要真实模型额度。
