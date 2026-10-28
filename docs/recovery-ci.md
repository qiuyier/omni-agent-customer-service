# 恢复回归 CI（2026-10-24）

入口：.github/workflows/recovery.yml，名称 Recovery regression。

2026-10-29 当前状态：默认回归、手动 PostgreSQL、受控失败标红与报告下载的云端验收均已完成，临时数据库及远端验收分支已清理，见 [验收报告](recovery-ci-acceptance-2026-10-29.md)。下文的延期及“首次启用”说明为 2026-10-24 历史记录，不代表当前未验收。

优先级更新（2026-10-24）：按用户决定，云端首跑验收延后至 1.0.0 正式发布后，统一跟踪于[发布后优化录 OPT-001](post-1.0.0-improvements.md#opt-001github-云端-ci-首跑验收)。下文首次启用步骤保留为后续操作说明，不代表当前待立即执行任务。本地必要回归继续运行。

## 触发与范围

- push / pull_request：运行默认回归，不创建数据库。
- workflow_dispatch：可手动勾选 postgres，运行一次性 PostgreSQL 回归。默认false。
- job 使用 ubuntu-24.04、Node24，Go版本读取 go.mod；总超时20分钟。
- 先检查入口自身与CI契约，再预编译Go依赖，避免冷缓存耗时影响已有测试的短超时，最后运行统一回归命令。预编译不算业务测试通过。

没有部署、发布或自动提交步骤。权限仅contents:read，checkout不持久化凭证，不使用pull_request_target或仓库密钥，不连接业务数据库或模型供应商。没有设置分支保护；要把该检查设为合并必需，需要维护者另行配置。

Linux runner 中只在手动数据库模式创建 desktop-linux context，指向本机 unix:///var/run/docker.sock。回归脚本继续管理随机命名的tmpfs容器，退出清理只针对本次容器。不改变开发者电脑上的Docker context。

## 报告和失败

所有执行步骤使用bash和pipefail，tee保留输出但不吞掉失败退出码。没有continue-on-error。

artifact步骤使用always()，保留14天：

- runner-tests.log、compile.log、recovery-console.log；手动数据库模式另有docker-context.log。
- omni-recovery-check-*/report.json及各测试阶段日志，包括入口自身中断测试生成的interrupted报告。

文件只从runner临时实验目录的日志和报告白名单收集，不上传工作区、.env、缓存或编译二进制。名称包含run_id和run_attempt。主回归目录由recovery-console.log中的Report directory指示；不要把入口自测故意生成的interrupted报告误认为主回归失败。

GitHub页面：Actions → Recovery regression → 对应运行 → Artifacts。下载后先看主report.json及失败阶段log。manual_required仍保留人工浏览器待验项，不因CI绿色自动认证视觉或隐藏/恢复行为。

always()只是要求runner尽力上传，不保证runner丢失、硬取消、网络故障时仍可上传。环境配置失败可能尚无业务report.json，应先查看GitHub步骤日志。无文件时显示警告，不掩盖之前失败。

## 首次启用与当前状态

本地检查时仓库没有.github工作流，也没有Git remote。本轮新增工作流文件，未提交、未推送、未实际触发GitHub云端运行，不能把本地验证称为云端CI已通过。

维护者需先确认并配置目标远端，将这些文件提交推送。手动触发入口需要工作流存在于默认分支，然后在Actions中选择Run workflow及postgres选项。正式云端验收应至少检查一次自动运行和一次手动数据库运行，并下载artifact确认失败日志保留。

本地已验证：actionlint语法与表达式检查、10项入口/CI保护测试，以及统一默认回归。本机PostgreSQL入口此前已通过；Ubuntu上的Docker context建立和artifact上传仍需云端首跑确认。

## 阅读顺序

本文 → .github/workflows/recovery.yml → scripts/recovery_ci.test.mjs → scripts/recovery_check.mjs → [统一回归说明](recovery-regression.md)。

参考官方说明：[触发工作流](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/trigger-a-workflow)、[上传artifact](https://github.com/actions/upload-artifact)。
