# 通用客服接入交付指南

本指南是当前执行入口。先跑参考链路，再接目标业务；不需要目标业务已有客服模块。不能把本地样例已通过抄成目标系统已验收，也不能把 fake 回复当成 AI 已可用。

## 1. 接入前交接什么

业务负责人提供客户/员工登录、退出、禁用方式，服务端验证会话的方法，稳定客户/员工编号，哪些员工可任客服，以及聊天/工作台拟挂载路径。只提供接口与权限说明，不在交接表放密码、Cookie 或令牌。

部署负责人提供客服专用 PostgreSQL、备份/迁移责任人、进程管理及可信私有网络方案；每个业务一个稳定 source_id 和独立宿主密钥，每个坐席独立密钥。使用受控配置/密钥管理器传递，不能写入前端或提交仓库。

| 选择 | 适用范围 | 配置 |
| --- | --- | --- |
| 单业务独立实例 | fake/人工；AI 需单独授权和验收 | WEBCHAT_HOST_ID、WEBCHAT_HOST_SECRET |
| 多业务共用实例 | 仅 fake/人工；多宿主 AI 启动直接拒绝 | WEBCHAT_HOSTS_JSON，清空上面两个旧字段 |

两者均需 DATABASE_URL；人工服务另配 WEBCHAT_AGENTS_JSON，来源必须存在、同来源坐席 ID 唯一、所有密钥独立。参见[配置模板](../deploy/webchat-server.env.example)和[多宿主约束](webchat-multi-host.md)。共享聊天记录来源隔离不等于知识、模型、工具授权和资源配额隔离。跨主机/容器私有网络另设计，现有入口限回环，不直接公网暴露。

跨容器接入现有独立实验：[私有分容器说明](../deploy/private-containers/README.md)。保留核心回环监听，以固定目标双向 TLS 通道连接，验证认证拒绝、断连及重启恢复；不等于生产网络或浏览器界面已验收。[本轮脱敏报告](evidence/private-containers-2026-10-21.json)。

分容器实验已补充[本机浏览器验收记录](private-container-browser-acceptance.md)：双客户切换、人工处理、断连重试、网关重启和新咨询已验证；仍不代表生产发布或完整网络审计。

## 2. 从空配置复跑本地参考链路

前置：仓库要求的 Go（见 go.mod）、Node 20+、Docker Compose、OpenSSL 可用；Docker 已启动；15433 未被其他服务占用；允许下载/使用依赖。不要先 source 根目录 .env。脚本会在独立的 omni_agent_webchat_demo 库迁移并新增随机测试数据，不能替换为业务生产库。已有该测试卷时保留旧数据，不删除卷来制造“全新环境”。

在仓库根执行：

```sh
bash scripts/host_onboarding_check.sh --allow-test-writes
```

脚本显式使用空环境运行正式二进制：先确认缺配置失败，再复用现有重启检查，最后分别验证单/多宿主 -check-config 与 -check-resources。子服务使用 fake，不继承模型密钥或 PG 配置。Go/Docker 构建工具保留必要 PATH/HOME 用于运行；不是空机器或第三方盲测。成功看到三类 PASS，重启报告必须 passed=true 且所有检查通过；脚本非零立即停止，不把预期的空配置失败误当成整轮失败。

当前参考报告包含 20 项持久化/进程检查。脚本不会测试目标业务身份，也不会自动打开浏览器。构建文件和脱敏报告位于输出的临时目录，数据库/数据卷保留。若本轮启动了数据库，结束后执行 `docker compose -f deploy/webchat/compose.yaml stop postgres`，不要用 down -v；其他人在使用的数据库不要擅自停止。

需要本地浏览器复核时，在仓库根执行下面命令。构建会覆盖对应样例的 public 资源，先保存自己的改动；普通 npm run build 只构建网关内置演示页，不能替代两个业务构建。

```sh
npm --prefix web/chat ci
node web/chat/build-office.mjs
node web/chat/build-fresh.mjs
docker compose -f deploy/webchat/compose.yaml up -d --wait postgres
node integrations/host-kit/postgres-browser-lab.mjs --allow-test-writes
```

用终端临时账号登录两系统；终端 restart 仅重启网关，stop 关闭本轮服务。完成标准见[完整浏览器验收](webchat-multi-host-browser.md)。这是参考启动器，不是生产服务管理工具。

## 3. 接入目标业务：必须实现的边界

