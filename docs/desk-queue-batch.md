# 坐席队列批量摘要

2026-10-12：PostgreSQL 工作台队列改为一个 SQL 语句返回授权范围内的会话、精确未读数、最早未读 ID 和最新公开消息预览。没有修改 CRMEB、前端、模型配置或数据库结构，schema 仍为 2。

## 行为与权限

- 可选 DeskQueueReader 接口由 PostgreSQL 实现，网关优先使用；内存和其他未实现接口的仓储沿用旧路径。
- 来源与坐席身份来自服务端认证。SQL 同时筛选当前来源的待接入会话，以及当前坐席自己的处理中会话；已关闭、机器人处理中及其他坐席持有的会话不进入队列。
- 按 updated_at、id 排序，最多 100 条，保持旧接口形状和上限。
- count/min 计算客户消息的未读数和最早未读，按当前坐席排除已读记录，不使用缓存，不读取/修改整套已读 ID。
- 最新消息只考虑 customer、bot、human_agent；SQL 最多返回 161 个字符，网关沿用 160 字符展示与截断标记。空会话和仅系统/工具消息会话无预览。
- 队列筛选、所有会话摘要共享一个 PostgreSQL 语句快照。不再逐会话持有写操作使用的会话锁。若别人接管发生在快照之后，本次结果可能仍包含接管前的会话；下一次读取反映已提交变化，历史和操作仍独立检查权限。不是即时撤销保证，也不是缓存。

## 对照测量

见[完整原始输出及执行计划](evidence/desk-queue-batch-2026-10-12.txt)。

本地 PostgreSQL 16 容器，专用 standalone_rounds_20261009_test 库；128 个会话、12700 条消息，每条 200 个“界”字符。包含两个来源、两个坐席、部分/全已读、已关闭、机器人处理中、空会话、仅系统/仅机器人消息。相同更新时间验证 ID 排序；返回上限为 100。

旧路径为 ListHumanQueue 后逐个 ReadDeskAttention，不是旧的全历史正文路径。每组预热 1 次后串行采样 9 次，计时覆盖仓储读取，不包括完整 HTTP、浏览器和模型。旧路径为比较而统一消息投影字段，其微小 Go 对象构造成本包含在耗时内。

| 路径 | 每次 pgx SQL 调用数 | 中位耗时 | 最大耗时 |
| --- | ---: | ---: | ---: |
| 逐会话摘要，100 会话 | 601 | 250.206 ms | 279.976 ms |
| 批量摘要，100 会话 | 1 | 6.452 ms | 6.875 ms |

SQL 次数由 pgx 查询追踪记录，包括旧路径 BEGIN、COMMIT、锁及各 SELECT；不包括协议内部 prepare 消息，不等于 TCP 包数。两名坐席均验证 601 → 1，结果逐项与旧路径相同。9 次样本不足以声称生产 p95/SLA。

## 执行计划与剩余成本

- 本夹具最新消息使用 messages_conversation_sequence_idx 倒序扫描；最早未读通过 sequence_no 唯一索引查 ID。
- 最新消息的 LATERAL 查询在数据库内执行 100 次；减少的是应用与数据库往返，不是把数据库工作变成常数时间。
- 本次优化器对消息及已读表使用顺序扫描、Hash Anti Join 和聚合；会话表也顺序扫描后排序。整个计划 shared hit=1838，Execution Time=6.734 ms。
- 精确未读仍随消息量和已读量增长，查询规划器也可能扫描队列外数据。权限通过结果范围限制，并不保证只触碰授权行的物理页面。
- 本轮没有添加索引或缓存。更大来源/消息规模、并发轮询、冷缓存、数据库连接池压力仍需单独验收，不能把这次本地数据外推为容量结论。

## 验证与复现

仅对名称以 _test 结尾、可丢弃的专用数据库设置 TEST_DATABASE_URL；下列数据库测试会清空客服测试表，不要连接验收或业务库：

```sh
DESK_QUEUE_MEASURE=1 go test ./internal/persistence/postgres -run TestDatabaseDeskQueueBatch -count=1 -v
go test -race ./internal/persistence/postgres ./internal/webchat ./internal/conversation -count=1
go test ./...
go vet ./...
git diff --check
```

数据库用例覆盖两名坐席逐项对照、来源隔离、他人已接管排除、提交接管后的下一次查询、100 条上限及排序、精确首条未读、全已读/部分已读、空/私有消息会话、多字节预览、人工/机器人预览、空来源结果及无效/取消上下文。网关测试禁止旧方法调用，验证批量分支、可信来源身份、JSON 字段、160 字符截断、空队列、查询错误和未认证拒绝；已有测试继续覆盖兼容路径。

本轮无 UI 变更，未重复浏览器验收。性能技能使本轮保留旧路径基线、查询计数和 EXPLAIN ANALYZE，而不只凭代码推断收益；附属框架参考文件缺失，不影响主流程。

验收结果：全量 Go 测试、真实 PostgreSQL 全部集成测试、上述三个包的真实数据库 race 回归、go vet ./... 与 git diff --check 均通过。测量和会重置数据库的回归串行执行。测试容器已停止，数据卷保留；只重置专用测试库，未修改 CRMEB 或验收库。没有提交或推送 Git。

## 阅读顺序

后续已补[多坐席并发验收](desk-concurrency.md)：包含真实 HTTP 抢接/已读竞争、1/8/16 坐席刷新及连接池等待测量；仍不是生产容量认证。

1. 本文：范围、数据和剩余限制。
2. internal/conversation/desk_queue.go：可选批量读取契约。
3. internal/persistence/postgres/desk_queue.go：范围筛选、快照和 SQL 投影。
4. internal/webchat/human.go：选择批量路径及共用摘要序列化。
5. internal/persistence/postgres/desk_queue_test.go：真实数据库对照、计数、测量与计划。
6. internal/webchat/desk_queue_test.go：接线、认证和响应测试。
