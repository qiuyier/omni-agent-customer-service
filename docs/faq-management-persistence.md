# FAQ 管理持久化与重启验收

日期：2026-10-30。范围：单业务独立实例、关键词 FAQ 的 PostgreSQL 存储适配；尚未接入生产聊天入口、管理 HTTP、管理员登录或页面。未提交、未发布。

后续进展：显式 managed 检索来源与可组合管理员 HTTP 处理器已实现，见 [接线说明](faq-management-wiring.md)；该处理器未默认挂载，管理员登录和页面仍未实现。下文为本阶段验收记录。

## 实现

`postgres.NewKnowledgeRepository(pool)` 借用调用方连接池，满足 `manage.Repository`：

- `Load` 返回独立快照；没有记录时返回初始空状态，不自动迁移或发布知识。
- `CompareAndSwap` 以 revision 为条件，用一条 INSERT/UPDATE 同时保存草稿、生效指针、已发布版本及操作记录。首次写入冲突也返回 `ErrConflict`；不重试旧请求。
- FAQ 原始字节以 base64 保存在 JSONB 快照中，保持内容版本哈希不变。读取和写入均检查内容哈希、版本引用、事件序号、字段及大小限制；错误不携带 SQL、连接串或 FAQ 原文。
- 保留第一阶段的 32 个已发布版本、1000 条操作记录上限，快照最多 48 MiB。到限拒绝，不截断审计。该全量快照实现面向小规模单实例，不是无限历史或高吞吐管理存储。
- 没有业务 ID/租户路由：一套数据库对应一份管理知识，不能直接用于多业务共享 AI。

## 写入失败与结果未知

服务端明确拒绝语句时，整条快照回滚，旧生效版本及操作记录不变。连接丢失、超时或严重连接错误可能出现在数据库已经提交之后：适配器返回 `ErrOutcomeUnknown`，管理核心原样保留该分类。调用方必须重新读取状态核对 revision 和内容，不能立即自动重复保存或发布。

已用离线“提交成功但应答丢失”模拟验证：先返回 unknown，随后读取发现新 revision；用旧 revision 重试得到 conflict，操作记录不会重复。网络级断线故障注入本轮未做，不将分类测试等同于真实断网验收。

## schema 3 与部署边界

新增 `003_knowledge_management.sql`，在客服专用库添加 `customer_service.knowledge_management`，不改聊天表内容。`schema.CurrentVersion` 升为 3，启动检查同时确认新表存在。迁移仍由 `cmd/migrate` 显式执行，不由请求触发。

**升级时必须先备份并安排维护窗口。** 新构建要求 schema 3；旧 1.0.1 严格要求 schema 2，迁移后不能直接换回旧二进制。down SQL 会删除全部 FAQ 草稿/版本/审计，不应作为普通应用回退手段。本轮仅在新建隔离测试库运行迁移，没有迁移已有参考部署或业务库。

FAQ 的原文件/HUP 链路未替换。后续接线需明确“管理知识”与“文件知识”来源互斥，并完成管理员授权和失效处理；不能仅新增表就宣称后台已可用。

## 本轮真实数据库验收

使用专用 PostgreSQL 16 容器 `omni-faq-persistence-20261030-01`、匿名数据卷、loopback 随机端口和虚构 FAQ。测试入口在迁移或清空前确认数据库名以 `_test` 结尾。

1. `TestKnowledgeDatabaseLifecycle` 在 race 模式重复 5 次通过：保存旧草稿 → 发布 → 保存新草稿但仍检索旧版 → 数据库 CHECK 约束拒绝下一 revision → 核对整个快照完全未变 → 去除故障 → 两个独立连接池同时发布，仅一个成功 → 回退旧版 → 新连接池恢复读取。
2. `TestKnowledgeDatabaseRestart` seed/verify 两个独立 Go 进程之间，实际执行容器 restart。revision 5、两份已发布内容、未发布的新草稿、五条成功操作记录及旧版检索结果均保留；重启前后完整 JSONB 快照摘要均为 `3a225474089fa410dd44348b28722fd8`（仅作为一致性探针，不作为安全签名）。
3. 重启时 Docker 改变随机宿主端口，首次使用旧端口的 verify 连接失败；重新查询实际端口后完整 verify 通过。没有跳过应用侧检索，也没有把连接失败算通过。
4. 对该隔离库执行 `go test -race ./internal/persistence/postgres -count=1` 通过；需额外测量标志或 restart phase 的专项测试保持各自跳过规则。
5. 全量离线 `go test ./...`、`go vet ./...`、10 项回归入口测试通过。默认统一回归通过：网页/宿主 127 项、网关 10 个、knowledge-core 54 个 Go pass 事件；计数含父子测试，不应简单相加当独立场景数。新增 unknown outcome 专项也单独通过。

统一回归已增加 `knowledge-core`，显式 PostgreSQL 模式包含 FAQ 生命周期测试。实际容器重启仍是本轮独立验收，不由默认入口执行；本轮没有重新触发云端 CI。默认报告目录标识：`omni-recovery-check-ps7zUb`。

## 清理与下一步

验收后仅删除上述专用容器及其匿名数据卷，确认容器和卷均不存在；虚构测试数据已不可恢复。未操作其他数据库、CRMEB/PHP74，未调用真实模型。

下一步是管理知识来源与聊天检索的显式接线、管理员登录/HTTP 权限边界；随后才能做页面。现有独立管理权限方案不变，普通客服不会自动成为知识管理员。

阅读：本说明 → `internal/persistence/postgres/knowledge.go` → `knowledge_integration_test.go` → 003 迁移 → `internal/knowledge/manage/service.go` 的 unknown outcome 分支。
