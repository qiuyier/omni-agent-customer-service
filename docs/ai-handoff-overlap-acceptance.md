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

## 浏览器补充验收（2026-10-18）：安全闭环通过，及时显示有待改进

本节记录修复前的发现；后续修复与复验见文末，不能将旧限制视为当前状态。

启动方式：

```sh
node integrations/standalone-host/browser-lab.mjs --allow-local-postgres --mock-slow-ai --allow-human-desk
```

新增验收专用 --mock-slow-ai，与 --allow-real-ai 互斥。不读取.env，使用随机本地凭证和回环Chat Completions模拟服务，外部请求预算固定为0。页面仍显示AI回复模式，但本模式不代表真实模型质量。模拟请求由终端release-model释放，90秒自动释放作为测试兜底，停止时解除等待；不可用于生产。

本次1次模拟调用，0次真实调用；手动释放发生在约25.9秒。浏览器验证：客户发送中申请人工成功；坐席看到原问题、接管并在模型释放前回复。释放后原发送返回superseded，无机器人回复；客户页退出发送中、显示人工回复，刷新后历史一致。继续发送交给人工，不增加模拟调用；坐席关闭后刷新显示已结束，输入框禁用。

**发现的体验限制，尚未修复：** 原AI请求未结束时，客户页仍显示发送中、暂停自动刷新，坐席已提交的回复暂时不可见。原因是客户历史接口与发送共用busy保护，前端也在sending期间暂停更新。不是消息丢失，也不是AI抢答，但不能宣称“接管后立即可见人工回复”。本轮不擅自解除会话并发保护；后续应优先设计并回归验证安全的只读刷新与待确认消息处理，而不是继续扩展业务功能。

本轮只改变本地验收工具与测试，不修改生产会话或页面逻辑。Node参数测试3项通过；业务后端与参数合计9项通过。报告保留自动pending_manual_browser_review，本节是人工审阅结论，不将其改写成无条件全通过。

证据：[report.json](../examples/archive/integration/standalone-host/2026-10-18T05-42-05-595Z-c1d7a2a7/report.json)。测试网关、后端、标签页和专用数据库容器已停止，保留数据卷与证据。

![释放迟到模型并关闭后的客户页](../examples/archive/integration/standalone-host/2026-10-18T05-42-05-595Z-c1d7a2a7/browser-overlap-closed.png)

## 及时显示修复与复验（2026-10-18）

已修复人工回复必须等原AI请求结束才可见的问题：

- sessions.go只将当前历史GET设为非独占读取，仍执行鉴权、会话归属、限流及全局并发上限；发送与新咨询仍互斥。回归明确断言历史读取后第二次发送仍429。
- app.jsx在发送中且已确认waiting_human/human_active/closed时恢复只读轮询（可见页约5秒），保留身份、会话及操作版本校验；原发送回执到达时使旧轮询失效。
- 页面说明人工流程已启动、原请求正在收尾，暂不允许再次发送。不以消息文本相同推断已确认；待确认缓存只有正式回执才清除。轮询期间隐藏重复的临时发送气泡，不删除缓存。
- 首轮浏览器复验遇到75秒模型超时，发现已接管情况下仍返回503、残留待确认提示。messages.go现会在生成失败后重新确认会话状态：已进入人工或已关闭，返回原客户message_id及superseded；状态读取失败或仍为bot_active时不吞掉原错误。新增人工处理中/已关闭的迟到失败回归。

最终浏览器复验使用本地慢模型：在模型未完成、原发送无回执时，客户页已显示人工回复；约55.6秒后手动release-model，原请求HTTP200/superseded、没有bot消息或待确认残留；关闭后刷新状态和两条历史消息一致。

证据：[最终报告](../examples/archive/integration/standalone-host/2026-10-18T09-53-12-561Z-3b6c7bd3/report.json)，[发现超时边界的首轮报告](../examples/archive/integration/standalone-host/2026-10-18T05-50-15-006Z-27b21b70/report.json)。两轮共2次本地模拟调用，真实外部调用0次。最终成功迟到回执做了浏览器验证；迟到失败修复用确定性自动测试覆盖，未再次在浏览器等待75秒超时。

