# 从无客服基线开始的装配验收

2026-10-16 完成。目的不是再增加一个行业样例，而是检查[最小接入包](../host-kit/starter/README.md)在没有客服路由的业务基线上能否装配成功。

## 结论

无需修改 Go 核心、共享 adapter/identity/acceptance 或 Chat/Desk/HostSessionBoundary，即可挂载客户聊天和坐席工作台。接入包的验收 JSON 保持原路径，仅替换本轮地址和临时账号，自动闭环通过。

这不是无上下文第三方盲测：实施者了解仓库，并参考了既有契约和测试设施。复用办公样例的 legacy.mjs 作为已有密码认证替身，不冒充新的生产 IAM 或新技术栈。新增 business.mjs 只提供业务认证、身份查询和通用路由挂载点；客服逻辑单独在 support.mjs，挂载前后使用同一个业务服务和已有登录。

## 实际装配成本

| 部分 | 本次实现 | 如何解释 |
| --- | --- | --- |
| 原业务基线 | business.mjs，密码登录/退出、身份映射、公开版本、同源安全边界 | 为测试模拟“已有系统”；不是每个真实接入都要重写账号系统 |
| 客服后端胶水 | support.mjs，32 物理行，含接口白名单、版本检查、受限代理和转发后身份核对 | 不能说只需调用 startAdapter 一行；行数为紧凑样例源码，不是工期或生产代码量保证 |
| 页面装配 | fresh.jsx，25 物理行；其中有测试登录表单，核心聊天和工作台未复制 | 仍需构建、路由、静态资源、身份通知和原站导航 |
| 测试设施 | fixture.mjs、browser-lab.mjs、acceptance.test.mjs | 临时 Go 网关、随机账号、挂载/重建控制，不属于生产部署代码 |

## 自动验收证据

执行 `node --test integrations/fresh-onboarding/acceptance.test.mjs`，7 项（6 个子场景加父测试）：

- 基线可以密码登录；/host/me、/chat/v1/messages、/desk/queue、/chat/ 及私有/演示路径不存在。
- 挂载后原 Cookie 对应同一业务身份，伪造 customer_id 或角色字段被拒绝。
- 外源、私有路径、非法方法、超限正文拒绝。
- 读取 starter/acceptance.example.json，仅替换地址和账号；只读不运行闭环，显式允许后完成发送/重试/转人工/接管/回复/已读/关闭/新咨询/退出。
- 错误浏览器版本、旧登录及跨账号聊天凭据拒绝。
- 重建业务服务和适配器后旧凭据拒绝；重新登录恢复稳定用户 ID、同一咨询与消息。

统一报告仍为 needs_verification，不把此样例或手工记录注入工具以覆盖待验项。

最终联合回归：web/chat、host-kit、office-portal、fresh-onboarding、business-login、standalone-host、cross-system 合计 133 项全部通过；页面构建、14 个本地文档链接和 git diff --check 通过。临时浏览器标签页及本轮服务已关闭。未运行新的 Go race 专项，不能把以前结果算作本轮结果。

## 真实浏览器证据

本机回环临时环境，口令表单登录，真实 Go 内存网关，fake 回复。2026-10-15 开始，2026-10-16 完成：

1. 客户 lin 登录发送咨询，看到固定回复；申请人工后进入等待接管。
2. staff 从独立员工登录页进入工作台，选择咨询、接管、回复；客户页面看到人工回复。
3. 终端 restart-business 关闭并重新创建业务 HTTP 服务、身份目录和适配器；客户与员工旧页面均显示登录失效并隐藏记录。
4. 两端表单重新登录，客户找回原历史；坐席列表仍显示“由你处理”，打开后历史、咨询 ID 与归属一致。
5. 坐席关闭咨询；客户显示已结束且输入禁用。客户发起新咨询后历史为空。
6. A 发送隔离标记后切换到 B；B 为独立身份、空历史，未显示 A 标记。客户和员工退出后均回到各自登录页。

临时截图：`/tmp/omni-fresh-restored-20261016.jpg`（重建后坐席历史与归属）。截图仅含虚构测试内容；不保存登录口令或 Cookie。

## 发现的接入缺口

1. **适配器不是业务服务器插件**：它启动独立私有监听器，宿主必须有安全代理和生命周期管理。本例独立 support.mjs 展示这部分，不能只看身份回调模板估算工作量。
2. **页面路径不能随意改**：现有组件使用固定 /host/*、/chat/v1/*、/desk/*，还有根路径链接；接入方应先检查路由冲突，再做有边界的映射。
3. **仍有示例文案**：例如工作台“原客服入口保持不变”和聊天“示例站点”，对原本没有客服的业务不够准确。本次记录而不修改共享 UI 迁就验收，不能把页面直接称为完成白标交付。
4. **演示代理预算不是 AI 预算**：本例 20 秒代理只用于 fake，不能复制到真实 AI 链路；按 host-onboarding.md 配置完整超时链。
5. **基线 UI 与最终页面需区分**：启动器 attach 前只提供认证 API 和登录资源，登录后客服目的页尚未挂载；浏览器闭环应在 attach 后执行。原业务已有首页时应保留原导航，而不是复制这个测试入口。

上述要求已补充到接入包的装配前检查中。后续若简化代理，应基于这些重复责任设计独立能力，而不是改客服核心绕过鉴权。

## 运行与边界

```sh
node web/chat/build-fresh.mjs
node --test integrations/fresh-onboarding/acceptance.test.mjs
node integrations/fresh-onboarding/browser-lab.mjs --allow-test-writes
```

需已有 Go、Node 与 web/chat 依赖。启动器打印临时回环地址和随机账号，依次使用 attach、restart-business、stop；没有公开 HTTP 管理入口。attach 不更改业务登录会话；restart-business 则重新创建认证状态。stop 关闭本轮监听器与 Go 子进程、删除自身临时构建目录；仅丢弃本轮可丢弃内存账号和消息，不操作已有数据库。

本次重建在同一 Node 进程内完成，**没有 SIGKILL Node 或重启网关**。网关全程内存运行，未证明 PostgreSQL、数据库故障/备份恢复、跨登录待确认内容迁移、生产 SSO、HTTPS、真实 AI、多浏览器或移动端可用。旧办公门户持久化证据不能自动算作本目标通过；本目标仍需独立部署验收。

前端装配沿用设计技能要求的已有布局、标签与错误/加载状态，不加新 UI 依赖。本轮不是 UI 重设计或生产可用性审查。

阅读顺序：本文 → business.mjs（无客服基线）→ support.mjs（接入增量）→ web/chat/src/fresh.jsx（共享页面装配）→ acceptance.test.mjs（真实 HTTP 证据）。
