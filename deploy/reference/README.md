# 通用持久化参考部署（本机、固定回复）

面向 macOS/Linux 同机运行：专用 PostgreSQL 容器 + 正式 Go 网关 + Node 业务身份示例/同源适配 + 已构建客户和坐席页面。不依赖 CRMEB，不使用 browser-lab、故障注入代理或每次启动随机生成的身份。

这是一套**可反复启动的本机参考系统，不是生产认证或公网部署**。账号配置保留在本机，登录会话仍在内存，业务进程重启须重新登录。资料/模型本轮不启用；不会因为根 .env 中有密钥而调用模型。没有订单、退款等业务执行功能。

## 首次准备

需要 Go 1.25+、Node.js 24、Docker Compose。所有命令在仓库根运行；首次构建或拉取镜像可能联网，但不是调用模型。页面复用已提交的 fresh-onboarding/public 产物，不需要 npm 开发服务器。

```sh
node deploy/reference/run.mjs init
mkdir -p bin/reference
go build -o bin/reference/webchat-server ./cmd/webchat-server
go build -o bin/reference/migrate ./cmd/migrate
docker compose -f deploy/reference/compose.yaml up -d --wait postgres
node deploy/reference/run.mjs migrate
node deploy/reference/run.mjs check
node deploy/reference/run.mjs start
```

**逐条执行，任何一步失败都应停止排错。** Docker 应使用你的本机测试 context；本轮验收使用 desktop-linux。迁移只针对本部署的客服专用库，必须先确认端口 15435 属于下面的新容器；不要将已有业务数据库改成这个端口来通过检查。

初始化只做一次：`.local/reference/` 权限 0700，config.json 和 postgres-password 权限 0600，已 Git 忽略。独立随机宿主/坐席/数据库密钥及三份随机测试密码保存到本机，不在终端自动输出。目录已存在时拒绝覆盖，部分初始化失败也不会自动重建密钥。请私下查看 config.json 的 accounts 取得登录密码，禁止把完整文件贴到工单或提交仓库。

固定资源归属：

| 资源 | 固定值 |
| --- | --- |
| PostgreSQL 容器 / 数据卷 | omni-reference-local-postgres-1 / omni-reference-local_data |
| 数据库 | omni_reference，宿主回环 127.0.0.1:15435 |
| 私有网关 | 127.0.0.1:8095，仅后端接口 |
| 客户登录 / 聊天 | http://127.0.0.1:8096/ / http://127.0.0.1:8096/chat/ |
| 员工登录 / 工作台 | http://127.0.0.1:8096/staff-login / http://127.0.0.1:8096/desk/ |
| 演示账号 | customer_a、customer_b、staff；随机密码在私密配置 |
| 稳定来源 | reference-local；不要重新生成配置来“清理”状态 |

## 日常使用

1. customer_a 登录客户入口，发一条虚构消息，收到固定回复。
2. 点击“申请人工处理”；staff 登录员工入口，在工作台刷新队列并选择该会话。
3. 明确接管后发送人工回复；客户刷新或等待页面自动更新确认送达。
4. 坐席“结束并关闭会话”；客户看到结束后点击发起新咨询。旧记录不删除，新咨询不继承原坐席。
5. customer_b 只能查看自己的咨询。不要把两个角色共享一份凭据或把密钥传给浏览器。

客户/坐席页面复用现有示例，因此会出现“空白业务”“本机临时账号”等示例文案。“未连接业务数据库”指演示认证没有连接原业务库，不代表客服消息没有落 PostgreSQL。配置里的账号在停止后保留，**并非每次启动随机换账号**。

## 停止与再次启动

在运行 start 的终端按 Ctrl+C：先关闭适配和业务服务，再停止本进程创建的网关。若网关意外退出，启动器也退出，不留下看似正常的业务入口。启动器不自动启动/停止数据库，也不自动迁移。

