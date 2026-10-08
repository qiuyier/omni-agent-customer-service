# 网页客服：显式 AI 回复与知识检索

已支持[显式知识重载与版本状态](knowledge-reload.md)：进程管理信号触发加载，成功整体切换，
失败保留旧版，空数组撤回全部资料；不新增浏览器管理权限，不自动改写处理中或历史回复。

[新场景迁移验收](../examples/archive/knowledge/webchat/mainflow-transfer-v1.md)：
提示和模型不变，维修、酒店、虚构政务咨询六题核心标准及技术检查通过。
这是单轮虚构案例验收，不是生产质量保证；本阶段停止单句调优，后续回到接入能力梳理。

[通用建议边界修正与六场景回归](../examples/archive/knowledge/webchat/mainflow-acceptance-v3.md)：
默认提示和知识约束均要求页面/功能建议有依据，保留有据回答；原六场景本轮技术和回答审阅通过。
这是已知案例单轮回归，不是独立盲测或事实正确性的运行时保证；未启用独立审核器。

[2026-09-28 跨业务主流程重试](../examples/archive/knowledge/webchat/mainflow-acceptance-v2.md)
已完成六个真实模型场景；保存、重放与冲突检查通过。回答审阅五题符合预期，
空资料的积分问题仍补出无依据页面建议，不能标记质量全部通过。

[2026-09-28 跨业务主流程验收](../examples/archive/knowledge/webchat/mainflow-acceptance-v1.md)
在首次供应商调用返回 401 后停止；网页安全返回 503，六题质量验收尚未完成。
不将认证失败计为回答质量结果，待凭据确认后再恢复。

最新[建议边界实验 v4](../examples/archive/knowledge/webchat/grounding-advice-v4.md)仍有输出契约/调用错误，
没有接入本网页的回复链路，也没有放宽覆盖检查。

独立校验器的[20 题冻结验收](../examples/archive/knowledge/webchat/grounding-frozen-validation.md)已完成但未全过，
尚不进入真实业务旁路或在线拦截；本网页回复没有因此增加校验保护。

独立校验器已做[逐细节检查修正](../examples/archive/knowledge/webchat/grounding-judge-v3.md)，
六题针对性回归符合预期；未接入本网页的回复链路，不代表在线回答已经受其保护。

新增[独立依据校验实验](grounding-check.md)，尚未接入网页回复；首次真实审查调用失败，
因此不能视为已经能够阻止资料外推断。

通用事实约束已收紧，并补充[跨业务回归](../examples/archive/knowledge/webchat/grounding-regression.md)：
六次提示回放仍发现未知验证机制及功能存在的推断，不能认为质量问题已解决。

后续已完成 [2026-09-22 真实网页小批量验收](../examples/archive/knowledge/webchat/acceptance.md)：
技术链路通过，但存在资料外流程/入口补充，回答质量需改进。下文离线测试说明保留原范围。

网页入口现在复用已有的 AI provider、FAQ/语义检索和客户服务应用层。
没有复制另一套模型调用、会话或数据库实现，没有修改 CRMEB，也没有新增行业专属工具。
默认仍为 `fake`；只有服务端显式启用 `ai` 后才读取 AI 和知识配置。

## 入口与配置

| 入口 | 开启方式 | 身份与存储边界 |
| --- | --- | --- |
| `cmd/webchat-demo` | `-reply ai` | 模拟账号、仅回环地址；默认内存，可另选专用 PostgreSQL |
| `cmd/webchat-server` | `WEBCHAT_REPLY_MODE=ai` | 可信宿主密钥、必须 PostgreSQL；没有模拟登录和网页 UI |

演示命令不读取 `WEBCHAT_REPLY_MODE`，避免继承该环境变量便意外调用模型。
私有网关未设置该变量也默认 `fake`。fake 模式完全不读取 AI、KNOWLEDGE、EMBEDDING 配置，
不会载入 FAQ 或创建语义索引。存储配置仍按各入口原有规则单独处理。

先在服务端环境或密钥管理器中配置已有参数：

| 参数 | 说明 |
| --- | --- |
| `AI_PROVIDER`、`AI_PROTOCOL` | 复用已有 provider 和协议选项；兼容端点可用 `custom`、`chat_completions` |
| `AI_MODEL`、`AI_BASE_URL` | 由接入方指定；Base URL 是 API 根地址，不是 `/chat/completions` |
| `AI_API_KEY` | 仅服务端注入；也支持已有 provider 专属环境变量；不要写入命令参数或前端 |
| `AI_MAX_CONCURRENT_REPLIES` | 正整数，默认 8；网关另有全局 16、同一身份 1 个进行中请求的限制 |
| `AI_SYSTEM_PROMPT` | 可选，默认沿用通用指引与能力边界；覆盖时由接入方维护这些约束 |
| `KNOWLEDGE_FAQ_PATH` | 可选；相对路径以进程工作目录为准；空值表示只使用对话和系统提示，不检索 |
| `KNOWLEDGE_RETRIEVER` | 默认 `keyword`；显式 `semantic` 时复用已有 embedding 配置 |
| `KNOWLEDGE_RETRIEVAL_TIMEOUT` | 启用知识时必须为正值且不超过 20 秒；默认 keyword 2 秒、semantic 10 秒 |

程序不自动加载 `.env`。不要在日志、截图或浏览器中展示密钥、供应商地址、FAQ 路径。
默认要求非回环模型端点使用 HTTPS；其他模型/Embedding 参数沿用 README 的既有说明。
一套实例使用一套模型与知识配置：**客户会话隔离不等于多租户知识隔离**。

