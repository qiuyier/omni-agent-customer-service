# 参考部署：独立知识管理与发布知识接入

本机启动与配置验收记录：[2026-11-08](../../docs/reference-managed-acceptance-2026-11-08.md)。

适用于本机单业务参考部署，不是公网部署指南。管理服务是独立 Go 进程；Compose 仍只管理数据库。管理页面不挂在客户或坐席入口上，不授予坐席管理员权限。所有命令在仓库根执行。

## 1. 准备专用库与二进制

新安装先按 [README](README.md) 完成 init、构建、专用 PostgreSQL 启动和 migrate；已有部署不要重新 init，不删除配置或卷。已有库升级前先按自己的备份策略备份，再显式运行 migrate；启动器不会自动迁移。知识管理要求当前 schema 3。

```sh
go build -o bin/reference/knowledge-admin ./cmd/knowledge-admin
# 更新旧部署时也重新构建网关和迁移程序
go build -o bin/reference/webchat-server ./cmd/webchat-server
go build -o bin/reference/migrate ./cmd/migrate
node deploy/reference/run.mjs migrate
```

迁移仅针对 `.local/reference/config.json` 指定的客服专用库，不能连接原业务库。任何一步失败应停止排错，不继续启动。

## 2. 创建独立管理员

账号文件固定为 `.local/reference/knowledge-accounts.json`，权限 0600。无默认账号和密码。以下首次创建示例在 **Bash** 中逐条执行：密码不回显、不进入命令行参数，文件已存在时拒绝覆盖。使用自己生成的 16–512 字节强密码并保存在密码管理器，不使用验收密码。

```bash
set -o pipefail
read -r -s -p '新管理员密码: ' knowledge_password
printf '\n'
printf '%s\n' "$knowledge_password" | bin/reference/knowledge-admin -hash-password | node --input-type=module -e '
import fs from "node:fs";
const hash=fs.readFileSync(0,"utf8").trim();
if(!hash.startsWith("scrypt-v1$")) throw Error("Password hash failed; no account file written");
fs.writeFileSync(".local/reference/knowledge-accounts.json", JSON.stringify([{id:"knowledge_admin",password_hash:hash,permissions:["knowledge.read","knowledge.edit","knowledge.publish"]}],null,2),{mode:0o600,flag:"wx"});'
unset knowledge_password
node deploy/reference/run.mjs check-knowledge
```

哈希生成或写文件失败时先清除 shell 中的密码变量并排错。不要开启 shell tracing（`set -x`），不要把账号文件提交或贴进日志。编辑员可另配置仅 read/edit 权限，详情见 [账号与安全限制](../../docs/knowledge-admin.md)。修改账号文件后须重启管理进程，旧会话失效。

`check-knowledge` 仅验证本地配置，不表示数据库可用。启动会检查数据库与 schema：

```sh
node deploy/reference/run.mjs start-knowledge
```

浏览器打开 `http://127.0.0.1:8094/`，用刚创建的账号登录。端口占用时启动器拒绝，不会杀掉其他服务。管理进程只得到数据库及管理员配置，不读取模型配置；它与客服进程分别启动、分别 Ctrl+C 停止。

## 3. 导入、检查、发布

1. 登录后下载页面 FAQ 示例模板，填写自己的资料；先用虚构资料演练。
2. 选择 JSON、确认导入、编辑并保存草稿。保存不影响客服生效版本。
3. 查看差异，运行单条试检索；可下载回归模板、填写预期 FAQ ID、运行批量检查并下载报告。这些检查不调用模型，不代表模型最终回答质量。
4. 确认发布已保存草稿。必须先发布首个版本，才能启动下面的管理知识 AI 模式。仅保存、未发布时预检会失败；显式发布空数组表示停用全部 FAQ。
5. 需要撤销时选择历史版本并明确回退；草稿和既有聊天回复不会随之改写。

## 4. 明确选择客服知识来源

| 命令 | 知识来源 / 行为 |
| --- | --- |
| `start` | 默认固定回复，不读取模型或管理知识 |
| `start-ai` | 原 `ai.json` 的 FAQ 文件，管理发布不会改变它 |
| `start-ai-managed` | 独立 `ai-managed.json` 的模型配置 + 数据库已发布知识 |

创建权限 0600 的 `.local/reference/ai-managed.json`，仅包含下面五个字段，不含 `faqPath`；填入真实供应商配置前自行确认资料授权和费用。空值仅展示结构，不能直接使用。不要覆盖原 ai.json，也不自动导入根 .env。

```json
{"provider":"custom","protocol":"chat_completions","model":"","baseURL":"https://example.invalid/v1","apiKey":""}
```

```sh
# 只读检查配置、数据库及已发布知识，不调用模型
node deploy/reference/run.mjs check-ai-managed
# 先停止占用 8095/8096 的旧客服进程；确认费用后才启动
node deploy/reference/run.mjs start-ai-managed
```

客户与员工入口仍为 README 中的 8096。管理模式每次检索读取当前发布版本；后续发布/回退不需重启客服，但已开始的检索可能使用旧版，历史回复不会重算。此模式仍会把问题、上下文和匹配资料交给模型供应商，没有调用次数硬限额。

启动成功和资源检查不证明模型密钥有效或回答质量合格。真实回答验证须单独授权，用虚构问题控制次数；本指南不要求为部署预检调用真实模型。原 verify.mjs 是 fake 专用，不能当作 AI 验收。

## 5. 停止、恢复与排错

管理和客服各自在其终端 Ctrl+C；数据库按 README 停止，保留配置和卷。再次运行 check-knowledge/start-knowledge 与 check-ai-managed/start-ai-managed，无需重新创建管理员或重新导入。管理重启需重新登录。

- 没有发布版本：先在管理页面发布，不手工写表。
- 发布后客服未变：确认使用的是 start-ai-managed，而不是 start 或 start-ai；新建测试咨询避免历史回复干扰。
- 配置失败：检查账号文件、ai-managed.json 的字段和 0600 权限、已构建二进制；不要打印密钥排错。
- 回到固定回复：停止 AI 进程，再执行 start；不会清除知识和聊天数据。
- 管理入口不是公网方案：不要改成公网监听；生产 IAM、HTTPS、代理、告警和备份须独立评估。
