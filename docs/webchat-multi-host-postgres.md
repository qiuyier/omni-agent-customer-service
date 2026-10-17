# 多业务 PostgreSQL 与正式网关重启验收

后续已补齐[双业务真实浏览器端到端验收](webchat-multi-host-browser.md)，将现有页面接到同一正式网关与 PostgreSQL。下文保持本轮服务端进程测试的独立证据范围。

本轮通过 `bash scripts/webchat_restart_check.sh` 复用既有验收器。正式 `cmd/webchat-server` 二进制使用 WEBCHAT_HOSTS_JSON，同时配置两个随机业务来源，连接同一个真实 PostgreSQL，不替换 repository，不使用内存回退。

## 结果

20 项检查全部通过（原 15 项及新增 5 项）；8 个网关/演示子进程全部等待退出。新增结果：

1. 相同客户、坐席、消息事件编号在两个来源下各自保存；人工队列只含本来源，跨来源 history/claim/reply/read/close 均拒绝。
2. 第一个多宿主网关被 SIGKILL 并等待退出；第二个正式进程启动后，两边旧聊天凭证均返回 401。
3. 相同来源/客户重新签发凭证，恢复原会话 ID；消息 ID、内容、顺序与状态完整一致。坐席没有重新 claim 即可发送新回复，原事件重试没有重复保存；来源队列及越权拒绝仍有效。
4. A 关闭咨询，B 的历史完整响应不变。
5. 正常停止后第三个网关进程启动，A 关闭状态、B 人工处理中状态及各自历史仍一致，跨来源操作仍拒绝。

多宿主进程证据：51206（kill）、51207（terminate）、51208（terminate），均 waited=true。完整报告本轮执行时间为 UTC 2026-10-16 16:02:25–16:02:26。

原始脱敏报告（本机临时路径）：`/private/var/folders/c9/f3mfsydd2ns7jx4bp2xlky2h0000gn/T/webchat-restart.Gbhrqb/report/report.json`。报告不含数据库密码、宿主/坐席密钥或聊天令牌；临时目录不是长期存档。

## 数据与环境边界

使用 `deploy/webchat/compose.yaml` 的 PostgreSQL 16.15，回环端口 15433，数据库 omni_agent_webchat_demo，迁移检查结果 schema version 2。没有访问 CRMEB、业务数据库、真实模型、embedding 或知识库。来源含随机运行 ID，每次执行新增独立测试记录，不清空或删除旧数据。

网关子进程使用显式最小环境，不继承 AI、PG、代理或部署凭据。保留原验收器的数据库名称白名单、二进制检查、输出目录防覆盖和子进程回收机制。脚本会启动专用数据库并保留数据卷；完成后可通过 `docker compose -f deploy/webchat/compose.yaml stop postgres` 停止，不使用 down -v。本轮停止数据库容器但保留卷与测试记录。

这是服务端 HTTP/进程验收：重启后由可信宿主重新签发凭证，不是本轮重新做业务密码登录或浏览器验收。原单宿主演示登录重启测试继续运行，但不能算作双业务浏览器证据。本轮没有重启 PostgreSQL 容器以验证数据库崩溃恢复，也不验证 HA、负载、知识/模型或工具授权隔离。

## 复现与阅读

```sh
go test -race ./cmd/webchat-restart-check ./cmd/webchat-server ./internal/webchat/...
bash scripts/webchat_restart_check.sh
```

上述 race 回归及 git diff --check 通过。验收脚本显式在客服测试库执行迁移并写入测试数据，运行前确认专用端口和卷，不将连接地址换成业务生产库。

阅读：本说明 → `cmd/webchat-restart-check/multihost.go` → `process.go` → `scripts/webchat_restart_check.sh`。现有脚本会自动包含新增多宿主检查，不增加生产测试接口或业务功能。
