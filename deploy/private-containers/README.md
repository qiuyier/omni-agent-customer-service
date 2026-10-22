# 私有分容器接入实验

用于验证通用客服接入层跨容器运行，不依赖 CRMEB。复用干净业务的原生密码登录、共享 host-kit 挂载函数与正式 Go 网关；不改核心回环监听约束，不调用模型。

```text
本机 HTTP 客户端 → 127.0.0.1:随机端口 → 业务容器 edge
    → 原业务登录 / support 挂载层 → 本地通道 127.0.0.1:8094
    → 私有网络 mTLS gateway:8443 → 网关命名空间中的 TLS 通道
    → 网关 127.0.0.1:8093 → 专用 PostgreSQL
```

## 运行

需要 Docker、Go、Node.js（支持 fetch 和 getSetCookie）、OpenSSL。测试从当前源码交叉编译 Go 二进制，构建独立镜像；使用 `node:24-alpine`、`postgres:16-alpine`、`alpine:3.22`。依赖应提前准备好，首次缺少镜像可能需要网络。

在仓库根目录运行（报告路径必须不存在）：

```sh
node --test deploy/private-containers/*.test.mjs
node deploy/private-containers/check.mjs --allow-isolated-test-writes --report /tmp/private-containers-new-report.json
```

脚本会创建独立、随机名称的容器、镜像及网络；使用随机密码和虚构账号。只停止/重启本轮资源，不访问 CRMEB、不重启共享 PHP、不读取项目 `.env`。源代码挂载只包含明确列出的运行模块，不挂载仓库整体。

## 网络与凭据边界

- 业务容器连接 edge 网络及 internal 私有网络；edge 仅发布随机宿主回环端口。edge 不是互联网反向代理；默认不开放页面，显式 `--browser` 才提供已有登录页、聊天页和工作台。
- 网关与数据库仅连接 internal 网络，没有宿主端口。TLS 通道共享网关网络命名空间，也不发布端口。网关原始 HTTP 仅监听该命名空间回环，业务容器不能直连 8093。
- 通道使用本轮独立 CA、1 天有效期的服务端/客户端证书，校验服务器名 `gateway`，服务端额外固定客户端证书 SHA-256 指纹。CA 私钥从不挂载进容器。
- 服务端证书/私钥仅挂载 TLS 容器；客户端证书/私钥及业务凭据仅挂载业务容器。宿主密钥和坐席密钥仍由网关检查，mTLS 不替代业务授权。
- 只透传明确列出的精确路径和方法、Authorization 及 JSON；Cookie 不进入通道，不跟随重定向。有请求/响应大小限制及上游超时。
- 浏览器侧业务入口不开放 `/internal/*`，即使携带宿主密钥也不放行；没有业务登录不能取得聊天会话。

## 验收与结果

1. 检查端口发布边界。
2. 运行共享 `verifyHost`：双客户隔离、发送/回执/幂等、转人工、接管回复、关闭、新咨询及退出失效。
3. 验证缺证书、错误宿主密钥、直连网关回环端口均失败。
4. 停止通道，读取返回故障；恢复通道，原历史仅保留一份。
5. 重启网关：旧能力令牌失效，原业务登录重新取得令牌后，会话、消息及回执 ID 保留。
6. 删除本轮容器、网络、镜像、临时凭据与测试数据；报告仅保留脱敏检查结果。数据库使用 tmpfs，不用于持久部署。

单元测试额外验证：同一 CA 签发但指纹不匹配的客户端被拒绝、路径/方法精确匹配、Cookie/Set-Cookie 不透传、上游断开返回 503。

正常完成或捕获到失败时会清理。强制结束进程、宿主崩溃或 Docker 不可用可能留下 `omni-private-*` 资源；先确认本轮具体名称后再人工清理，不要删除其他环境。报告文件预先以独占方式创建，不覆盖已有报告。

## 不代表什么

这不是生产发布包：公共侧仍是本机 HTTP，证书为临时测试证书，未实现证书轮换、生产域名、业务会话持久化、备份恢复、容量验收或多宿主 AI 隔离。自动脚本证明 HTTP 协议与真实容器验收；打开浏览器实验环境不等于 UI 验收通过。脚本自身报告始终保留 `browser_verified: false`、`support_may_open: false`。

业务入口不限定电商。更换业务系统时替换原业务身份适配和业务查询能力；通道、客服核心、人工流程及隔离验收可以复用。

## 浏览器验收模式

在交互终端执行（非交互 stdin 会拒绝启动）：

```sh
node deploy/private-containers/check.mjs --allow-isolated-test-writes --report /tmp/private-browser-new.json --browser
```

先自动跑接口验收，再输出本轮回环网址和临时登录账号。账号密码只用于本轮，不进入报告或页面资源；不要将终端完整输出粘贴进文档。界面复用仓库已有 fresh-onboarding/public 构建产物，不改页面设计。

- 客户登录入口为输出的根地址，员工入口为 /staff-login；登录后分别进入 /chat/ 和 /desk/。
- 终端命令 disconnect / reconnect 仅停止、恢复本轮通道；restart-gateway 仅重启本轮网关及通道。
- audit 输出入口请求的方法、白名单路径和状态码，不记录 Cookie、Authorization、请求体或拒绝路径的查询参数。这是服务端观测，不是浏览器 HAR。
- records 只读显示以 BROWSER- 开头的虚构消息，用于核对页面与数据库。测试消息请使用该前缀，不输入真实个人资料。
- stop 结束实验并清理。失去交互进程或强制中止后，容器可能仍在；通过 Docker inspect 确认该轮名字、挂载目录和网络，再精确清理，不删除其他测试环境。

验收步骤：客户一发送标记 → 切换客户二确认无客户一内容并发送另一标记 → 切回客户一确认无客户二内容 → 申请人工、员工登录接管回复 → disconnect 后发送并确认待确认提示 → reconnect 后重试 → restart-gateway 后核对历史 → 坐席关闭 → 客户刷新看到关闭并发起空白新咨询 → records 核对消息唯一及归属。

页面资源保留正确 MIME，并返回 nosniff 和限制同源连接的 CSP。静态路径不支持 POST，不开放 .env、source map、任意路径或内部网关接口。

2026-10-22 已完成上述真实浏览器操作：[验收记录](../../docs/private-container-browser-acceptance.md)。这是本机假回复验收，不是生产发布结论。
