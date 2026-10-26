# 候选版本验收记录（2026-10-26）

结论：本轮构建、自动回归、fake 核心浏览器闭环及实际重启恢复通过；**不是发布批准，R2/R3/R4 仍有未关闭事项**。真实模型调用 0，无发布/推送、CRMEB 或 PHP74 改动。

## 固定版本及交付范围

被测 commit：`f4a422cf5acfe8b626ca6822376a4f522c0410d5`。开始及测试完成时工作区干净；之后仅新增本报告并更新发布清单。报告本身不在被测 commit 的源码快照内。

通过 `git archive` 从该 commit 生成 `/tmp/omni-rc-f4a422-source.tar`，包含 1183 个跟踪文件，不包含 Git 历史、本地未跟踪文件或忽略文件。**交付清单定义为该 commit 的完整树**，不是当前目录全量压缩。包含源码、go.mod/go.sum、前端 package-lock、已构建页面及 LEGAL 文本、参考部署、测试和文档；示例与实验保留各自边界，不表示全部能力进入推荐支持范围。

源码包 SHA-256：`2b472757c225ede599459d0e97b52e1396ba59d8d6af21fa706690c89d71dbd4`。

三个 Go 入口均构建成功，仅为本机验证产物，不是多平台发布包：

| /tmp 下产物 | SHA-256 |
| --- | --- |
| omni-rc-f4a422-server | 9198d712f9b0e0e5985d94e8ab09dd93ee028dafbd80be9927470f054d1b5937 |
| omni-rc-f4a422-migrate | b2989efe1b5d9f09c5dfa2715c0be5363294d4cdda63b5fe319ae1351ff4b2ef |
| omni-rc-f4a422-demo | 2d35188aba3143088321868ee7a6c28c8200ee60c0a7365ff16d76de29f868e3 |

## 本轮执行结果

- `node scripts/recovery_check.mjs --postgres --allow-test-writes` 通过：网页/宿主 121 项，网关 10 个 pass 事件，PostgreSQL 12 个 pass 事件（含父子及重复计数，不合并称为独立场景数）；均无失败/跳过。
- reference 全部测试、host-kit onboarding/proxy、fresh-onboarding acceptance、recovery_check 与 CLI 测试组合：30 项通过，0 失败、0 跳过。
- `go build` 构建 webchat-server、migrate、webchat-demo 成功；固定部署参考二进制重新构建，check 配置和资源检查通过，明确 fake、知识关闭。
- 浏览器 customer_a 发 `RC-f4a422-20261026` 消息并收到固定回复；申请人工，staff 接管并回复，客户显示人工坐席回复；关闭后客户发送框禁用；发起新咨询为空且可重新申请人工。
- `verify.mjs --allow-test-writes` 通过共享双客户隔离/人工闭环并保存快照。随后真正停止应用和专用数据库，再启动数据库与应用；`verify.mjs --verify-restart` 通过同一会话及完整历史精确比较。
- 浏览器重启后旧登录失效，同一客户重新登录能看到唯一重启探针及固定回复。截图暂存 `/tmp/omni-rc-f4a422-restart.png`。

原始自动报告暂存 `/var/folders/c9/f3mfsydd2ns7jx4bp2xlky2h0000gn/T/omni-recovery-check-gN0hCw/report.json` 及同目录日志。临时目录可能被系统清理，本报告保留摘要，不把临时文件承诺为永久归档。

## 交付凭据检查

扫描对象为实际源码 tar 及同 commit 的文件内容，而不只检查 .gitignore。1183 个文件中没有 `.env`、`.local/`、Git 历史、私钥扩展名文件或 postgres-password。检查本机 10 个去重后的敏感配置值（不输出值）：有 2 个匹配，均是 README 已公开的本地开发/测试 DSN 示例；无模型密钥或 reference 随机凭据匹配。

私钥块、常见供应商密钥/GitHub token/AWS access ID 的有限正则检查，唯一命中为 `internal/bootstrap/logging_test.go` 的虚构 `sensitiveMarker`，用于验证错误不泄露输入。不是实际供应商密钥。

这不是“没有任何测试密码”的交付包：源码包含公开、低权限预期的测试夹具及开发示例，不能用于生产；本机生成的私密测试密码没有进入快照。有限规则和已知值检查不等于完整秘密扫描、历史泄漏审计或依赖许可证合规结论，不对未知凭据做保证。

## 未关闭事项

1. R2：本轮核心浏览器流程已通过，但统一回归明确保留的跨标签身份切换、页面隐藏/恢复及无人操作网络恢复等人工矩阵未重新执行，不能用 HTTP/逻辑测试替代；也未进行全新环境安装或 Linux 运行验收。真实 AI 仅引用之前小样本证据，不计作本轮复验。
2. R3：有限凭据扫描已完成；依赖许可证/第三方声明清单审查及最终分发材料仍未完成。前端已提交产物实际可用，本轮没有重建前端验证可重复性。
3. R4：仓库分发许可仍需所有者决定，不自行创建 LICENSE。

测试结束应用和 reference 专用数据库已停止，原配置、数据卷及新增虚构验收记录保留。统一回归自己创建的临时数据库容器已删除、不可恢复；它未包含原业务数据，清理报告为 passed。没有删除既有业务或 reference 数据。
