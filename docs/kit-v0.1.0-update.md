# kit v0.1.0 更新验收

2026-10-27。客服基线 commit f95ada68ea34c7238b2258b9f546994610fa93c8 上更新工作区，未提交/推送客服。kit 标签 v0.1.0 指向 53dfbc4d699bed154fbefbdf6a13c06d30b0936a，公开且包含 MIT LICENSE，无运行代码差异。

## 模块证据

- 正常命令：`GOPROXY=https://proxy.golang.org,direct GOSUMDB=sum.golang.org go get github.com/qiuyier/omni-agent-kit@v0.1.0`；原校验 404 已解除，本轮未关闭校验或使用 replace。
- go.mod 仅 kit 从旧伪版本升级到 v0.1.0；go.sum 新增该版本两条记录，旧校验记录保留不代表旧版本仍被选择；未运行无关依赖升级或 tidy。
- 模块 Sum：`h1:cg1Dd9hskhF0F/zpwQKBxrbJobgEC+VdjcDPzdEG6p8=`；GoModSum：`h1:vwiZwVUI/htC42LlUmWeXhvFEnSixRgs4TPkE5RVzKk=`。
- 下载包根 LICENSE SHA-256：`3597cf8a45f4ad543b6c75431d2bd7010181c49110b84f1fde0a46be3eb4a756`，与 kit LICENSE 逐字一致；许可为 MIT，Copyright (c) 2026 QiuYiEr。
- 2026-10-26 依赖清单及 Go 根声明汇编是旧候选版本证据，未偷偷改写；本文件作为 kit 依赖变更增量证据。最终打包时须重新生成全量清单，不能使用旧包校验和。

## 回归结果

- `go mod verify`：all modules verified。
- `env -u TEST_DATABASE_URL go test ./... -count=1` 和 `go vet ./...`：退出 0。无真实数据库 DSN，依赖数据库环境的用例不视为本轮实库通过。
- `node scripts/recovery_check.mjs`：默认自动检查通过；网页/宿主与网关恢复通过，未启用 PostgreSQL。原始报告在 `/var/folders/c9/f3mfsydd2ns7jx4bp2xlky2h0000gn/T/omni-recovery-check-DYb4YZ/report.json`（临时目录，不保证长期保留）。
- reference 测试、host-kit onboarding/proxy、fresh-onboarding acceptance：23 项通过，0 失败、0 跳过。

本轮未调用真实模型、未运行数据库写入验收、未操作 CRMEB/PHP74、未重新执行人工浏览器矩阵。既有浏览器证据保留原范围；本轮不是完整生产发布验收。完成后删除本聊天的 kit 校验定时任务，避免重复更新。
