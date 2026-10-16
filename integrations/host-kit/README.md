# 可复用宿主接入包（仓库内参考实现）

多业务并行接入须核对[业务 Cookie 命名与隔离边界](cookie-isolation.md)：两个样例现已使用不同默认命名空间；这不替代生产安全域隔离。

同源业务服务器可复用[共享安全代理](proxy.md)，办公门户和空白业务已采用。登录/身份映射仍由业务方负责，不需要重复实现客服路由代理。

首次接入从[最小接入包](starter/README.md)开始：集中装配顺序、可复制验收 JSON、故障处理与目标交接表；复用本包，不新增认证系统。

新系统优先按[统一接入验收流程](acceptance.md)运行：默认只读，显式授权后复用业务闭环，单列切号/禁用、浏览器和部署证据缺口。

接入前先运行[只读检查](preflight.md)：不登录、不发消息、不关闭咨询，明确区分失败和证据不足；完整业务验收仍需显式授权写操作。

浏览器侧接入见[通用业务页面模板](browser.md)：客户与员工共用身份核对、切号/退出和请求版本隔离组件，维修样例通过配置使用。

后续已补充[业务密码登录的端到端验收](../business-login/README.md)：不启用 demoAccounts，不改本包或核心，由业务认证服务实现客户/员工验证、退出与禁用。

这是从已验收 Node 独立宿主提取的后端适配包，不依赖 CRMEB，不修改 Go 客服核心，不是已发布的 npm SDK 或开箱即用的生产账号系统。
现有独立样例通过三行包装显式开启演示账号，其他入口默认必须提供身份验证回调。

## 阅读顺序

1. 本文：怎么接、运行范围及安全边界。
2. [身份模板](identity.example.mjs)：唯一需要业务方实现的身份服务接口。
3. [适配器](adapter.mjs)：登录绑定、固定代理路由、坐席映射、迟到响应检查。
4. [统一验收器](verify.mjs)：地址、登录请求和路径通过配置提供。
5. [回调测试](adapter.test.mjs)、[非电商真实 HTTP 验收](../cross-system/isolation.test.mjs)。

## 最小后端装配

先按[网关配置、迁移与启动检查](../../docs/host-onboarding.md)准备独立网关和测试库，再装配业务后端。不可把业务生产数据库交给迁移脚本。

```js
import {startAdapter} from './integrations/host-kit/adapter.mjs';
import {businessIdentity} from './integrations/host-kit/identity.example.mjs';

const adapter = await startAdapter({
  gatewayOrigin, // 私有回环网关地址
  hostSecret,    // 由服务端密钥管理注入，绝不能来自浏览器
  storage: 'postgres',
  replyMode: 'fake',
  agents,        // [{id, name, secret}]，id 对应已授权业务员工
  identity: businessIdentity({sessions}),
});
// 退出进程时 await adapter.close()
```

这里的 sessions 是**你已有的业务认证服务**，不是本包创建的账号表。必须实现：
`sessions.authenticate(request, 'customer' | 'staff')`，返回 null 或经过真实认证的记录：

- role：严格等于所请求的 customer/staff；enabled 必须 true。
- subjectID：稳定用户或员工 ID，1–128 位字母、数字、下划线或连字符。其它格式需业务方做稳定映射，不能随请求随机变化。
- sessionID：16–512 字符的不透明登录会话标识，不能只用用户 ID；退出、切号、重新登录必须撤销/更换，避免旧登录复活。
- expiresAt：Unix 毫秒到期时间；不是秒。认证服务需设置自身超时，并及时反映禁用和退出。
- displayName：可选显示名，不是鉴权依据。
- version：使用共享 proxy 时额外提供，与浏览器公开身份接口一致的随机登录版本（16–128 位字母/数字/下划线/连字符）；不是私有 sessionID。直接使用 adapter 的身份模板不消费该字段。

直接调用 startAdapter 时 identity.customer / identity.staff 返回 `{id, name, sessionID, expiresAt}`。适配器在转发前和返回前调用验证，不凭前端提交的 customer_id、agent_id 授权。相同 sessionID 对应的用户发生变化会拒绝；不同登录的聊天凭据不能混用。
认证异常仅返回固定 authentication_unavailable，不把原异常或密钥发给客户端。

默认不暴露 /demo/login、/demo/logout、/desk/login、/desk/logout，也不挂载演示页面。业务方保留自己的登录/退出、页面入口及 Cookie；现有聊天与 Desk 组件可复用，但入口链接和请求路由需适配业务网站，不能把演示账号页直接上线。员工身份必须匹配服务端 agents 配置；客服密钥独立于宿主密钥。

只有本地样例显式传入 demoAccounts 时，才提供无密码选择与演示页面；demoAccounts 与 identity 不可同时配置。

