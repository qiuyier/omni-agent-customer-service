# 数据库分页与未读汇总优化

本轮保持网页接口和交互不变，将 PostgreSQL 坐席历史读取、未读汇总的主要工作放回数据库。没有修改 CRMEB、前端或模型配置，没有新增迁移，数据库版本仍为2。

## 已实现

- PostgreSQL 事务提供可选 MessagePager 能力：按会话内 sequence_no 做 before/after/from 游标查询，不使用 OFFSET，不按时间或消息ID字典排序。
- 每页先查询最多100条公开消息的ID、顺序号和正文长度，再只读取预算内选中的消息正文。保留原先每页100条、128 KiB正文预算、超长消息截断及 has_older/has_newer 语义。
- 锚点必须属于当前会话且为公开消息。网关先校验坐席权限，数据库事务仍持有原有会话锁，权限变更和消息追加沿用现有串行化约束。
- 未读数量与最早未读用 count/min、已读记录反连接直接计算；最新公开消息独立取1条，摘要读取最多161个Unicode字符，供现有160字符展示和截断标记使用。
- 页面仅查询该页客户消息的已读记录；队列摘要不返回已读ID集合。已读写入先校验整个至多100条ID批次，再幂等批量插入，不读取这些消息的正文。
- 内存与未实现新可选接口的自定义仓库保留原有路径。原 DeskAttention 方法保留完整 Seen 集合和最新消息语义，供兼容调用；工作台摘要优先使用有界 ReadDeskAttention。原 ListMessages 仍供其他应用流程使用，不声称所有历史读取都已优化。

## 本地测量

[原始测量与 EXPLAIN ANALYZE](evidence/desk-sql-2026-10-12.txt)。

专用 _test 数据库、PostgreSQL 16、本地容器；单会话10000条客户消息，每条1024字节，全部未读。每组预热1次、串行采样9次。下表为归档复测结果，首轮得到同方向结果但耗时不同。

| 对照路径 | 返回正文记录数 | 返回正文总字节 | 中位耗时 | 平均Go分配字节 |
| --- | ---: | ---: | ---: | ---: |
| 原全历史读取路径 | 10000 | 10240000 | 70.856 ms | 19413698 |
| 数据库最新页 | 100 | 102400 | 3.183 ms | 204344 |
| 全历史读取后汇总对照 | 10000 | 10240000 | 65.366 ms | 19411905 |
| 数据库摘要 | 1 | 161 | 3.438 ms | 4125 |

最新页正文返回量减少99%。这里的 rows/body_bytes 是应用收到的正文行/字节，不是数据库扫描行数，也不含协议、候选ID/长度、已读ID及其他元数据。Go分配由进程 TotalAlloc 差值估计，可能包含连接池等后台活动，不代表堆常驻大小。

全历史汇总对照直接调用旧的 ListMessages + SummarizeAttention，已读集合为空；不是旧版本完整HTTP请求或真实队列延迟。数据库路径包含自己的事务和权限核对成本。没有测HTTP p95、并发吞吐、冷缓存或生产SLA；不能把本地中位数当作上线承诺。原始日志保留最大耗时等数据。

## 查询计划与尚存成本

- 最新页候选查询在本样本使用 messages_sequence_no_key 倒序索引扫描，返回100行，shared hit=17，执行约0.051 ms。复用了现有索引；不保证所有数据分布下优化器选择同一索引。
- 精确未读统计依然需要扫描消息键并与已读记录比较。本样本为10000行顺序扫描与 Hash Anti Join，shared hit=1431，执行约3.218 ms；**并非常数时间、也不是零扫描**。
- 正文不再被整段传回Go，不代表数据库完全不访问包含正文的堆页。特别是精确计数、筛选和TOAST解压仍可能有成本。
- 本轮当时尚未解决队列逐会话查询；后续已补[队列批量摘要](desk-queue-batch.md)，保留本文作为单会话分页阶段的测量记录。高并发和大规模数据仍需独立验收。
- 浏览器仍保留当前会话已加载消息；虚拟列表、容量上限不在本轮。客户聊天页、AI生成所需历史及其他调用方的 ListMessages 未改动。
- 超长单条消息正文在SQL中先截到131072字符，再按原UTF-8字节预算裁剪；普通页正文按字节预算选取，极端多字节超长消息的数据库返回片段可能大于128 KiB，但有字符上限，且不自动标为已读。

## 验收

2026-10-12：

- 真实PostgreSQL：混合公开/系统消息、相同时间戳、逆字典顺序ID、UTF-8正文预算、双向全量遍历、空尾页、非公开/未知游标、跨来源、已读全批拒绝、页内Seen过滤、队列不读取Seen集合、最早未读推进及超长消息通过。
- 网关接线测试：提供 MessagePager 时禁止调用全历史 ListMessages，确认实际走分页；摘要仅请求当前页ID，队列传空集合。
- go test ./...、核心包race测试、真实PostgreSQL race集成测试、go vet ./... 与 git diff --check 通过。万条消息对照测试单独串行运行，未和会重置同库的测试并行。
- 本轮未变更UI，未重复浏览器交互验收；前一轮分页交互证据仍见[历史分页说明](desk-history-pagination.md)。本轮重点是数据库行为与网关接线，不冒充全链路生产性能测试。
- 测试只重置 standalone_rounds_20261009_test 专用库。测试结束停止专用数据库容器，保留数据卷。没有提交或推送Git。

性能技能要求先对照和查看执行计划，本轮保留了上述原始测量，并明确统计扫描和队列查询数的剩余限制。该技能引用的框架参考文件在本机缺失，使用主文档流程与项目实测完成工作。

## 复现及阅读顺序

仅对可丢弃、名称以 _test 结尾的专用库设置 TEST_DATABASE_URL。测试会重置客服表：

```sh
DESK_SQL_MEASURE=1 go test ./internal/persistence/postgres -run TestDeskSQLLongHistoryMeasurement -count=1 -v
go test -race ./internal/persistence/postgres -count=1
```

1. internal/conversation/history_page.go：可选分页/摘要接口和返回结构。
2. internal/persistence/postgres/history_page.go：SQL分页、正文预算和边界。
3. internal/persistence/postgres/attention.go：数据库汇总、页内已读与全批校验。
4. internal/webchat/human.go、sql_paging_test.go：接口选择与兼容路径。
5. internal/persistence/postgres/history_page_test.go：功能回归、可重复测量和查询计划。
