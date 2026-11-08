# v1.1.0 — 独立知识管理

发布日期：2026-11-09（Asia/Shanghai）。公开状态以 GitHub v1.1.0 标签及 Release 为准；本版本是通用客服源码发布，不构成公网生产开放批准。

## 新增能力

- 独立知识管理员登录及内嵌管理页面；读取、编辑和发布权限分别授权，不提升普通客户或坐席权限。
- FAQ 草稿编辑、搜索、JSON 导入导出、发布前差异、单条试检索、批量回归检查及下载报告。
- 发布与历史版本回退、revision 冲突保护、知识和成功操作记录的 PostgreSQL 持久化；管理进程重启后会话失效，但知识保留。
- 聊天网关通过显式 managed 来源读取数据库已发布知识；保存草稿不改变生效版本，发布或回退影响后续检索，不重写历史回复。
- 参考部署提供 check-knowledge/start-knowledge 与 check-ai-managed/start-ai-managed。默认 start 仍固定回复，原 start-ai 仍读取 FAQ 文件，不自动迁移资料或启用模型。

## 开始使用

先阅读 [参考部署](../deploy/reference/README.md)，再按 [知识管理部署指南](../deploy/reference/KNOWLEDGE.md) 构建独立管理程序、创建私密账号配置、导入 FAQ、检查并发布。管理页面随 Go 程序嵌入，不需要前端开发服务器。

## 升级提醒

**数据库 schema 从 2 升至 3；即使保持固定回复也必须显式迁移。** 升级前停止写入、备份专用库与配置，并在恢复副本验证。不要重新 init、删卷或用示例配置覆盖现有密钥。

旧 v1.0.1 严格要求 schema 2，迁移后不能只换回旧二进制。逆向迁移会删除全部知识草稿、版本和审计；恢复升级前备份还需处理备份后的新增数据。详见 [升级和回退指南](knowledge-release-upgrade.md)。

## 验证与边界

- [完整知识链路](managed-knowledge-e2e.md)：真实管理/客服进程及独立 PostgreSQL，保存、发布、回退、重启和冲突验证；仅本机模拟模型。
- [双浏览器协作](knowledge-admin-collaboration-2026-11-04.md)：旧页面保存/发布被拦截、核对后继续操作、退出换号清理。
- [参考部署入口](reference-managed-acceptance-2026-11-08.md)：正式启动路径、未发布拒绝、重复启动保护和重启状态保留。
- [旧数据升级](schema-upgrade-acceptance-2026-11-08.md)：v1.0.1 真实程序建立旧数据，迁移后逐表及序列比较，继续人工流程并重启核对；8 项通过。
- [本地发布检查](knowledge-release-readiness-2026-11-08.md)：全量 Go 测试、vet、构建、依赖校验、默认回归和管理页面产物一致性检查通过。最终提交、CI 和附件校验以 Release 的 VERIFICATION.json 为准，不将历史证据冒充最终版本的全场景测试。

单业务独立实例、关键词 FAQ、小规模管理存储；非多租户共享知识、语义搜索、订单退款等业务工具或生产 IAM。管理历史和快照有上限；公网使用须另行评估 HTTPS、可信身份、网络、备份与值守。真实模型效果、费用和资料授权仍须部署方确认，本轮发布不调用真实模型。

## 源码附件

包含源码、已构建页面和第三方许可；不含本机配置、数据库、node_modules 或预编译 Go 二进制。下载后运行 `shasum -a 256 -c SHA256SUMS`，并核对 SOURCE-MANIFEST.json 与 VERIFICATION.json。自有代码 MIT，第三方遵循各自许可。