1. 在业务服务端实现 `sessions.authenticate(request, role)`，只从已验证会话取稳定 subjectID、displayName、role、enabled、sessionID、毫秒 expiresAt 和公开 version。未登录/到期/禁用返回无身份，退出和切号立即撤销；version 不是私有 sessionID，必须在登录变化时更新。
2. 复用 [identity.example.mjs](../integrations/host-kit/identity.example.mjs) 和 [startAdapter](../integrations/host-kit/README.md#最小后端装配)，显式配置该业务宿主密钥、授权坐席和真实 storage/replyMode。适配器不会替你创建登录或选择数据库，元数据不等于真实部署。
3. 保留原登录/退出，并提供客户/员工公开身份接口 `{identity:{id,role,version}}`（无身份为 null）。同源挂载[共享代理](../integrations/host-kit/proxy.md)，只转发允许的客服路由；保护非客服路由、Origin、Cookie、正文限制和身份服务超时，管理 adapter 关闭。PHP/Java 可按契约实现等价边界，不是粘贴 Node 文件即可完成。
4. 按[浏览器装配契约](../integrations/host-kit/browser.md)挂载 Chat/Desk/HostSessionBoundary，使用其请求函数与身份作用域；检查 API/根路径冲突。不同业务安排独立 Cookie 名；不同端口不是 Cookie 安全边界。样例文案与导航需要另行适配，不能称为白标成品。
5. 由数据库负责人确认目标库和备份后执行 `go run ./cmd/migrate`；进程管理器安全注入配置，再按顺序运行 `go run ./cmd/webchat-server -check-config`、`-check-resources`、正常启动。检查失败不得回退内存或擅自改 fake 掩盖 AI 配置问题。

## 4. 验收与交接

从[最小接入包](../integrations/host-kit/starter/README.md)复制 JSON 样板到仓库外受控文件，替换目标 origin、路径、登录字段；不要提交凭据。先运行只读 acceptance，再在获授权的测试数据上加 --allow-test-writes。SSO/MFA/JWT 或强制版本头登录不一定受通用 CLI 支持，应补专项客户端，不降低生产校验。

检查 phases/checks：退出 1 为失败；退出 2/needs_verification 表示有未覆盖证据，不能视为通过。目标系统必须完成双客户、未授权员工、切号/退出/禁用、人工闭环、真实浏览器多标签及重启恢复，记录重启的是业务服务、网关还是数据库。已完成的参考证据：[跨系统 HTTP](../integrations/cross-system/README.md)、[PostgreSQL](webchat-multi-host-postgres.md)、[浏览器](webchat-multi-host-browser.md)。它们不是新目标业务的证据。

填写[交接表](../integrations/host-kit/starter/handoff.md)：环境/代码版本、来源与坐席映射、负责人、执行命令、脱敏报告位置、未覆盖项、试运行人群与回退责任。全部符合才能签目标技术验收；生产开放另需 HTTPS、网络、备份恢复、监控与规模证据。

## 5. 排错与回退

| 现象 | 检查/动作 |
| --- | --- |
| 配置拒绝 | 单/多模式是否混用、来源是否重复、密钥独立性、坐席来源；多宿主是否误启 AI |
| 资源检查失败 | 客服库端口、认证、schema；核对库与迁移，不输出 DSN/驱动原始错误 |
| 401 / 身份失效 | 原业务登录、会话撤销/到期、版本及代理绑定；重新认证，不跳过鉴权 |
| 403 / 坐席操作拒绝 | 来源、授权坐席和会话归属；不能通过共享密钥绕过 |
| 重启后无历史 | source_id/客户 ID 是否改变、是否连到相同客服库；新随机测试来源不会自动找到旧记录 |
| 发消息结果不明 | 先查询原消息只读回执；未知/失败保留待确认，不自动重发。手动重试沿用原 event_id 与内容；accepted 不代表已经回复 |
| 浏览器串登录/登录被覆盖 | Cookie 名/域/路径及 BroadcastChannel 配置；端口不隔离 Cookie |

暂停时先由业务方关闭/限制新客服入口，保留原业务登录和其他功能；由进程管理器正常停止所管客服服务。回退应用前核对 schema 兼容性，不默认执行向下迁移。保持原 source_id 与客户映射；轮换密钥需同步宿主/坐席配置。不得以清库、删除卷、换来源 ID 作为回退。恢复后重跑配置/资源检查和目标闭环再开放。

尚不交付：任意业务零适配、生产认证系统、多宿主 AI/知识/工具隔离、业务订单/退款能力、任意容量/高可用保证。本指南整合现有能力，不新增行业样例。

## 本轮指南复跑记录

执行 `bash scripts/host_onboarding_check.sh --allow-test-writes` 返回 0：空环境缺配置正确拒绝，单/多宿主各自配置和资源检查通过，专用 PostgreSQL schema version 2，持久化报告 20 项全部通过、8 个子进程均已回收。报告：`/private/tmp/webchat-restart.umU9iq/report/report.json`（临时文件）。数据库容器已停止，数据保留；bash -n 与 git diff --check 通过。

复核修正了旧“一实例一宿主”说明、补充了多来源交接项，并明确两个业务页面要分别运行 build-office.mjs/build-fresh.mjs。本轮没有重新执行浏览器矩阵或安装前端依赖，浏览器证据引用上一轮记录；不是全新电脑安装、空数据库或未接触项目人员的盲测。目标系统交接表仍保留待验证，不签上线批准。
