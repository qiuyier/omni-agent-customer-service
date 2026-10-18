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

阅读：internal/webchat/human_overlap_test.go → internal/webchat/human_test.go → internal/customerservice/reply.go → internal/webchat/messages.go。后续 PostgreSQL 验证见下节。

## PostgreSQL 补充验收（2026-10-18）

新增 internal/persistence/postgres/human_overlap_test.go。使用真实本机 HTTP 服务、PostgreSQL 16.15 和可控模拟模型，三种场景各10轮，启用Go竞态检测，全部通过。真实外部模型请求为0；未修改CRMEB和生产业务逻辑。

测试使用独立容器 omni-handoff-overlap-20261018、仅回环端口15439、数据库 omni_handoff_overlap_test，不复用已有业务数据库。沿用测试仓储的实际数据库名以 _test 结尾检查；测试会清空该测试库的客服表，不可指向业务库。

验证内容：

- 模型由channel阻塞，申请人工、读取历史、接管、人工回复及关闭须在模型释放前完成，每个操作等待上限3秒；若生成期间持有阻塞这些操作的数据库锁，测试失败。
- 另一独立连接池在模型返回前即可读到已提交的原客户消息，以及适用场景中的人工回复。
- 释放模型后原请求返回superseded，reply_message_id为空；历史没有机器人消息。
- 关闭原HTTP服务和原连接池，再建立新的仓储、应用及网关；重新认证同用户，状态和消息历史完全一致。
- 重试原event_id仍返回同一客户message_id，不追加消息或模型调用；未关闭会话可以继续人工回复，已关闭会话拒绝新消息。

复现命令（TEST_DATABASE_URL须显式指向独立可清空的 _test 数据库）：

```sh
go test -race ./internal/persistence/postgres -run '^TestDatabaseDelayedBotAcrossHumanLifecycle$' -count=10 -v
go test -race ./internal/persistence/postgres -count=1
go test ./internal/webchat/... ./internal/customerservice ./cmd/webchat-server
```

以上三项均通过，完整PostgreSQL套件也包含新增场景。首次无数据库环境的执行仅用于编译检查，不计入集成验收；随后显式设置专用测试库连接执行，确认未跳过。

范围：证明这些确定性时序下真实数据库提交与重连读取正确，不代表数据库进程重启/崩溃恢复、分布式多网关竞争或浏览器视觉验收，也未验证取消模型费用和迟到供应商错误提示。专用容器验收后停止，保留容器与测试卷便于复查。下一步可补浏览器层“发送中申请人工”的操作体验验收。
