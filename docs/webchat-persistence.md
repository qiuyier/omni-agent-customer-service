# 网页客服持久化与重启恢复

网页客服现在复用已有 PostgreSQL Repository、会话表、消息表和 Tern 迁移，
没有新增另一套会话存储，也没有修改 CRMEB 或引入电商业务逻辑。
两个入口仍使用假回复；持久化不等于已完成真实宿主登录或生产部署。

## 两个启动入口

| 入口 | 存储 | 身份来源 | 用途 |
| --- | --- | --- | --- |
| `cmd/webchat-demo` | 默认内存；显式 `-storage postgres` 可持久化 | 林澈/周宁两个模拟账号 | 本地浏览器演示；绝不能当成真实登录 |
| `cmd/webchat-server` | 必须 PostgreSQL，无内存回退 | 配置的宿主密钥确认来源，宿主后端确认用户 | 接入真实宿主时使用的私有网关；不提供模拟登录或站点代理 |

两者都只监听字面量回环地址。实际宿主接入还需要自己的登录、同域代理、HTTPS、
密钥管理和运维配置。`cmd/server` 原有 `/v1` 路由保持不变。

## 快速验证：专用本机数据库

在项目目录运行：

```sh
bash scripts/webchat_restart_check.sh
```

脚本使用独立 Compose 项目 `omni-agent-webchat-local`，将 PostgreSQL 16.15
映射到 `127.0.0.1:15433`，专用数据库为 `omni_agent_webchat_demo`。
它构建二进制、对该数据库执行已有向上迁移，再验证真实子进程的终止与重启。
需要更换端口时，显式设置 `WEBCHAT_POSTGRES_PORT`。

脚本不读取 `.env`、`DATABASE_URL` 或 `TEST_DATABASE_URL` 作为目标，不连接 CRMEB；
迁移和验收进程不继承模型密钥、代理和 PG 环境覆盖项。脚本不重建或删除已有表、不清表，
只执行本服务已有的向上迁移，并写入本轮虚构验收数据。

验收报告及二进制保留在终端输出的临时目录；PostgreSQL 容器和数据卷也会保留，
便于继续检查。需要停数据库时：

```sh
docker compose -f deploy/webchat/compose.yaml stop postgres
```

不要执行带 `-v` 的删除命令来“重启”；这会删除持久化数据。
也不要把演示库设置成 `TEST_DATABASE_URL`：旧的 Repository 集成测试会清空其测试表。

## 在浏览器里验证

完成上述迁移后，启动持久化演示：

```sh
WEBCHAT_DEMO_DATABASE_URL='postgres://webchat_demo:webchat_demo_local@127.0.0.1:15433/omni_agent_webchat_demo?sslmode=disable' \
  go run ./cmd/webchat-demo -address 127.0.0.1:8092 -storage postgres
```

这里的账号密码是专用本机演示值，不能用于生产。演示命令只接受明确写出的
回环 IP、端口、用户、密码和固定演示库名；拒绝其他数据库以及通过查询参数改写目标。
`-storage memory` 不读取数据库配置。连接失败或未迁移时直接启动失败，不退回内存。

打开 <http://127.0.0.1:8092>：

1. 页面应显示“持久化演示”，选择一个模拟账号并发送消息。
2. `Ctrl+C` 停止服务，再使用同一条命令启动。
3. 重新选择同一模拟账号，聊天记录应恢复；另一账号仍独立。

重启后需要重新选择账号是正常的：**只持久化会话和消息，不持久化模拟登录或浏览器凭证**。
待确认消息仍按原会话存于本标签页的 `sessionStorage`，回到同一账号后可继续原编号重试。
页面不会在重载时自动重发。内存模式仍明确提示重启清空；未知存储状态不显示持久化承诺。

## 私有宿主网关配置

`cmd/webchat-server` 的配置如下；所有密钥由服务端配置注入，不能交给浏览器。

| 配置 | 要求 |
| --- | --- |
| `DATABASE_URL` | 必填；本服务 PostgreSQL 数据库，迁移版本必须匹配 |
| `WEBCHAT_HOST_ID` | 必填、稳定的宿主标识；不能使用保留的 `example-host` / `webchat-demo` |
| `WEBCHAT_HOST_SECRET` | 必填、独立随机密钥，32–128 字节；与原 `/v1` 服务密钥不同 |
| `WEBCHAT_ADDRESS` | 默认 `127.0.0.1:8093`；可用 `-address` 覆盖，拒绝非回环监听 |
| `WEBCHAT_REPLY_MODE` | 默认且目前只允许 `fake`，不读取 AI 模型配置 |
| `STORAGE_DRIVER` | 可以不设置；设置时只能为 `postgres` / `postgresql` |

