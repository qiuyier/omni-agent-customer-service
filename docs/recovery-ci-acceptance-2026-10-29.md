# 云端恢复回归验收（2026-10-29）

## 结论

OPT-001 完成。主分支业务代码与工作流未改动；本次只补验收文档。未发布版本，未调用真实模型，未操作本机数据库、CRMEB 或 PHP74。

主分支基线：`c271f07e35535d7fcb6352b930ac2770b6e77bf7`。

| 场景 | 运行 | 结果 |
| --- | --- | --- |
| push 默认回归 | [40742211874](https://github.com/qiuyier/omni-agent-customer-service/actions/runs/40742211874) | success；网页/宿主 127 项，网关 10 个 Go pass 事件；未运行 PostgreSQL |
| 手动 postgres=true | [40743290776](https://github.com/qiuyier/omni-agent-customer-service/actions/runs/40743290776) | success；网页/宿主 127 项，网关 10 个、数据库 12 个 Go pass 事件；无失败/跳过 |
| 受控测试失败 | [40743416404](https://github.com/qiuyier/omni-agent-customer-service/actions/runs/40743416404) | 预期 failure；网页/宿主 127 通过、1 失败；网关仍通过，报告上传成功 |

Go 计数含父子测试和数据库三遍执行，不代表 12 个独立业务场景。

## 数据库证据

数据库运行的主报告为 artifact `recovery-40743290776-1` 中的 `omni-recovery-check-fH91hX/report.json`，由 `recovery-console.log` 的 Report directory 确认：

- `status=passed`、`postgres=executed`、`external_models=not_configured`。
- `postgres-recovery` 执行 `TestDatabaseRetryAcrossHumanLifecycle` 三遍，覆盖失败事件恢复、人工处理阶段及 HTTP 网关/连接池重建后的记录重放。
- `cleanup` 中本轮随机容器状态为 `passed`，`postgres-cleanup.log` 确认删除了该容器。临时 tmpfs 测试数据随容器删除，不保留业务数据。
- 这是网关/连接池重建，不是 PostgreSQL 容器宕机重启测试，也不是浏览器重登验收。

## 受控失败与报告下载

用户单独授权创建、推送和清理临时验收分支。使用独立临时副本，分支 `codex/ci-failure-acceptance-20261029`，提交 `ee59e408089de4ccd0b5a84c660bc3c909a287f6`；仅新增 `controlled-ci-failure.test.mjs`，断言固定失败标记 `EXPECTED_ACCEPTANCE_FAILURE`。未修改工作流，未合并主分支。

核对 GitHub job：`Run recovery regression=failure`，`Upload reports even on failure=success`，整体结论 `failure`。下载 artifact `recovery-40743416404-1` 后，确认 `omni-recovery-check-ctA8Di/report.json` 为主失败报告、失败数为 1；对应网页日志包含上述固定标记。入口自测产生的其他 interrupted 报告不作为主结果。

三次运行的 artifact 均已实际下载并读取，不仅检查上传步骤。正常默认报告为 `omni-recovery-check-cKa6JX/report.json`。GitHub artifact 保留 14 天；本报告保留运行链接和结论，不将临时日志、凭证或测试副本纳入仓库。

验收后删除远端临时分支，删除时使用预期 SHA 保护；再次查询确认该远端分支不存在、main 仍为基线提交。本机独立临时副本和下载报告保留供排查，不影响开发工作区。

## 边界与后续

- 本轮未执行浏览器跨标签/隐藏恢复，不将 CI 绿色替代报告中的 `manual_required`。
- 本轮没有授权发布 1.0.1；验收文档等待用户提交。
- runner 提示所用 Actions 的 Node20 运行时弃用，已记入优化录 OPT-004；本次实测运行和上传均通过，未临时升级依赖。