```sh
# 应用停止后，仅停本部署数据库，保留卷：
docker compose -f deploy/reference/compose.yaml stop postgres
# 再次启动：不运行 init，不重置账号，不删除卷
docker compose -f deploy/reference/compose.yaml up -d --wait postgres
node deploy/reference/run.mjs check
node deploy/reference/run.mjs start
```

业务重启后旧登录失效；回到登录页输入原账号密码，稳定身份会读回 PostgreSQL 历史。保留 `.local/reference` 与数据卷，不能单独换数据库密码或宿主标识。禁止使用 `down -v` 或删库作为普通停止/回退。

## 可重复验收

这些命令是**独立验收客户端**，不负责启动部署。普通日常使用无需运行。仅在自己控制的测试账号无其他操作者时执行；会发送虚构消息、接管、关闭、新建咨询并注销。

```sh
node --test deploy/reference/config.test.mjs
node deploy/reference/verify.mjs --allow-test-writes
# 在运行 start 的终端 Ctrl+C，再按上述步骤停止/重启数据库和应用。
node deploy/reference/verify.mjs --verify-restart
```

第一条写验收复用 host-kit 双客户/坐席闭环，保存重启探针及完整历史快照到私密目录。第二条重新登录、只读比对会话 ID 与完整历史，探针只能有一条；本身不会重启服务，因此不能在未重启时将它当成重启证明。两者会正常登录/退出，虽不修改认证配置，仍有会话状态变化。失败时不清库、不自动重发，不把结果 needs_verification 说成生产通过。

## 排错与接真实系统

- 配置已存在：正常重启无需 init。不要删除配置重建，先核对归属；不提供一键破坏性重置。
- 端口占用：启动器拒绝再次启动，不杀已有进程；确认 8095/8096 的占用者。数据库端口冲突由 Docker 报错。
- 配置/资源失败：核对私密文件权限、密码文件一致性、bin/reference 是否已构建、专用数据库健康及迁移结果。不要打印密钥排错。
- 登录失效：重启会清除示例登录会话，原账号重新登录；不是消息丢失。
- 强制杀死启动器可能留下子网关；使用进程路径、端口确认归属后再停止，禁止按通用进程名批量杀进程。
- 参考端口、账号映射和 fake 模式有意固定，不能通过修改根 .env 切 AI。真实业务要替换演示认证、明确来源与员工映射，按 [接入指南](../../docs/integration-delivery.md)装配；生产还需要 HTTPS、会话存储、备份恢复、网络与值守。

本机回环并不隔离本机其他用户。配置中的明文测试密码、无 Secure 的 HTTP Cookie、内存登录、Docker 数据库发布到回环，均不得被当成公网生产方案。当前不宣称多实例/开机托管/备份恢复或真实 AI 已在这套新部署上验收。

## 2026-10-25 验收结果

- 全新配置与独立数据卷，迁移到 schema 2，配置/资源检查通过。
- 配置/端口保护测试 3 项通过；共享装配的 onboarding、starter、fresh-onboarding 三个测试文件合计 14 项通过，零失败、零跳过。
- HTTP 双客户隔离、回执/幂等、转人工、接管回复、关闭和新咨询通过；真实模型请求 0。
- 应用（网关、业务、适配）与专用 PostgreSQL 实际停止/重启后，同一账号的会话及完整历史精确一致；最终启动入口再次复跑恢复通过。
- 浏览器看到 PostgreSQL 模式；重启后旧登录失效，重新登录看到原探针及一条固定回复。人工闭环本轮为 HTTP 验收，不冒称浏览器重新走完全部人工步骤。
- 第二次并行启动按预期退出 1，原实例不受影响。
- 无 CRMEB 代码/数据库/共享 PHP74 变更，无根 .env 模型配置继承。收尾停止本轮服务，配置、数据库卷保留。

阅读顺序：本文 → run.mjs → config.mjs / compose.yaml → verify.mjs。现有客服核心与 host-kit 无修改。
