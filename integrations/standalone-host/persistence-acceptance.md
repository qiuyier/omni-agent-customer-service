# 独立后端 PostgreSQL 与重启恢复验收

日期：2026-10-08。本轮已通过，原始[检查与进程记录](../../examples/archive/integration/standalone-host/2026-10-08T10-49-26-697Z-778b773f/report.json)保留42条断言结果（含重复的登录/历史/就绪检查，不是42种独立故障场景）。

## 实际运行结构

- 独立 Node 业务后端：host-worker.mjs 调用原 host.mjs，真实子进程，有自己的内存登录会话。
- 现有 cmd/webchat-server：真实子进程，PostgreSQL、固定回复，无模型调用，不再用上一轮内存网关装配。
- 独立 Docker 项目 omni-standalone-persistence-test，独立数据卷、standalone_chat_acceptance 数据库，只映射127.0.0.1:15434。
- 客户端验收器与本地故障代理：把同一待确认事件写入本轮 pending-event.json，服务端回复完成后断开客户端连接，模拟响应未收到。

未读取 .env/.env.local 或 CRMEB 配置，未使用现有 CRMEB PostgreSQL/MySQL。迁移仅作用于新测试库；未执行清表、删除数据库或删除卷。

## 通过结果

| 场景 | 证据 |
| --- | --- |
| 成功响应丢失 + 网关强制结束 | 代理观察到 replied 后销毁客户端连接；网关被 SIGKILL 后以相同身份配置和数据库重启 |
| 原事件恢复 | 重新取受限凭证后，以磁盘记录中的原 event_id/content 重试，客户消息 ID、回复 ID 与丢失响应完全相同，历史只有一对消息 |
| 网关重启的凭证恢复 | 旧受限凭证返回401，原业务登录可重新交换凭证，conversation_id 不变 |
| 两账号同名事件 | 各自保存不同消息，乙看不到甲的正文，历史不串用 |
| 业务后端强制结束 | Node 进程被 SIGKILL 后重启，旧登录 Cookie 失效；重新登录恢复同一客户映射与原历史 |
| 旧凭证/新登录绑定 | 新登录不能直接复用旧登录的聊天凭证；用户乙也不能搭配用户甲凭证读取记录 |
| 网关短暂不可用 | 停止网关后发送返回502，不误报成功；恢复、重新换凭证后原事件成功，仅新增一对消息 |

验收器保留了5个后端/网关进程的 PID、退出信号和等待回收状态；全部停止。新 PostgreSQL 容器也已停止（另以 Docker 状态只读确认），测试数据卷仍保留。现有 CRMEB 网关与数据库保持运行，未重启。

## 复现

需要本机 Docker Desktop 的 desktop-linux 上下文，15434端口空闲，已有/可获取 postgres:16.15-bookworm 镜像。启动器显式指定该本地上下文，不读取 Compose 环境文件。Go 和 Node.js 版本要求同主项目。

```sh
node integrations/standalone-host/persistence-check.mjs --allow-local-postgres
```

每次运行使用新的验收来源 ID 与报告目录，避免沿用上轮消息；**同一轮内来源 ID、用户映射和宿主凭证在重启前后保持不变**。真实部署的来源 ID 必须长期稳定，不应每次启动随机生成。启动器自动执行现有 migrate 的向上迁移，并在结束时停止新测试容器，保留卷以便核查。

本轮还通过：

```sh
node --test integrations/standalone-host/host.test.mjs
go test ./cmd/webchat-server ./internal/webchat/... ./internal/persistence/postgres ./internal/conversation ./internal/customerservice
git diff --check
```

独立 Node 原内存模式的2个测试仍通过。Go 命令覆盖已有测试；本轮真实 PostgreSQL 证据来自上述独立验收器，不将可能跳过外部数据库的普通测试冒充数据库验收。

## 不应扩大解释的边界

固定回复不能证明真实模型调用/计费恰好一次。本轮证明的是已保存事件/回复的幂等恢复。网络断开点在上游成功响应已经产生之后，不是数据库事务提交中途故障或任意时间断网。

业务后端与网关确实重启，但未做 PostgreSQL 进程崩溃、磁盘损坏、数据库备份恢复或集群故障转移。客户端由测试程序主动执行401后的换凭证、重试与重新登录；未通过真实浏览器验证自动恢复交互，也没有新增服务端自动重试队列。

登录仍是模拟账号选择。身份与受限凭证仍为进程内状态，重启后按预期失效；需要重新登录/交换，而不是宣称凭证本身持久化。本轮无需也未部署到现有常驻服务。
