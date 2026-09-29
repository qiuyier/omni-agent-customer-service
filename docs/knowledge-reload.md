# 显式知识重载与版本状态

适用于本项目 `webchat-demo` 和 `webchat-server` 可执行进程（Linux/macOS）。
不新增 HTTP 管理路由，也不把重载权限交给浏览器或宿主客户。需要操作系统的进程管理权限。
`console`、通用 `server` 和其他直接使用旧知识构造器的入口未接入此控制方式。

## 使用

先完成知识同步，再向**确认属于目标实例**的实际服务 PID 发送信号：

```sh
kill -USR1 <实际服务PID>   # 在该进程 stderr 输出当前状态
kill -HUP <实际服务PID>    # 异步加载配置的 KNOWLEDGE_FAQ_PATH
```

PID 不能是 `go run` 的父进程或未经核实的其他进程。推荐构建二进制后运行并由进程管理器记录 PID。
信号无同步返回值；kill 成功不等于知识已切换，需检查服务日志中的完成事件和版本。
不支持通过信号修改文件路径、来源、模型或 Embedding 配置，这些仍需重启配置。
同一进程同一时刻只构建一个新版本；重载期间再触发会收到 busy 事件，信号也可能合并，不能当队列使用。

状态字段：version（实际读取 FAQ 字节的 SHA-256）、entries、loaded_at、attempted_at、result、loading。
日志只输出状态，不输出原文、密钥、FAQ 路径或供应商错误。
fake 或未配置知识时报告 disabled，不会因重载信号自动启用知识或模型。

## 版本切换

- 加载、解析、校验，必要时建立完整语义索引后，才在短锁内替换当前检索器。
- FAQ 内容与版本哈希来自同一次读取，适用于同步工具的 current 符号链接。
- 失败时保留当前检索器、版本及成功加载时间，记录 failed 与最近尝试时间。
- `[]` 是显式空知识版本：成功切换后，新检索返回空参考集，仍保留“知识已启用但无依据”的回复约束。
- 缺文件、空文件、null、损坏 JSON、非法条目不等于撤回，不切换版本。
- 初次加载就失败仍拒绝启动，不降级 fake；显式空数组允许网页服务启动。
- 对 semantic 模式，空版本不调用 Embedding；非空版本重新构建索引，可能产生 Embedding 费用。
  构建期间旧索引仍服务请求，峰值内存可能同时包含两版，失败保持旧版。

旧的 `faq.Load`、`faq.New` 保持拒绝空数据的契约；网页重载使用新增的 `LoadVersion`。
因此不能推断所有旧命令现在都支持空 FAQ。

## 处理中请求、撤回和历史

版本固定点是每次 Retrieve 开始读取检索器指针的时刻，不是 HTTP 到达时刻。
此前已进入检索或取得资料的请求可能继续使用旧版本，并在重载成功后才完成回复。
新的检索使用新版本；每次检索不会混合两版。现有回复上下文会拷贝检索结果，不被切换修改。
每个进程独立切换，多实例须逐一确认状态；本功能不是集群同步屏障。

已保存的历史回复和幂等重放不会被重写或重新生成。撤回知识不等于删除会话历史，
历史文字仍可能进入模型上下文；既有提示要求它不能被当成独立事实来源，但这不是模型行为硬保证。
紧急撤回若要求任何旧资料都不能继续输出，仍需停止接收/排空或取消请求，并单独处理历史与交付策略。
本版不承诺“信号发出后绝不再出现旧事实”。

## 与同步工具配合

同步目录的 `current/faq.json` 切换仅改变文件，**不会自动触发重载**。
完成同步后显式发 HUP，再通过状态版本和新请求核验生效。该文件版本哈希可与同步元数据 faq_sha256 对照。
全部撤回仍需同步命令显式 `-allow-empty`，以防意外清空；网页重载此 `[]` 后正常进入无资料状态。
系统不会自动把无资料当成支持某业务，也不会因此开放业务工具。

## 2026-09-29 验证

自动测试覆盖更新、损坏后保留、空数组撤回、恢复、缺失/非法数据、管理信号、状态查询、并发 busy、
取消的候选版本不提交、正在检索的请求固定旧版。原 FAQ 加载契约测试继续通过。

实际构建网页 demo，连接本机 mock Chat Completions，通过真实模拟登录和消息接口发送五条消息。
在同一服务进程发送真实 SIGUSR1/SIGHUP，模型入口收到的参考内容依次为：
`old → new → new（坏文件重载失败）→ []（撤回）→ restored`。
不是只看文件或内部字段：已检查真实消息链路送往模型的 snippets。
测试未调用外部模型、Embedding 或数据库，未修改/重启已有部署服务。

```sh
go test -count=1 ./internal/knowledge/faq ./internal/knowledge/reload ./internal/bootstrap ./cmd/webchat-demo ./cmd/webchat-server
go test -race ./internal/knowledge/reload ./internal/bootstrap ./internal/knowledge/faq
go vet ./internal/knowledge/reload ./internal/bootstrap ./cmd/webchat-demo ./cmd/webchat-server
go build -o /tmp/omni-knowledge-reload-demo ./cmd/webchat-demo
node scripts/knowledge_reload_check.cjs /tmp/omni-knowledge-reload-demo
```

另已完成[CRMEB 真实测试库联合验收](crmeb-sync-reload-acceptance.md)，串联数据库变更、
只读导出、同步、显式重载与模型入口参考校验；该轮临时修改的测试记录已恢复。

阅读顺序：本说明 → `internal/knowledge/reload/manager.go` → `internal/bootstrap/knowledge_reload.go`
→ `internal/bootstrap/webchat_reply.go` → 两处对应测试。
