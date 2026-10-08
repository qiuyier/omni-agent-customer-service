# CRMEB 本机持久化测试部署

最新阶段接入[可配置知识库](../../integrations/crmeb/Knowledge-acceptance.md)。
保留知识模式请使用 `node deploy/crmeb/set-mode.mjs ai-knowledge`；`ai` 明确表示不加载知识。

当前已完成 [AI 接入验收](../../integrations/crmeb/AI-acceptance.md)，实例显式启用 AI。
下文基础 compose 的 fake 说明仍是默认行为；日常切换请使用 `node deploy/crmeb/set-mode.mjs ai|fake`，
避免只运行基础 compose 将实例切回 fake，却没有同步桥接的模式提示。

2026-09-30。使用通用 `cmd/webchat-server` + PostgreSQL；CRMEB 仅提供身份桥接和独立 H5 入口。
不依赖商城原人工客服模块，不修改商城 MySQL 表。此目录是本机部署实例，不是把通用核心改成电商专用。

## 当前状态与边界

- 入口：`http://demo.crmeb.com/omni-chat-test/`，先使用商城账号登录。
- Compose 项目 `omni-crmeb-webchat-test`，固定测试回复，零模型调用、零订单查询。
- PostgreSQL 数据卷 `omni-crmeb-webchat-test_chat-data`；普通 stop/start/restart 保留历史。
- 网关与数据库共享 `php74` 网络命名空间，仅绑定回环 `8093`、`15434`；无新增发布端口。
- 网关宿主来源 ID 固定为 `crmeb-local-test`。来源 ID/用户映射影响会话身份，不可随意修改。
- 短期客服凭证仍为进程内状态。网关重启后旧凭证 401，桥接清理该登录的旧绑定；浏览器续签一次，恢复同一会话。
- 不改变原人工客服入口。新的持久化历史从本次部署开始，前一轮已停止的内存记录无法恢复。
- 本机 HTTP、单实例、fake 模式的测试部署，不是生产上线。未验证主机重启自恢复、备份恢复、高可用或真实模型回答。

## 初始化与启动

以下命令从本仓库根目录执行。先安装 `integrations/crmeb/README.md` 的四个桥接文件与 H5 文件，确认 php74 正在运行。
`prepare.mjs` 当前明确针对本机 CRMEB 路径，迁移到其他主机前必须检查路径、域名、端口和架构。

```sh
mkdir -p deploy/crmeb/bin
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o deploy/crmeb/bin/webchat-server ./cmd/webchat-server
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o deploy/crmeb/bin/migrate ./cmd/migrate
# 仅首次执行；已有私密配置时拒绝覆盖，正常重启不要再运行。
node deploy/crmeb/prepare.mjs
docker compose -f deploy/crmeb/compose.yaml config --quiet
docker compose -f deploy/crmeb/compose.yaml build gateway
docker compose -f deploy/crmeb/compose.yaml up -d postgres
docker compose -f deploy/crmeb/compose.yaml --profile tools run --rm migrate
docker compose -f deploy/crmeb/compose.yaml up -d gateway
docker exec php74 php -r '$r=@file_get_contents("http://127.0.0.1:8093/readyz");echo $r===false?"not ready\n":$r;exit($r===false?1:0);'
```

本机为 arm64；其他 CPU 必须调整 GOARCH。scratch 镜像从 Alpine 3.22 复制公开 CA 根证书，
支持 HTTPS 模型请求且不关闭证书验证。镜像不包含模型密钥；AI 必须使用显式 override 与私密运行配置。
首次就绪后，才让 FPM 读取私密配置、开放独立桥接：

```sh
docker exec php74 sh -c 'chgrp www-data /www/CRMEB/crmeb/runtime/omni-chat-deploy /www/CRMEB/crmeb/runtime/omni-chat-deploy/config.json && chmod 750 /www/CRMEB/crmeb/runtime/omni-chat-deploy && chmod 640 /www/CRMEB/crmeb/runtime/omni-chat-deploy/config.json'
```

密钥只存在 `deploy/crmeb/.env` 和商城 `runtime/omni-chat-deploy/config.json`，不要打印、提交或发给浏览器。
`.dockerignore` 仅允许构建所需文件，排除私密配置。`.env` 已被 Git 忽略。
私密文件、数据库卷需成套保管；丢失配置时不要通过重新生成密码来覆盖既有数据库实例。
FPM 还必须可写 `runtime/omni-chat-locks`，只授权该专用锁目录，不放宽整个 runtime 权限。

## 日常操作

```sh
docker compose -f deploy/crmeb/compose.yaml ps
docker compose -f deploy/crmeb/compose.yaml stop gateway postgres
docker compose -f deploy/crmeb/compose.yaml up -d postgres gateway
docker compose -f deploy/crmeb/compose.yaml restart gateway
```

不要使用 `down -v` 或删除数据卷。手动 stop 后 `unless-stopped` 不保证自动启动，需要显式 up。
PostgreSQL 有健康检查；网关运行时就绪状态须使用 `/readyz` 验证，restart policy 不是健康监控。
若 php74 被重建或网络命名空间改变，需先停客服容器，再在 php74 就绪后执行：

```sh
docker compose -f deploy/crmeb/compose.yaml up -d --force-recreate postgres gateway
```

此操作保留命名卷，但仍须复验 readiness 和会话恢复；不能宣称依赖容器重建后自动修复。
停用桥接可将私密配置的 enabled 设为 false；不要删除密钥或清空商城共享缓存。

## 验收与阅读顺序

1. 本说明与 `compose.yaml`：部署边界、数据归属及生命周期。
2. `integrations/crmeb/Bridge.php`：上游 401 清理旧绑定，缓存 TTL 不得为零。
3. `integrations/crmeb/config.php`、`OmniChat.php`：私密实例配置及受限元数据。
4. `web/chat/src/crmeb.mjs`、`crmeb.jsx`：续签、同会话重试、持久化提示；保留既有加载和错误状态。
5. `integrations/crmeb/persistent_check.mjs` 与 `persistent-acceptance-report.json`：28 项真实 HTTP 检查。

真实验收包含两个独立账号、网关和 PostgreSQL 重启后逐条历史一致、旧凭证拒绝并续签到原会话、
相同 event_id 重放不重复、改正文 409、停服失败关闭、恢复后重试不重复、退出后拒绝、另一账号不受影响。
脚本会短暂中断本专用客服服务；仅在明确授权的测试环境运行，凭据 JSON 经 stdin 输入，不写报告。
脚本会保留测试消息并退出自行创建的商城登录。报告不包含密码或 Token。
真实自然到期计时未在本轮等待；过期绑定和前端 401 续签由单元测试覆盖，不混称为实际 TTL 到期验收。

PHP74：30 项通过；前端：22 项通过；Go webchat/webchat-server 测试通过。
静态资源构建带内容版本号，避免商城缓存策略使浏览器继续加载旧 JS/CSS。

浏览器复验：用户一原登录直接连接，读取前一轮 HTTP 验收保留的记录；发送“持久化页面验收：重启后保留这条测试消息。”后收到固定回复。
再次重启网关，在未重载页面点击刷新记录后未出现登录失效，随后重载页面仍为会话后缀 `e91597fc89bf`，
该条客户消息和对应回复各一条。截图：`integrations/crmeb/h5-evidence/persistent.png`。
续跑时两个容器曾正常退出（退出码 0）；未据此推断退出原因。使用原卷重新启动后读到旧历史。
最终保留专用 PostgreSQL 与网关运行，不清理数据库、不退出用户原浏览器登录。
