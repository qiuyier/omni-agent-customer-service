# 共享同源客服代理

`createSupportProxy` 提取办公门户、空白业务重复的 HTTP 安全边界。它不创建登录、账号或页面，不改变私有 adapter 和 Go 客服核心。

```js
import {createSupportProxy} from './integrations/host-kit/proxy.mjs';

const proxy = createSupportProxy({
  adapterOrigin: adapter.origin,
  publicOrigin: () => business.origin,
  sessions, // 同一套经过真实验证的业务身份服务
});
// 放在宿主自身登录、退出、身份和静态页面路由之后：
if (await proxy(request, response)) return;
// 未匹配路径返回 false，宿主自行返回 404，不得再做通配代理。
```

参数使用 Node IncomingMessage / ServerResponse；当前仅接受规范的 `http://127.0.0.1:端口` 源（不带结尾斜杠）。publicOrigin 为函数，方便监听随机端口后获取真实地址；不得从请求 Host、Origin、X-Forwarded-* 推断。adapterOrigin 必须是 startAdapter 的私有地址，而不是网关或浏览器自报目标。

## 身份与请求约束

sessions.authenticate(request, role) 沿用身份模板字段：role、enabled、subjectID、sessionID、expiresAt，并额外提供公开 version。version 必须与公开身份查询接口一致，16–128 位字母/数字/下划线/连字符；不是 Cookie、sessionID 或其他凭据。代理在请求前后取得身份快照，撤销、切号、重登或版本变化时丢弃旧响应。认证异常只返回固定 503 错误。

为兼容现有非浏览器验收器，不带版本头的调用仍由真实业务登录和私有适配器鉴权；有头则严格验证，包括空值和重复值。不能单凭 version 授权。已经被网关接受的写操作不因之后退出而回滚。

只转发固定的 /host/me、/host/chat-session、/chat/v1/messages、/chat/v1/handoff、/chat/v1/consultations、/chat/v1/previous-consultation、/desk/me、/desk/queue、/desk/action 及各自允许方法。未知路径不转发；不支持 URL 重写或可配置任意上游。

已匹配请求自行验证 Host/Origin、跨站标志、POST JSON 类型和方法；POST 正文最多 32 KiB，上游 JSON 最多 1 MiB，禁止重定向。仅转发所需 Cookie、Authorization、JSON 类型和内部 Origin，不透传代理头。响应禁止缓存，错误脱敏。上游 JSON 内容必须来自可信 adapter，本模块不提供任意上游响应字段脱敏保证。

## 宿主仍然负责

- 原登录、退出、公开身份接口和其他业务路径的鉴权/同源检查；共享代理只保护匹配的客服路径，不能替代全站安全中间件。
- 真实身份服务的超时、撤销语义、坐席授权映射、Cookie 策略、监听器并发/HTTP 预算和关闭生命周期。
- startAdapter 的初始化和 close；本代理无独立监听器，不接管适配器生命周期。
- 私有网络隔离、HTTPS、可信反向代理和生产部署设计；本轮仍限回环样例。

timeoutMs 默认 20000，仅沿用 fake 样例预算，可显式配置 1–120000 毫秒。它限制上游 HTTP，不限制身份服务回调；AI 部署必须同时调整宿主 HTTP/反向代理预算，遵循 host-onboarding.md，不能只改本参数就宣称 AI 可用。

办公门户仍保留自身 member/operator 身份映射、原登录路径和页面；空白业务 support.mjs 仅负责创建 adapter、挂载代理和关闭。二者都没有复制通用代理逻辑，也没有加入电商字段。

运行专项：`node --test integrations/host-kit/proxy.test.mjs`。同时回归两个业务样例及既有 host-kit/浏览器状态测试；测试验证迟到响应、错误/重复版本、越界路径、外源、正文/响应超限、超时、重定向与异常脱敏。

## 2026-10-16 提取验收

新增代理专项 6 项全部通过；联合 web/chat、host-kit、office-portal、fresh-onboarding、business-login、standalone-host、cross-system 共 139 项通过，git diff --check 通过。两个样例通过真实 HTTP 和临时 Go 网关完成原人工闭环；办公门户覆盖登录版本、禁用与退出，空白业务覆盖适配器重建后恢复历史。

本轮没有改 Go 核心、原 adapter、共享页面、认证目录或 CRMEB，没有访问数据库或模型。没有新增真实浏览器或 PostgreSQL 重启验收；浏览器身份状态逻辑运行了自动回归，不把历史截图当作本轮手工验收。临时服务由测试 teardown 关闭。
