# 客服未读消息与待处理提醒

这是通用网页客服能力，不依赖 CRMEB 或电商业务字段。默认仍需配置人工坐席；没有坐席配置时不开放工作台。

## 本轮实现

- 队列显示每位坐席自己的未读客户消息数及最后一条公开消息摘要（最多160个 Unicode 字符）。机器人、人工回复可以成为摘要，但不计入未读客户消息。
- 未读不会自动切换会话、接管、回复或改变草稿；当前选择有低干扰标记。
- 只有选中会话中、页面可见且消息元素与视口相交的客户消息，才延迟300毫秒批量提交已读确认。这里的“已读”是界面可见性判定，不代表人确实理解了正文，也不要求整个长消息全部进入屏幕。
- 队列和 history 接口只是读取；预取、后台轮询不隐式修改已读记录。确认后通过下一轮读取更新数量，不在前端盲目减掉计数。
- 使用精确消息 ID 集合，不使用“截至现在全部已读”的游标。旧快照确认不会清掉随后到达的消息，其他会话、客户、轮次或坐席的提醒也不会一起清除。
- 确认失败时保留未读，显示重试保存按钮；同一观察周期不无限自动重试。后续重新打开会话或刷新会重新观察，重复确认安全。

## 接口和身份边界

队列及人工动作响应新增 attention_enabled、unread_count、latest_message；history 另返回本次实际返回的客户消息中尚未读取的 unread_message_ids。

私有接口 POST /internal/chat/desk/action：

```json
{"conversation_id":"当前会话ID","action":"read","message_ids":["实际看到的客户消息ID"]}
```

每次1—100个 ID，全批校验后写入；跨会话、非客户消息或不存在的 ID 拒绝，不部分确认。权限沿用同来源等待人工或本人负责的会话。read 不变更会话 revision，不接管、不回复。坐席身份始终来自服务器验证的凭证。

独立 Node 宿主公开 /desk/action 的 read 还要求 expected_agent_id 与当前已认证坐席一致；它只是防止旧标签页误用新 Cookie 的一致性断言，不能用于选择权限。宿主校验后移除此字段，再用服务端坐席凭据转发。原始凭据不进浏览器。

数据层的 DeskAttention 是可选接口；未实现的自定义仓库返回 attention_enabled=false，read 为404。内置内存和 PostgreSQL 仓库均实现。

## 数据库升级

新增迁移 002_desk_read_receipts.sql，应用要求 schema version 2。已配置目标 DATABASE_URL 后，部署新版本前运行：

```sh
go run ./cmd/migrate
```

迁移只新增已读表，不删除现有会话/消息。已有消息对尚无记录的坐席初始为未读。记录主键为来源作用域坐席 ID + 全局唯一消息 ID；消息外键关联具体会话/客户/轮次。权限检查、全批校验和写入共享会话事务锁。重复确认使用 ON CONFLICT DO NOTHING。

PostgreSQL 已读状态可跨刷新、登录和进程重启保留；内存模式仅在当前进程保留。不要对生产库使用集成测试命令：测试会重置专用 _test 数据库。

## 范围限制

这不是生产通知中心：无声音、浏览器推送、短信、排班、转派或 SLA；未读不等于“尚未回复”或“尚未解决”。原有等待人工/由你处理状态承担待处理区分。

队列最多100条，历史每页最多100条且正文总预算128 KiB。后续已补[历史分页与未读定位](desk-history-pagination.md)，可以查看较早的未读消息；未返回或未进入视口的旧消息仍不会被误标已读。

当前仓库按会话读取完整历史后汇总，队列存在逐会话读取；没有做规模化负载验收。不可据此声称适用于大规模生产队列。

## 2026-10-12 验收

[网络/进程证据](../examples/archive/integration/standalone-host/2026-10-11T16-59-38-506Z-ed8d6d34/report.json)使用 UTC 命名。独立本地来源、PostgreSQL、模拟回复，真实模型调用0；不读取 CRMEB 配置、不修改 CRMEB。

浏览器实际完成：

1. 林澈申请人工并留言，乔杉看到1条未读及摘要。
2. 乔杉打开会话，产生精确 read 确认；重新登录后不再显示未读。
3. 温宁登录，同一条消息仍显示1条未读。
4. 重启网关并刷新，温宁仍未读；切回乔杉仍已读。
5. 乔杉接管第一条会话并输入未发送草稿；周宁申请人工并留言，第二条队列自动出现1条未读。第一条仍被选中，草稿原样保留。
6. 未发送这份草稿。网络记录只有一次 read 和一次明确的 claim，没有自动人工回复。

自动测试：前端41项、Node宿主6项；Go全量测试、核心相关包race测试、专用PostgreSQL库race集成测试及 go vet ./... 均通过。覆盖旧快照/新消息、客户/坐席/来源隔离、无效批次不部分写入、幂等确认、视口/隐藏页面/上下文失效、失败不风暴重试及每批上限；PostgreSQL用不同连接池验证持久化。浏览器未逐项模拟所有断网或隐藏策略，不把确定性单测说成人工验收。

临时标签页、测试服务和专用数据库容器均已关闭，保留数据卷及证据。未提交或推送 Git。沿用前端设计技能的现有中性色和低干扰状态样式，无新增依赖。

![第二个会话未读提醒出现，当前选择与草稿保留](../examples/archive/integration/standalone-host/2026-10-11T16-59-38-506Z-ed8d6d34/unread-desk.png)

## 阅读顺序

1. 本文：范围、语义和迁移。
2. internal/conversation/attention.go：权限、消息级确认与内存实现。
3. internal/persistence/postgres/attention.go、migrations/002_desk_read_receipts.sql：事务和持久化。
4. internal/webchat/human.go、attention_test.go：队列摘要与 read 接口。
5. integrations/standalone-host/host.mjs、host.test.mjs：登录身份绑定。
6. web/chat/src/read-receipts.mjs、read-receipts.jsx、read-receipts.test.mjs：可见性与请求生命周期。
7. web/chat/src/desk.jsx：工作台展示。
