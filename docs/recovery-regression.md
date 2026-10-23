# 恢复与身份隔离统一回归

GitHub Actions 自动运行、手动数据库验收及日志下载见 [CI 使用说明](recovery-ci.md)。

## 使用

在仓库根目录运行（需要项目支持的 Node.js、Go，race 测试需要可用的本机 C 工具链）：

```sh
node scripts/recovery_check.mjs
```

默认运行网页逻辑、宿主接入与身份隔离、网关恢复并发测试，不启动 PostgreSQL。
要验证真实持久化，显式确认临时测试写入：

```sh
node scripts/recovery_check.mjs --postgres --allow-test-writes
```

数据库模式固定使用本机 Docker Desktop 的 desktop-linux context。需要 Docker Desktop 已启动，postgres:16-alpine 镜像可用（缺少时 Docker 可能下载镜像）。不支持传入外部 DSN；不读取 .env，也不继承调用方的模型密钥、数据库地址、NODE_OPTIONS 或 GOFLAGS。依赖未缓存时 Go 本身可能需要下载依赖，这不等于模型请求。

## 覆盖映射

| 路径 | 自动测试 |
| --- | --- |
| 回复失败、原事件恢复、已有回复不再生成 | pending-receipt.test.mjs；TestReceiptReplyRecoveryEligibility |
| 核验后发生人工接管/关闭、迟到回复抑制 | TestRetrySnapshotDoesNotAuthorizeAfterHandoff；TestRetryAcrossHumanLifecycle |
| 两个客户端重试同一失败事件 | TestTwoSessionsRetrySameFailedEvent |
| 登录到期、切换账号、迟到响应拒绝及重登读取 | standalone-host/host.test.mjs；inflight-identity.test.mjs；网页身份/凭证测试 |
| PostgreSQL 持久化及网关/连接池重建后重放 | TestDatabaseRetryAcrossHumanLifecycle，各人工阶段重复3遍，仅数据库模式 |

这是既有回归的编排，不增加业务功能，也不将模拟模型测试视为真实模型质量验收。数据库重建场景是 HTTP 网关和连接池重建，不是容器宕机恢复。

## 报告与退出码

启动时打印唯一临时目录，其中保留 report.json 和每阶段日志。不会覆盖已有报告。JSON 包含：

- status：running / passed / failed / interrupted。
- suites：各阶段状态、通过/跳过/失败数量、退出码、超时、日志路径。
- postgres：not_run / requested / executed；默认未运行不会冒充通过。
- manual_required：仍需人工执行的真实浏览器项目；每次报告都保留，不自动引用历史浏览器报告认证本次版本。
- cleanup：本次专用数据库容器清理结果。清理失败导致整轮失败。

退出码0只代表已选择的自动检查通过。测试退出非零、超时、跳过、零项通过均不能获得绿色结果。Go计数包含父测试及子测试，重复执行也重复计数，不宜直接与JS数量相加当作独立业务场景数。

参数错误退出2；测试失败或中断退出1。命令失败日志留存。普通测试失败后仍尝试后续检查；环境错误会停止余下阶段并记录error。

## 清理与安全边界

子进程以专用进程组运行，结束、超时和SIGINT/SIGTERM时回收其进程组。数据库使用随机容器名、loopback随机端口、固定虚构凭证和tmpfs，只删除本轮创建的确切容器。不启动或停止既有CRMEB、PHP74及standalone数据库。

正常退出后容器及其虚构数据库数据被删除、不可恢复；报告日志保留。主进程SIGKILL、断电或Docker不可用无法保证自动清理；此时依据报告中的确切resource名称检查残留，禁止批量删除其他容器。

程序化报告保留在系统临时目录，长期归档需另行保存。浏览器视觉、真正隐藏/恢复、用户交互等仍应按manual_required单独验收，不能用自动测试计数替代。

## 入口自身回归

```sh
node --test scripts/recovery_check.test.mjs scripts/recovery_check_cli.test.mjs
```

覆盖写入参数保护、环境过滤、跳过/零项/失败结果判定，以及真实子进程收到SIGINT后退出并写出interrupted报告。

## 本轮验收（2026-10-24）

默认模式和数据库模式已实际执行。网页/宿主121项通过；网关10个pass事件；数据库12个pass事件（父子测试和3遍重复计数）。入口自身7项通过，包括中断路径。没有执行真实浏览器操作或外部模型调用，没有发布。

阅读顺序：本文 → scripts/recovery_check.mjs → recovery_check_support.mjs → recovery_check.test.mjs / recovery_check_cli.test.mjs → 对应场景测试和运行报告。