验证：前端61项通过；完整webchat竞态检测通过；3个成功迟到场景及2个失败迟到场景各20轮通过；相关Go回归通过；独立PostgreSQL完整仓储竞态套件通过（新增生成中HTTP历史读取及二次发送拒绝断言）。浏览器资源已重新构建。所有本轮服务、标签页与测试数据库容器已停止，证据与测试卷保留。

边界：此修复是“读不必等待写完成”，不是取消上游生成，也没有放开同会话并发发送。人工消息约按5秒轮询可见，不是实时推送；隐藏页面暂停更新。未修改CRMEB或引入电商规则。

阅读顺序：web/chat/src/handoff-read.mjs → web/chat/src/app.jsx → internal/webchat/sessions.go → internal/webchat/messages.go → internal/webchat/human_overlap_test.go → internal/persistence/postgres/human_overlap_test.go。

![AI仍在等待时客户已看到人工回复](../examples/archive/integration/standalone-host/2026-10-18T09-53-12-561Z-3b6c7bd3/live-human-reply.png)

## 账号切换、刷新和关闭边界验收（2026-10-18）

新增 web/chat/src/handoff-identity.test.mjs 共7项：发送及历史同时悬挂，在clear并重新打开B后，旧200/401/503分别按发送先返回、历史先返回两种顺序完成；全部拒绝为AbortError，不恢复A凭证、不因旧401续期、不重发。另验证刷新只获取会话及历史，恢复closed状态，A的待确认缓存不进入B且不自动发送。前端完整测试68/68通过。本轮未修改生产代码。

浏览器使用两个模拟客户、一个坐席及本地慢模型：

1. A发送唯一标记问题，生成中申请人工，坐席读取并接管。
2. 另一首页标签切换到B，原客户页自动变成B的空会话；坐席随后回复并关闭A。
3. 本次模型先达到75秒超时（mock记录499），release-model指令到达时请求已结束；业务后端因登录已改变，旧消息请求返回401。没有将其解释为正常模型成功回执；旧成功/失败返回的完整组合由上述确定性测试覆盖。
4. B刷新仍为空，不含A问题、人工回复或待确认消息；切回A后两条持久化消息、closed状态和原待确认缓存恢复，刷新不重复发送，旧响应不重新开启会话。
5. A显式发起新咨询得到新的空会话，不继承人工状态、历史或旧待确认内容；没有触发模型或重发原消息。

报告只有1条客户消息POST、1次本地模型请求、0次外部调用。关闭会话里额外虚线“等待确认”是客户端原事件缓存，不是第二条数据库消息；切号已丢弃旧回执，不能以相同文本猜测确认成功。关闭状态下重试按钮禁用，原缓存保留在旧轮次；本轮未实现事件级只读回执查询或自动清理该缓存。

2026-10-19 后续已为支持新路由的通用接入链路实现事件级只读回执，见[回执恢复验收](message-receipt-acceptance.md)。上面的记录保留为当时结果，不代表当前限制。

证据：[报告](../examples/archive/integration/standalone-host/2026-10-18T10-03-52-832Z-06141610/report.json)。真实密码登录、企业IAM、多浏览器进程以及刷新瞬间所有网络时序不在此轮浏览器覆盖范围。自动报告仍保留pending_manual_browser_review，本节为人工审阅记录。所有本轮进程、标签页及测试数据库容器已停止，测试卷保留。

![B刷新后没有A的数据](../examples/archive/integration/standalone-host/2026-10-18T10-03-52-832Z-06141610/account-b-empty.png)

![A刷新后仍关闭，原待确认缓存保留](../examples/archive/integration/standalone-host/2026-10-18T10-03-52-832Z-06141610/account-a-closed.png)
