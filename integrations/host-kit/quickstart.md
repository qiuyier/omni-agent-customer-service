# 新系统接入：最短执行路径

本入口面向已有登录、尚无客服模块的 Node 业务。复用 host-kit，不创建另一套认证系统，不改 Go 核心。PHP/Java 等应实现等价边界或设计可信边界服务，不能直接复制 Node 装配函数。

## 接入方只需准备三组材料

1. **身份回调**：`sessions.authenticate(req, 'customer' | 'staff')`。必须从已有服务端认证取用户，不接受请求传来的用户编号或角色作为凭据。返回 null 或 `{role, enabled, subjectID, sessionID, expiresAt, displayName, version}`。ID 必须稳定；sessionID 为私有登录标识，退出/禁用失效，expiresAt 为毫秒；version 为独立的公开登录版本，不能泄漏 sessionID。字段限制见 [身份契约](README.md#最小后端装配)。
2. **配置**：私有 gatewayOrigin、hostSecret、授权 agents、真实 storage 和 replyMode。来源 source_id 在网关配置中定义，通过对应的 hostSecret 绑定，不能让浏览器指定。坐席 ID 与通过认证的员工 ID 对应，密钥分别独立。
3. **业务挂载点**：同源客服路由处理器、客户/员工身份查询及原登录退出、聊天页和工作台入口。挂载点必须能返回 Promise 并处理异步错误；原业务路由仍由业务负责。

不要提供运营资料或真实用户数据来启动接入。先固定 fake 回复，使用专用可丢弃账号验收；业务订单、退款及知识接入另行授权。

## 一次装配，复用原组件

```js
import {mountSupport} from './integrations/host-kit/onboarding.mjs';

const support = await mountSupport({
  config: {
    gatewayOrigin, // http://127.0.0.1:私有网关端口
    hostSecret,    // 服务端密钥管理注入，不得在页面/报告中出现
    agents,        // [{id, name, secret}]，仅已授权员工
    storage: 'postgres',
    replyMode: 'fake',
  },
  sessions: business.sessions,
  publicOrigin: () => business.origin,
  mount: handler => business.mount(handler),
});
// 业务框架调用 await handler(req, res)，true 表示已处理，false 继续原路由。
// 停止业务服务时 await support.close()；不会替你关闭业务服务器或数据库。
```

`business.mount` 是接入方的框架挂载方法，不是包提供的万能框架 API。一个完整可读实例见 [fresh-onboarding/support.mjs](../fresh-onboarding/support.mjs)。已有业务身份映射示例见 [business.mjs](../fresh-onboarding/business.mjs)，其测试认证服务不是生产 IAM。

新装配函数组合现有 `businessIdentity`、`startAdapter` 和 `createSupportProxy`：配置与回调先验证，后创建私有监听器；挂载抛错会关闭本次监听器且隐藏原异常。`close()` 可重复调用，关闭的是自己的适配器。它不会反向迁移业务路由或自动卸载框架中的 handler；若只关闭客服而保持业务运行，应由业务框架卸载路由。残留代理不能继续成功访问已关闭适配器。

配置必须显式给出，不加载 `.env`、不内置账号或默认密钥。本入口支持回环 HTTP + fake + 已授权人工坐席；新增显式 replyMode=ai 时必须选择 postgres，外层代理等待预算同步为 125 秒，fake 仍 20 秒。memory 仅用于固定回复的本地演练。语法校验不证明网关已运行或元数据真实；先按 [部署指南](../../docs/host-onboarding.md)完成资源检查，并保持网关/适配的模式一致。完整参考配置见 [显式 AI 入口](../../deploy/reference/AI.md)。远端部署、TLS、生产会话存储不在此便捷装配范围，不为跑通样例降低生产校验。

页面复用 [浏览器模板](browser.md) 中的 Chat、Desk、HostSessionBoundary。业务方配置真实身份/登录/退出路径与导航，确保 Cookie Path 覆盖 API。装配函数不安装前端，也不替代浏览器切号/退出验收。

## 选对验收入口

| 场景 | 工具与输入 | 默认行为 |
| --- | --- | --- |
| 新系统首次接入 | `host-kit/acceptance.mjs`，复制 `starter/preflight.example.json` | 只读，不登录/发消息 |
| 首次完整聊天闭环 | 同一入口，`starter/acceptance.example.json` + `--allow-test-writes` | 授权后使用两个干净客户会话和测试坐席，产生测试数据 |
| 已接入系统恢复后的检查 | `restore-acceptance/cli.mjs`，按目标适配器配置旧凭据和新登录 | 默认仅计划；不是首次接入入口，不自动导入/恢复或开流 |
| 浏览器、身份撤销、部署与重启 | `starter/handoff.md` 的目标专项证据 | 保持“待验证”，不能复制其他系统结论 |

首次接入的 Cookie/JSON 工具不支持任意 JWT、SSO/MFA、多步登录或强制版本头流程。原生 CRMEB JWT 使用其适配器；不能为通过验收删除目标业务安全校验。退出码 2/needs_verification 表示自动流程外仍有缺口，不是失败，也不是生产上线批准。

```sh
# 从仓库根运行，凭据填在仓库外权限受限的文件，不能放参数。
node integrations/host-kit/acceptance.mjs < /private/preflight.json
node integrations/host-kit/acceptance.mjs --allow-test-writes < /private/acceptance.json

# 独立复跑干净业务：随机测试账号、真实 Go 内存网关，无 Docker/模型调用。
node --test integrations/host-kit/onboarding.test.mjs integrations/host-kit/starter.test.mjs integrations/fresh-onboarding/acceptance.test.mjs
```

## 本轮证据与完成边界（2026-10-21）

上述定向测试 14 项通过（含父测试）：业务在挂载前可密码登录但无客服路由；挂载后同一登录可用；原 starter JSON 仅替换地址与登录字段即可完成只读检查和授权闭环；跨客户凭据/退出/错误登录版本拒绝；业务服务重建后需重登，稳定用户身份仍能读取原历史。

改动只有共享装配入口、装配测试和干净示例的支持接线。`fresh-onboarding/business.mjs`、通用适配器/代理、身份模板、Go 核心均未为本轮修改。不是从零再造业务认证。

这一轮使用真实 HTTP + Go 内存网关，未新增 PostgreSQL 备份恢复或浏览器证据。测试结束关闭自有监听器/进程并删除自己的临时构建文件，不动已有 CRMEB 或其他服务。

完成接入交付还要填写 [交接表](starter/handoff.md)，核对生产网络、Cookie、责任人、备份、监控及容量。技术样例通过不等于目标业务可上线。

阅读顺序：本文 → onboarding.mjs → identity.example.mjs → starter/acceptance.example.json → starter/handoff.md。