先通过部署配置或密钥管理器注入这些值，再运行：

```sh
go run ./cmd/webchat-server -check-config
go run ./cmd/webchat-server
```

`-check-config` 只检查配置，不代表数据库可达。正常启动会在监听前检查数据库和 schema，
总初始化预算 10 秒；启动不自动迁移。需要迁移时由部署者显式对已确认目标执行已有
`cmd/migrate`。错误输出不会回显数据库连接串、密钥或驱动原始诊断。

私有网关只提供 `/internal/chat/sessions`、`/chat/v1/messages`、`/healthz`、`/readyz`。
`/readyz` 会检查 PostgreSQL 和 schema；开始停机后拒绝新请求，最多给予 5 秒排空时间，
随后取消剩余请求，待已接受处理退出再关闭连接池。它没有 `/demo/login`、
`/demo/logout`、`/host/chat-session`、聊天页面或 `/v1` 全权限 API。
真实宿主必须实现[网页接入说明](browser-chat.md)中的身份桥接与同域代理。

## 为什么重启后能找到原会话

会话标识依赖稳定的“版本 + 来源系统 + 用户 ID”，不依赖进程 PID、启动时间、
数据库连接池或宿主密钥。服务重启、连接池重建、密钥轮换后，使用同一来源和用户
签发新凭证仍指向原会话。改变 `WEBCHAT_HOST_ID` 则会进入另一套身份空间，
不是迁移或删除历史的方法；用户 ID 也必须稳定。

浏览器凭证只在签发它的进程中有效。重启后旧凭证返回 401，宿主重新交换新凭证。
如果宿主进程没有重启，示例代理收到上游 401 会清理缓存并允许立即重新交换；
如果整个演示进程重启，模拟登录也失效，需要重新选同一账号。

相同来源/用户下，同一个 `event_id` 和原内容重放时，复用已保存的客户消息与回复；
改用不同内容返回 409。不同用户或宿主复用相同事件编号不会串记录。

## 验收覆盖和边界

2026-09-21 本机专用 PostgreSQL 实测通过全部 15 项检查，5 个服务子进程均已退出并回收。
同时通过完整 Go 回归、网页入口相关 race 检查、`go vet ./...` 和 11 项前端测试。

自动化验收会保留不同 PID、停止方式和每项检查结果，且只有旧进程已退出并 `Wait`
完成后才启动下一进程。核心场景使用真实本机 HTTP 代理暂扣已提交回复的响应，
结束原服务进程，再中断客户端连接，之后验证原事件重试。

覆盖：历史消息 ID/顺序不变、旧凭证失效、新凭证指向原会话、密钥轮换、
重复消息与回复不增加、冲突内容拒绝、用户/宿主隔离，以及完整 demo 重启后重新登录恢复。
报告不包含凭证、DSN、客户消息正文或原始子进程日志。

本轮不覆盖“客户消息已提交但回复尚未提交”的精确进程崩溃点；已有应用层测试覆盖
生成失败后重试，但不能把它说成上述 PostgreSQL 崩溃点已实测。数据库进程崩溃恢复、
备份恢复、跨副本会话/限流、真实宿主认证、真实模型、数据保留治理仍未验收。

## 阅读顺序

1. `cmd/webchat-demo/storage.go`：显式演示存储选择与数据库边界。
2. `cmd/webchat-server/config.go`、`main.go`、`lifecycle.go`：宿主配置、组装和生命周期。
3. `internal/bootstrap/repository.go`、`internal/persistence/postgres/repository.go`：复用的持久化实现。
4. `cmd/webchat-demo/main.go`、`host.go`：演示组装、存储状态与身份桥接。
5. `web/chat/src/demo.mjs`、`protocol.mjs`、`app.jsx`：存储状态提示与凭证恢复。
6. `scripts/webchat_restart_check.sh`、`cmd/webchat-restart-check/acceptance.go`：真实进程验收。
