# 正式入口多宿主配置

`cmd/webchat-server` 支持两种互斥配置方式：原有 `WEBCHAT_HOST_ID` + `WEBCHAT_HOST_SECRET`，或 `WEBCHAT_HOSTS_JSON`。后者为 1–32 个对象组成的 JSON 数组，每项只有 `source_id`、`secret`。使用 JSON 时必须清空两个旧字段；空字符串表示未启用 JSON，不是空数组。配置由进程/密钥管理器注入，不自动读取 .env，不接受浏览器输入。

每个 source_id 必须稳定、唯一且非保留演示标识；secret 必须独立随机、32–128 字节、无空白或控制字符且不以 chat_ 开头。不要提交填充后的密钥文件。变更来源 ID 会改变客户/会话映射，不是简单显示名称变更。

`WEBCHAT_AGENTS_JSON` 沿用 `source_id` / `id` / `secret` 格式。坐席必须属于已配置来源，同来源不能重复 ID；不同来源可使用相同坐席 ID，但所有坐席密钥必须互异，且不能与任何宿主密钥相同。总计最多 32 个坐席；未配置时不启用人工授权。

启动前拒绝未知字段、尾随 JSON、过大配置、空数组、超过 32 个宿主、重复来源/密钥、无效标识和错误坐席归属，错误不回显配置内容。`-check-config` 离线检查；`-check-resources` 继续检查 PostgreSQL 资源；正常启动仍须 PostgreSQL/schema 就绪，没有内存回退。

**超过一个宿主只允许 fake 模式，AI 模式直接拒绝。** 模型、知识、工具授权尚未按来源分区。原有单宿主配置和 AI 路径保持兼容；不能用此功能宣称生产多租户 AI 隔离。监听仍限回环，私有网络、业务身份、Cookie、容量配额和数据运维权限仍由部署方负责。

## 验证边界与复现

```sh
go test -race ./cmd/webchat-server ./internal/webchat/...
node --test integrations/cross-system/isolation.test.mjs
```

`multihost_test.go` 通过真实 runCommand → 配置解析 → 启动监听 → 网关链路，在相同客户/坐席/事件 ID 下检查会话与历史分离、来源队列、跨来源五种人工操作拒绝、本来源接管回复，以及关闭 A 不影响 B。测试仅替换 PostgreSQL 依赖为内存仓库；不是实际数据库持久化、真实浏览器或负载验收。原有跨系统 HTTP 测试继续覆盖业务密码登录和切号/退出。

配置实现当轮没有修改客服核心或 CRMEB，没有连接数据库或调用真实模型。后续已完成[独立测试 PostgreSQL 的多来源重启验收](webchat-multi-host-postgres.md)，其中明确区分真实持久化与浏览器证据范围。

2026-10-16：正式入口、webchat 和 UI 的 race 回归通过；跨系统 demo/password HTTP 专项 21 项通过。语法与配置拒绝场景包含原有单宿主 AI 测试；未把注入内存仓库的命令测试记作 PostgreSQL 现场验收。
