# 独立知识管理员 API

日期：2026-10-30。已实现独立登录 API 与管理宿主；**尚无网页表单和管理页面**。不是客户聊天或坐席登录的升级权限，也不自动开放公网。

## 运行方式

新增 `cmd/knowledge-admin`，只允许 literal loopback IP 监听，默认 `127.0.0.1:8094`。需要已经迁移到 schema 3 的客服专用 PostgreSQL；进程不会执行迁移、调用模型或启动客服网关。

环境配置：

| 配置 | 含义 |
| --- | --- |
| DATABASE_URL | 已有客服专用库；不得连接 CRMEB 原库 |
| KNOWLEDGE_ADMIN_ACCOUNTS_FILE | 仅当前用户可访问的账号 JSON 文件，权限 0600、最大 64 KiB，无默认值 |
| KNOWLEDGE_ADMIN_ADDRESS | 本机监听地址，默认 127.0.0.1:8094 |
| KNOWLEDGE_ADMIN_ORIGIN | 浏览器/客户端使用的精确 Origin，默认 http://127.0.0.1:8094；改端口时也要更新。远程使用须为 HTTPS，经独立配置的可信反向代理转发到 loopback |

构建：`go build -o bin/knowledge-admin ./cmd/knowledge-admin`。`-check-config` 只读本地配置，不检查数据库或端口，也不调用模型。正常启动会检查数据库连接与 schema，缺表或版本不符则停止。

账号文件是数组，每项为 `id`、`password_hash`、`permissions`。ID 允许 1..64 位字母、数字、下划线或连字符。最多 32 个账号；权限从 `knowledge.read`、`knowledge.edit`、`knowledge.publish` 显式选择，编辑者不得默认拥有发布权限。

`-hash-password` 通过标准输入接收 16..512 字节 UTF-8 密码（去掉末尾换行，拒绝控制字符），输出随机盐的 `scrypt-v1` 哈希。**不接受密码命令行参数，也拒绝直接从回显终端读密码。** 使用密码管理器或不回显输入工具提供管道输入，不把明文密码写进 shell history。不要使用文档中的虚构测试密码。

将真实配置保存到 `.local/knowledge-admin/` 等私密目录（已加入 gitignore），勿提交账号文件或哈希；gitignore 不是安全权限替代。没有注册、默认账号、默认密码或在线改密功能。增删账号、修改密码/权限需要更新私密配置并重启进程，重启使所有会话失效。

## 登录与管理

- `POST /auth/login`：JSON `{account,password}`，必须匹配配置 Origin，成功只返回 authenticated 和独立会话 Cookie。
- `GET /auth/me`：返回稳定账号 ID、明确权限和非凭据的会话版本；失效返回 401。
- `POST /auth/logout`：JSON `{}`，撤销当前会话并清除 Cookie。其他已登录会话不受单次退出影响；禁用账号或重启撤销全部相关会话。
- `/internal/knowledge/state|draft|publish|rollback`：使用上述管理员 Cookie，契约见 [管理接口说明](faq-management-wiring.md)。权限不足返回 403，不接受请求里自报的角色或 actor。

Cookie 为 HttpOnly、SameSite=Strict、Path=/，HTTPS 使用 Secure 和 `__Host-` 前缀；本机 HTTP 使用不同的 local Cookie 名称。服务端只保存随机 token 的 SHA-256，不把 token 放进 JSON。登录轮换当前请求持有的旧会话；客户/坐席 Cookie、重复 Cookie、到期或已撤销会话均拒绝。

会话固定 30 分钟，不滑动续期，最多 128 个。进程内限制每账号（未知账号共用一桶）每分钟 10 次登录尝试、全局每分钟 60 次，最多 2 次并行 scrypt；过限 429，不作自动重试。这是单实例保护，不是分布式限流，可能在恶意尝试下影响同账号合法登录。服务器配置了头/请求/响应超时和头大小限制。

## 验收

- race 测试覆盖密码校验、Cookie 属性、会话轮换、到期、退出、账号撤销、跨进程拒绝、外来/重复 Cookie 拒绝、跨站和自报权限拒绝、编辑者不能发布、限流及离线配置检查。
- 实际构建管理二进制，在专用 PostgreSQL 16 容器 `omni-admin-login-20261030-01` 上执行真实 HTTP：登录 → 保存草稿 → 发布 → 编辑者拒绝发布 → 跨站拒绝 → 登录轮换 → 停止并重启管理进程 → 旧会话失效 → 重新登录 → 完整管理状态与重启前相同 → 退出后拒绝读取。
- 此次重启的是管理进程，不是数据库容器；数据库实际重启证据见 [存储验收](faq-management-persistence.md)。本轮不是浏览器 UI 验收。
- 虚构账号由验收程序临时随机生成，不是正式管理员，没有调用真实模型。报告目录标识 `omni-admin-process-vQskzC`。进程已停止，测试容器及匿名卷已清理，测试库内容不可恢复。
- 默认统一回归新增 knowledge-admin 阶段。未提交、未发布、未修改已有部署的配置或账号。

## 限制与下一步

这是单实例独立认证方案，不具备 MFA、SSO、找回密码、账号管理页面、分布式会话或持久化登录失败审计。现有成功知识操作审计仍由管理核心保存；登录尝试计数仅在内存中。用于公网生产前还需独立的安全评估、HTTPS/代理配置、告警与备份策略，不能仅凭本机测试放开公网。

接入方已有管理 IAM 时，可以继续用 `managehttp.New` 的可信授权回调复用原登录，不要求使用本独立账号方案。两种方式都不默认授权普通坐席。

下一步接管理员登录页和 FAQ 编辑/发布页面，验证会话过期、切号、冲突提示以及结果未知时先核对状态的用户流程。
