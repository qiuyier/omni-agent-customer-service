# CRMEB 客户身份桥接（PHP 7.4+）

最新人工接入与安装补充见 [2026-10-13 人工验收](Human-acceptance.md)。下文“未接人工”是早期阶段边界。

后续已完成[独立 H5 测试入口及真实 HTTP/浏览器验收](H5-acceptance.md)。
随后已进入[持久化测试部署](../../deploy/crmeb/README.md)，本机独立入口已启用；下文早期“尚未接线/未启用”是历史验收边界。

本目录是宿主适配层，不是客服核心，也不依赖 CRMEB 人工客服模块。
本轮仅新增四个 CRMEB 文件，默认关闭，不修改现有登录、退出、H5 入口或人工客服。

## 安装映射

| 本目录文件 | CRMEB 根目录内目标 |
| --- | --- |
| `Bridge.php` | `extend/omni_chat/Bridge.php` |
| `OmniChat.php` | `app/api/controller/OmniChat.php` |
| `route.php` | `app/api/route/omni_chat.php` |
| `config.php` | `app/api/config/omni_chat.php` |

安装前确认目标不存在；不要覆盖第三方或用户已修改的同名文件。本机四个文件已安装，通过私密实例配置启用。
优先读取商城 `runtime/omni-chat-deploy/config.json`；文件不存在时才回退到下列环境配置，默认仍关闭。
私密文件畸形时关闭；不得位于 public 目录或公开返回。实例配置还能声明 storage/reply_mode，控制器仅返回已知枚举值。
配置读取私有环境的 `[OMNI_CHAT]` 项：`enabled`、`origin`、`upstream`、`secret`、`allow_local_http`。
两个布尔开关必须为真正的 true。不要把密钥写进本目录或提交进版本库。

`origin` 是无尾斜杠、无路径的可信公开源；正式环境使用 HTTPS。
HTTP 仅供显式 `allow_local_http=true` 的可信本机测试网络；此标志不自动判断域名是否真的指向本机。
`upstream` 仅接受 `http://127.0.0.1:端口`，必须与 PHP 处于同一网络命名空间。
不能把 Mac 的回环地址误当成 Docker 容器的回环地址，也不要为接入而公开私有网关。
`secret` 为独立的 32–128 位字母/数字/下划线/连字符随机凭据，与网关 WEBCHAT_HOST_SECRET 一致，不能以 chat_ 开头。
来源 ID 由私有网关的宿主配置确定，不由浏览器指定；不同来源须使用不同宿主密钥。

## 新增接口

所有请求携带 CRMEB H5 的 `Authori-zation: Bearer <CRMEB token>`。
不接受另一份 `Authorization`，避免两种凭据解析歧义；禁止把令牌放 URL。
POST 必须携带与配置精确一致的 Origin 和 application/json；GET 若带 Origin 也必须一致。
精确检查 Host、拒绝 cross-site 请求与查询参数，不新增通配 CORS。

| 接口 | 请求 | 结果 |
| --- | --- | --- |
| POST `/api/omni-chat/session` | 空 JSON 对象 `{}` | `access_token`、`expires_at`、`conversation_id`、`storage`、`reply_mode` |
| GET `/api/omni-chat/messages` | 无 body；另带 `X-Omni-Chat-Token: <chat token>` | 本人历史 |
| POST `/api/omni-chat/messages` | 另带 chat token；仅 `event_id` 和 `content` | 通用网关消息结果 |

消息 event_id 限 1–128 字节字母/数字/下划线/连字符；content 限 4000 UTF-8 字节。
不接受 customer_id、uid、conversation_id、role 等身份/权限字段。错误使用 HTTP 状态码和固定 error 字段，
不是 CRMEB 通常的 `status=200` 包装；前端需显式适配，不要直接假设原 request.js 能解析。
浏览器只能把 chat token 放内存，不落日志、URL、localStorage 或 sessionStorage。
CRMEB 原 token 的现有存储方式本轮未改变；密码和 CRMEB token 不传入客服网关或模型。

## 身份与失效

控制器复用 `UserAuthServices::parseToken` 的签名、缓存和账号状态检查，额外要求 token 类型为 api。
只从认证结果读取正整数 uid。原认证会更新最近活动时间，不宣称无数据库写入。
通过私有网关 `/internal/chat/sessions` 用可信 uid 换取会话；来源/用户到会话的映射继续复用通用核心。