## 本地网页体验

以下命令假设 AI 参数已经在服务端环境中配置。先做无联网配置检查：

```sh
KNOWLEDGE_FAQ_PATH=examples/knowledge/webchat/faq.json \
  go run ./cmd/webchat-demo -reply ai -check-config
```

`-check-config` 不调用模型或 embedding，不读取 FAQ 文件、不连数据库；
成功只代表配置格式与预算合法，不代表文件存在、密钥可用或模型能正确回答。

确认允许向所选供应商发送测试内容并承担费用后，再启动：

```sh
KNOWLEDGE_FAQ_PATH=examples/knowledge/webchat/faq.json \
  go run ./cmd/webchat-demo -reply ai -address 127.0.0.1:8092
```

打开 <http://127.0.0.1:8092>，仍需选择模拟账号。可问“如何重置密码？”或
“演示服务台何时开放？”，再问资料没有覆盖的问题。示例文件全部为虚构的通用服务资料。
页面显示“AI 回复”，但模拟登录仍不是生产认证。模型回复不是查询或操作执行结果。
若改用持久化模式，另按[持久化说明](webchat-persistence.md)配置 `-storage postgres`。

私有宿主网关在原有数据库与宿主配置基础上开启：

```sh
WEBCHAT_REPLY_MODE=ai go run ./cmd/webchat-server -check-config
WEBCHAT_REPLY_MODE=ai go run ./cmd/webchat-server
```

这不会提供宿主的真实登录、同域代理或公开内部接口；仍按[网页接入契约](browser-chat.md)接入。
宿主后端返回给示例前端的安全模式字段为 `reply_mode: "fake" | "ai"`；
不返回模型配置或密钥。未知模式显示“回复模式未确认”，不猜测为 AI。

## 预算、失败与重试

AI 链路从内到外的预算如下，避免代理先于模型中断：

| 阶段 | 上限 |
| --- | --- |
| 知识检索 | 最多 20 秒，独立于生成预算 |
| 模型生成 | 75 秒，与已有 AI 默认一致 |
| 网关请求 | 110 秒，包含接收、检索、生成和保存 |
| 私有 HTTP 写入 / 示例代理等响应头 | 115 秒 |
| 示例代理整个请求 / 公共 HTTP 写入 | 120 / 125 秒 |
| 浏览器发送等待 | AI 150 秒；fake/未知模式 45 秒 |

PostgreSQL 初始化保留原有 10 秒预算。知识初始化另开可取消预算：通常 10 秒，
semantic 则为 `KNOWLEDGE_INDEX_TIMEOUT + 10 秒`；索引内部仍受原索引预算约束。
semantic 启动会把 FAQ 文本送往 embedding 供应商；请求时会发送检索问题，
生成模型会收到对话及匹配资料。keyword 模式不读取 embedding 配置、不向 embedding 发送文本。

无效 FAQ、索引初始化失败或 AI 初始化失败均在监听前报错，不降级 fake。
检索/模型运行失败返回安全的 `503 unavailable`，不向浏览器透传供应商错误内容。
已接收的客户消息保留；使用原 `event_id` 和原文重试可以补齐回复。
已保存回复的重放复用原消息 ID，不再检索或生成；修改同一事件的内容返回 409。

不能据此承诺供应商调用全局 exactly-once：跨进程并发、供应商已处理但响应丢失、
或生成完成但尚未保存时崩溃，仍可能再次生成并产生费用。浏览器不会自动重试未知失败。
切换 fake/ai 不会改写历史，也不会重新生成已保存的旧回复；页面不把旧消息重标为 AI。

服务停机先停止新请求，给已接受请求 5 秒排空时间；之后取消剩余请求，
等待已接受处理退出再关闭数据库连接池。AI 接线没有扩大原 `/v1` 权限或增加业务执行能力。

## 离线验证与阅读顺序

本轮验证全部使用本机 mock Chat Completions / embedding HTTP，不调用真实模型。
测试验证配置、提示与知识的传递、失败恢复和隔离，**不证明真实模型答复质量**。
无命中会传递空参考集和资料不足约束；是否诚实回答仍需另做真实模型验收。

```sh
go test ./internal/bootstrap ./cmd/webchat-demo ./cmd/webchat-server ./internal/webchat
go test -race ./internal/bootstrap ./cmd/webchat-demo ./cmd/webchat-server ./internal/webchat
npm --prefix web/chat test
bash scripts/webchat_restart_check.sh
```

最后一条仍使用 fake 模式，仅回归既有 PostgreSQL 重启/幂等行为，不是 AI+数据库联合崩溃测试。

1. `internal/bootstrap/webchat_reply.go`：显式模式、安全配置与共享组装。
2. `cmd/webchat-server/config.go`、`main.go`、`lifecycle.go`：私有网关组装与预算。
3. `cmd/webchat-demo/storage.go`、`main.go`、`host.go`、`lifecycle.go`：完整示例站点入口。
4. `web/chat/src/protocol.mjs`、`demo.mjs`、`app.jsx`：模式、等待与状态文案。
5. `cmd/webchat-server/ai_test.go`、`cmd/webchat-demo/ai_test.go`：模型与知识实际 HTTP 接线验收。

本步不含真实宿主登录、流式网页回复、业务查询/修改工具、人工通知或生产部署。
