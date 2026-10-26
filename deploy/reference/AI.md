# 固定参考部署：显式 AI 与业务资料

2026-10-26。默认 `start` / `check` 仍使用 fake，不读取 ai.json 或根 .env。仅 `check-ai` / `start-ai` 读取私密 AI 文件，不改来源、账号、数据库密码或会话映射。与固定回复共用原 PostgreSQL 历史。

## 配置（不自动导入现有密钥）

先完成 [参考部署](README.md)的初始化、构建与数据库迁移。把本目录 `ai.example.json` 作为结构参考，在已受保护的 `.local/reference/` 中创建 `ai.json`，权限必须为 0600，不能是符号链接。不要将填好的文件提交或打印到日志。

六个字段全部必填：

| 字段 | 要求 |
| --- | --- |
| provider | 已支持供应商名称；兼容 API 可用 custom |
| protocol | 本入口仅 chat_completions |
| model | 供应商实际模型名，不自动猜测 |
| baseURL | HTTPS API 根地址，不带用户名、密码、查询参数或片段，不是完整 /chat/completions 路径 |
| apiKey | 私密模型密钥，仅给网关 |
| faqPath | 本机 UTF-8 FAQ JSON 的绝对路径；严格格式由网关检查 |

可复用本机已配置的模型字段，但须明确选取上述字段，不复制整份根 .env。经授权可一次性执行 `node deploy/reference/prepare-ai.mjs --from-primary-env`：只选取五个模型字段，绑定虚构 FAQ，以 0600 创建 ai.json；已存在时拒绝覆盖，不输出密钥、不调用模型。普通启动不会自动执行导入。示例中的空模型/密钥/路径故意不能直接运行。

知识文件结构见 [最小使用指南](../../docs/minimal-use.md#c-配置模型与业务资料)。已有虚构通用资料为 `integrations/standalone-host/ai-human-faq.json`；使用时将其绝对路径写入 faqPath。当前只启用 keyword，不调用 Embedding；`[]` 表示当前无知识，缺文件/坏 JSON 不会悄悄关闭知识或降级 fake。

## 检查、启动与恢复默认

```sh
# 数据库已启动且迁移完成；本步骤不监听或调用模型：
node deploy/reference/run.mjs check-ai
# 仅在确认数据授权与模型费用后，停止旧 start 进程，再运行：
node deploy/reference/run.mjs start-ai
# 结束 AI 接待：Ctrl+C 停止上述进程，再回到固定回复：
node deploy/reference/run.mjs start
```

AI 启动前额外执行资源检查，通过后才打开网关和业务入口。检查成功只证明配置、FAQ 与数据库可用，不验证模型密钥、供应商可用性或回答质量。既有 verify.mjs 是 fake 专用：在 AI 模式会拒绝执行写验收，不能把它作为真实模型验收脚本。

AI 模式会将问题、对话上下文和匹配资料交给供应商。**固定部署没有本轮调用次数硬限额器**；并发限制和等待超时不是费用上限。真实验收必须另行确认额度、测试资料和调用计数方式，不能把此前实验代理的 10 次硬限额误认为本入口也有。失败不自动切换 fake；客户需按原消息回执/事件规则恢复。

原业务认证和人工接待接口保持不变；停止/重启会清除本机示例登录会话，须重新登录，历史仍在。模式切换不重写已有回复。页面登录前不猜测回复模式，聊天页按服务端实际模式显示 AI/模拟/未确认提示。

## 接线与范围

- 网关环境显式设置 AI 和 keyword FAQ，业务适配返回相同 reply_mode。
- mountSupport 支持显式 ai＋postgres（仍仅本机回环）；内存 AI 便捷装配被拒绝。它不能检查上游真正使用的模式，部署方仍须保持两层一致。
- AI 外层同源代理预算 125 秒，内层适配器消息预算 120 秒，网关 110 秒，浏览器沿用 150 秒；fake 代理仍 20 秒。Node requestTimeout 管的是接收请求而非模型生成时间，不应以延长它替代外层代理配置。
- 一个网关实例的一套模型/知识配置；不新增多宿主 AI 隔离、业务执行工具、生产认证或公网部署。

## 验证记录

本轮配置/代理/装配/人工闭环组合 Node 测试 23 项通过，使用本机 HTTP 替身。新增 AI 测试验证权限和坏配置拒绝、AI 元数据一致、上游 503 脱敏且只转发一次、不降级成 fake；未把替身结果称为真实模型质量验收。页面构建通过。

另有前端 protocol 测试 12 项通过；Go 的 internal/bootstrap、internal/knowledge/faq、internal/knowledge/reload、cmd/webchat-server 四个包以 -count=1 通过，涵盖知识预检、无外部调用、无依据/坏文件与模型失败等既有离线用例。语法、差异格式与 31 个本地文档链接检查通过。

后续经授权完成本部署“知识问答 → 人工 → 新咨询 → AI 模式重启恢复”联合验收，见 [2026-10-26 真实验收报告](AI-acceptance.md)。共提交 2 次真实生成请求并收到 2 条回复，没有失败追加重试；不是供应商账单级调用计数证明。配置已保存在忽略提交的私密目录；验收结束停止服务，保留卷和配置，未操作 CRMEB。