CRMEB 私有缓存按登录 token 的 HMAC 摘要绑定客服凭证，而不是只按 uid 绑定。
因此 B 登录态加 A 客服 token、同 uid 的另一登录 token 加旧客服 token 均拒绝。
缓存保存客服短期凭证供刷新复用，必须按凭据级别保护 Redis/文件缓存访问与备份。
本机私有运行目录用固定 64 个文件锁串行化同登录会话的签发；忙时返回 429。
缓存失败时不返回新凭证；上游超时、跳转、非 JSON、超大响应或非法签发结果均失败关闭。

每次消息请求都重新验证 CRMEB 登录态；上游执行完再验证一次，拦截执行过程中退出后的历史/回复返回。
原退出逻辑删除 token 缓存后，后续桥接请求即拒绝；无需修改原退出代码。
过期、禁用、删除的账号由宿主认证拒绝。PHP 7.4 兼容 Go RFC3339Nano 时间，去掉已校验的小数部分，
将过期时间向下取整，绝不延长有效期。

**边界：**已被网关接受的发送可能仍完成，退出不回滚消息或模型调用。
私有网关 token 本身未同步撤销，仍可能有效至过期，必须防止浏览器绕过宿主代理直接访问。
同一登录 token 若被窃取，本适配器不能区分攻击者；不是设备绑定或全设备登出系统。
本地文件锁不是跨副本分布式锁，当前验收仅覆盖单 PHP 主机/私有网关。
PHP 上游等待最多 120 秒；AI 模式占用 PHP worker 的容量与网关、FPM、反向代理超时需单独配置和验收。
没有接订单、支付、退款、人工工作台、跨来源知识隔离或生产部署。

## 2026-09-29 验收

PHP 7.4.33 的 27 项独立测试通过，覆盖：纳秒过期时间、刷新复用、跨用户及同 uid 不同登录凭证隔离、过期绑定、
并发签发忙、身份字段注入、无效 body、Origin/Host、冲突凭证头、失效登录、越权字段、
请求中退出、签发中退出、上游错误脱敏、禁用配置、上游地址限制、缓存失败关闭。

真实验收使用用户提供的两个本地账号，由标准输入传递凭据，不写入脚本或报告。
PHP74 内调用真实 CRMEB LoginServices、UserAuthServices、缓存、新增控制器和原生 logout，
连接同容器内独立 Go 通用内存网关（固定假回复，无外部模型）。19 项检查通过。
首次发现 PHP74 无法直接解析 Go 纳秒时间，已修复并加入纳秒格式回归。

每个账号用相同 event_id 发不同测试文字，历史各为自己的客户消息及假回复；混用凭证返回 401。
原生 logout 后换会话、读历史、发消息均 401，另一账号仍可访问。
测试专用登录及桥接缓存已删除，临时 Go 网关停止，随机宿主密钥文件删除。
登录/鉴权的正常活动时间等副作用不回滚，以免覆盖并发用户活动；不改密码、商品、订单或账号配置。
测试消息仅存在临时内存网关，停机后不保留。

原始结果见 `acceptance-report.json`。`live_check.php` 是固定本机路径的私有 CLI 验收辅助，
须与构建的 `gateway` 放在新建的私有临时目录，专门用于独立测试库，不能部署为网页或在生产重放。
临时网关源码为 `scripts/crmeb-identity-gateway/main.go`，仅回环监听、15 分钟自动停机、内存存储。
真实联调通过控制器调用完成：**未启用公开 HTTP 桥接，也未完成浏览器客服页面接线**。
额外实测公开 GET `/api/omni-chat/messages` 返回 404，配置仍关闭。

```sh
php integrations/crmeb/bridge_test.php
go test ./internal/webchat/... ./cmd/webchat-demo ./cmd/webchat-server
```

下一步是为 H5 增加独立测试入口及前端协议适配，明确私有网关部署位置后进行真实 HTTP/浏览器验收；
保留原客服入口，避免把当前后端联调通过表述为网页已接通。

阅读顺序：本说明 → `OmniChat.php` → `Bridge.php` → `bridge_test.php` → `acceptance-report.json`。
