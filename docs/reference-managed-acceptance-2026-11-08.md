# 参考部署知识管理接入验收

日期：2026-11-08（Asia/Shanghai）。结论：本机正式启动入口接通；不是公网发布或模型回答质量认证。

## 实现

- `check-knowledge` / `start-knowledge`：独立管理员配置、回环 8094，沿用参考部署专用库；不传递模型密钥、客户宿主或坐席密钥，不自动迁移。
- `check-ai-managed` / `start-ai-managed`：只读取私密 `ai-managed.json`，显式使用数据库已发布知识；拒绝混入 faqPath。保留普通 fake 与原文件 AI 模式。
- [首次使用指南](../deploy/reference/KNOWLEDGE.md) 贯通构建、管理员哈希配置、导入、检查、发布、客服来源选择与停止恢复。

## 本轮实际证据

在临时源码副本 `omni-reference-managed-xEBNSc` 中构建真实二进制，用全新、带归属标签的 PostgreSQL 16.15 tmpfs 容器，确认 15435/8094/8095/8096 空闲后运行。没有复制原 `.local` 或根 `.env`，没有使用原参考库、CRMEB/PHP74。验收客户端调用的启动路径均是本轮 `deploy/reference/run.mjs`，不是 browser-lab 或 managed_knowledge_e2e 的进程装配。

以下 8 项通过：

1. 正式 init 与 migrate 在隔离配置、专用新库成功。
2. 创建虚构管理员哈希文件，check-knowledge 通过，start-knowledge 提供页面。
3. 重复 start-knowledge 被拒绝，原服务仍可用。
4. 配置虚构 HTTPS 模型地址后，无已发布版本的 check-ai-managed 失败。
5. 通过真实管理 HTTP 登录、保存 FAQ、发布产生 r2。
6. check-ai-managed 通过，start-ai-managed 的网关 readyz 及业务页面返回 200；未发送聊天生成请求。
7. 停止并重新启动管理服务后旧 Cookie 返回 401，重新登录仍读到 r2 和已发布版本。
8. 普通 check 仍通过，不读取管理模型配置或启用真实模型。

模型地址为保留的 `.invalid` 测试域名，密钥为虚构值；真实模型调用 0。本轮只证明启动与知识资源可用，不声称已通过该入口验证模型生成内容。管理员配置由程序通过 stdin 哈希并写入，指南的 Bash 不回显交互未单独人工输入验收；导入/差异/报告页面证据见此前 [完整链路](managed-knowledge-e2e.md) 和 [双浏览器协作](knowledge-admin-collaboration-2026-11-04.md)。

验收结束停止本次管理和客服子进程，核对标签后删除测试容器，其 tmpfs 虚构数据不可恢复；未创建持久化数据卷。机器报告留在临时副本的 acceptance-report.json，不随源码公开。

离线配置/装配测试 8 项全部通过：config.test.mjs、ai.test.mjs、managed.test.mjs；包含显式来源、缺配置不回退、字段限制、权限、符号链接拒绝及普通模式不变。脚本语法、git diff --check 通过。本轮未重跑全量 Go 回归，Go 生产代码无修改。
