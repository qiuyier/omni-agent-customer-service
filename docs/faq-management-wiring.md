# 管理知识检索与管理员 HTTP 边界

日期：2026-10-30。本轮完成显式检索接线与可组合的管理处理器，**没有管理员登录实现、管理页面或自动挂载的管理服务**。未提交、未发布，未改运行中的配置或数据库。

后续进展：独立 `knowledge-admin` 进程已组合专用管理员登录和管理 API，见 [独立管理入口](knowledge-admin.md)。客户/坐席网关仍不挂载管理路由；管理页面尚未实现。下文保留本阶段边界记录。

## 聊天检索接线

仅 `cmd/webchat-server` 新增 `WEBCHAT_KNOWLEDGE_SOURCE`：

- 未设置或 `file`：保留原 FAQ 文件/HUP 路径，不隐式启用管理知识。
- `managed`：要求单个可信宿主、`WEBCHAT_REPLY_MODE=ai`、关键词检索，且 `KNOWLEDGE_FAQ_PATH` 为空。fake、多宿主、semantic 或同时设置文件来源均拒绝；此功能不会自动启用模型。
- 使用已有 PostgreSQL repository 的连接池，借用 `KnowledgeStore`，生命周期仍由应用控制。不另开业务数据库、不自动迁移。
- 启动前及 `-check-resources` 检查已发布版本可读且校验通过；没有发布过则失败，显式发布的 `[]` 有效。预检不调用模型、不发布知识或创建监听。
- 每次检索读取当前已发布快照；草稿不进入模型上下文。发布/回退后新检索立即使用对应版本，已开始的检索可继续旧版。历史消息和幂等回复不会被重写。
- 管理模式下文件重载不负责发布，HUP 不切换数据库版本；现有文件信号观察器可能报告文件知识 disabled，不是管理存储的状态查询接口。

本轮尚无独立管理进程来建立首个版本。启用 managed 前须由后续受保护管理入口发布；不要手工写表绕过校验。`webchat-demo`、旧通用 server 及其他入口未新增此配置。

## 管理处理器（未默认挂载）

`managehttp.New(repository, authorize, origin)` 由独立管理宿主显式组合。调用方必须提供真实管理员会话核验函数；未配置不能构造处理器。不能从浏览器传来的角色、actor 或自报身份决定权限；测试中的 X-Test-Session 仅存在测试文件，不是生产身份协议。

| 路由 | 方法 / 权限 | 输入 |
| --- | --- | --- |
| `/internal/knowledge/state` | GET / knowledge.read | 无查询参数；返回 revision、active、草稿 JSON、已发布版本 ID、操作记录，不返回内部存储快照 |
| `/internal/knowledge/draft` | POST / knowledge.edit | revision、entries（FAQ 数组） |
| `/internal/knowledge/publish` | POST / knowledge.publish | revision，可选 allow_empty |
| `/internal/knowledge/rollback` | POST / knowledge.publish | revision、version，可选 allow_empty |

普通客户、坐席、host token 不自动取得这些权限。管理路由没有挂载到聊天网关；聊天网关对其返回 404。查看权限不包含编辑权限，编辑权限不包含发布权限；未来管理员角色应明确配置所需权限。

防护：固定 Host/Origin（HTTPS 或 HTTP loopback）、写请求必须同源、拒绝跨站标记/查询参数/未知字段，JSON 请求体上限约 1 MiB，最多 8 个并发处理，10 秒操作 context，无缓存响应。读取结果发出前再次验证身份；写操作在核心提交前再次验证权限与 actor 一致性。

这不是完整认证系统。宿主仍须实现安全 cookie、登录限流、到期/撤销、会话更新、服务器读写超时、HTTPS/反向代理信任边界以及失败操作安全日志。context 不代替 HTTP 服务器的网络读超时；并发上限也不等同于按账号登录限流。撤销检查不是跨认证库与知识数据库的分布式原子事务，不保证取消已经提交的操作。

## 客户端错误处理

- 403：无权限或身份变化，不展示旧管理数据；由实际登录层决定重新登录。
- 409 revision_conflict：重新读取后由管理员确认，不能盲目覆盖。
- 422 empty_confirmation_required：清空知识需要明确确认。
- 503 outcome_unknown_reload_required：数据库可能已提交，先读取 revision/状态核对，不自动重发。普通 storage_unavailable 不回显私密诊断。

## 验证

- 实际聊天网关调用本机 mock Chat Completions，逐次检查发送给模拟模型的参考资料：旧版 → 保存新草稿仍为旧版 → 发布后新版 → 回退后旧版；不只是直接测试 Retrieve。
- 未发布版本拒绝启动；资源预检读取已发布空版成功且 revision 不变，没有模型请求。
- 管理处理器测试覆盖匿名/客户/坐席拒绝、编辑者不能发布、跨站/缺 Origin 拒绝、操作者字段拒绝、陈旧 revision 冲突、撤销身份读取拒绝、读取期间撤销拒绝以及错误分类。
- 核心补充测试：加载状态后权限撤销或 actor 改变，写入前拒绝，不产生 revision。
- 针对性 race 测试、全量离线 Go 测试和 go vet 通过。统一入口加入 managehttp 和 `knowledge-entry`，只使用模拟模型。

本轮没有真实数据库写入或真实模型调用，没有浏览器登录验收，不冒称真实管理员登录已跑通。schema 3 存储证据仍见 [前轮持久化验收](faq-management-persistence.md)。

## 下一步

实现独立管理员登录与管理宿主，接入此处理器，再做页面；已有业务后台可实现同一授权回调复用真实登录。接线前必须保留客户/坐席隔离，不能通过开启聊天网关路由或复用客服 secret 来临时绕过认证。

阅读：本文 → `cmd/webchat-server/managed.go` / `config.go` → `internal/bootstrap/managed_knowledge.go` → `internal/knowledge/managehttp/handler.go` → `managed_test.go` / `handler_test.go`。