## 配置化验收

代理装配顺序：先验证外部业务请求的Host/Origin，再向adapter.origin转发并设置内部Origin为adapter.origin（目标URL决定内部Host）；仅保留所需业务Cookie、聊天Authorization和受限请求体。不可把外部Origin原样送给私有适配器，也不可未经检查便重写成可信Origin。

验收器登录成功必须HTTP200；退出必须幂等返回200/204，因为finally会重复清理。其Cookie管理不是完整浏览器Cookie jar，也不会发送X-Business-Session，故不能替代Cookie Path/SameSite和浏览器版本隔离验收。额外验证员工能登录但未获agents授权时的拒绝行为。

验收器会发送测试消息、转人工、关闭咨询、创建新咨询并注销测试登录。**只用于授权的、可丢弃账号与 fresh bot_active 空会话**，不自动清库，不自动关闭已有业务咨询。
当前仅接受 http://127.0.0.1:端口，且要求服务端元数据 fake + human_enabled=true；这些是误操作保护，不是抵御恶意目标伪报模式的安全保证。

```sh
node integrations/host-kit/verify.mjs --allow-test-writes < /private/path/test-input.json
```

输入示例（请替换端口；示例账号来自独立宿主，不是生产凭据）：

```json
{
  "origin": "http://127.0.0.1:实际测试端口",
  "customers": [{"account":"lin"}, {"account":"zhou"}],
  "staff": {"account":"staff-1"},
  "paths": {
    "customerLogin": "/demo/login",
    "customerLogout": "/demo/logout",
    "staffLogin": "/desk/login",
    "staffLogout": "/desk/logout"
  }
}
```

customers 的两个对象和 staff 对象是目标登录接口需要的 JSON 请求体，可含目标测试密码；仅通过受限私有文件或 stdin 传入，不写代码、仓库、URL或报告。路径配置是同源绝对路径，不能指定另一个服务器。其余可配置键为 me/session/messages/handoff/next/deskMe/queue/action，默认采用通用宿主协议。

当前登录必须通过 Set-Cookie 维护会话，业务响应需遵循通用字段；工具不支持任意业务包装、JWT 登录头、SSO、验证码、多步 MFA、复杂 Cookie 路径或跨域跳转。
因此不能仅换路径就直接验证原 CRMEB JWT 入口；CRMEB 保留自己的真实验收脚本。测试密码、登录机制与协议映射由目标业务方负责。

输出只包含 passed、检查名称、失败阶段和注销清理失败数量，不输出 Cookie、令牌、密码、会话内容。无授权参数或非回环地址在联网前拒绝；非 fake、非空既有咨询不会进入发送闭环。
成功退出码 0，失败 1；失败不自动重试，保留已发生的测试消息方便排查。最终注销失败也计为失败，不宣称清理完成。不能通过报告判断供应商是否“恰好调用一次”。

## 验收覆盖与部署限制

配置化验收包括双客户登录/隔离、坐席登录、相同事件重放只保存一次、转人工/队列/接管/人工回复/已读、关闭禁发、新咨询空历史、旧凭据失效、客户与坐席退出。跨系统隔离由 cross-system 测试另行覆盖，不混称为 CLI 支持多个目标系统的一次验收。

适配器保留本地参考实现的限制：绑定随机或指定回环端口、Host/Origin 必须匹配实际监听源、凭据绑定在进程内、最多128登录/16并发公共请求；不是分布式认证、生产反向代理配置或容量保证。无需配置公开源就能任意反向代理的说法不成立。生产需要单独设计可信代理、HTTPS、Cookie策略、网络隔离、认证调用超时和持久化会话。

网关仍按一实例一宿主配置，PostgreSQL/模型/知识由网关管理，适配器不会自动迁移数据库或加载 .env。配置 storage/replyMode 只声明元数据，必须与实际网关一致。客服正文或身份可能交给已配置的模型；本轮只使用 fake。

## 2026-10-13 验收

提取前后原独立宿主及跨系统回归通过，合计20项 Node 测试通过，Go 网页网关与启动入口 race 回归通过；新增认证必填、角色隔离、撤销/迟到响应、认证异常脱敏、默认禁演示入口测试。
配置化验收在 repair-portal 非电商样例、实际 Go 内存网关上通过；没有复制 CRMEB 代码或改 Go 核心，未改当前 CRMEB 部署，也没有真实模型调用。
正常测试结束会关闭本轮监听器并删除自身临时构建目录，不影响常驻服务。未新增 UI，本轮不声称进行了新的浏览器视觉验收。

```sh
node --test integrations/host-kit/*.test.mjs integrations/standalone-host/host.test.mjs integrations/cross-system/isolation.test.mjs
go test -race ./internal/webchat/... ./cmd/webchat-server
```
