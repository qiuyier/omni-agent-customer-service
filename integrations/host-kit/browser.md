# 通用业务页面接入模板

本模板复用客户 Chat、员工 Desk 及 HostSessionBoundary，不依赖商城或维修业务。当前是仓库内 React 19 / Tailwind 4 参考组件，不是已发布 SDK，也不意味着任何系统无需适配即可接入。

## 接入方负责什么

1. 保留自己的客户、员工登录机制；后端验证 Cookie/会话、到期时间、账号启用状态及角色。不要把浏览器提交的 ID 当作认证结果。
2. 按 [后端身份模板](identity.example.mjs)提供 sessions.authenticate(request, role)。员工还必须匹配服务端 agents 授权映射；有员工账号不等于有客服权限。宿主与坐席密钥仅留在服务端。
3. 提供两个同源身份查询接口，分别返回 `{identity:null}` 或 `{identity:{id,role,version}}`。role 为 customer/staff；id 为1–128位字母/数字/下划线/连字符；version 为16–128位同类字符，每次登录重新生成。version 是公开随机标识，不能使用原始 Cookie、认证令牌或后端 sessionID 代替。
4. 提供同源退出接口：POST JSON `{}`，撤销当前角色登录并清除 Cookie，成功返回204或200 JSON。禁用账号须使现有登录失效；重新启用不能复活旧会话。
5. 在业务代理中将每个请求的 X-Business-Session 与经过认证的当前登录 version 比较，不一致拒绝401。此标识本身不能授权。仍需转发前后验证登录、校验 Host/Origin、保护私有网关；前端隐藏页面不是访问控制。
6. 代理现有宿主聊天/工作台协议路径。当前 Chat/Desk 仍使用 /host/*、/chat/v1/*、/desk/*；任意业务接口字段、JWT/SSO或路由重写需要业务适配层，不是只换URL就能适配全部协议。

## 最小页面装配

配置放在模块作用域，客户、员工分别建立配置。路径仅允许同源绝对路径，不含查询参数、片段或跨域跳转；生产 SSO 可先指向自己的同源登录入口，再由业务层完成跳转。

```jsx
import {HostSessionBoundary} from '../../web/chat/src/host-session.jsx';
import {createHostSessionConfig,announceHostIdentityChange} from '../../web/chat/src/host-session.mjs';
import {Chat} from '../../web/chat/src/app.jsx';
import {Desk} from '../../web/chat/src/desk.jsx';

const customer=createHostSessionConfig({
  role:'customer',identityURL:'/portal/current',logoutURL:'/portal/logout',
  loginURL:'/sign-in',channelName:'my-system-auth',label:'业务服务',
});
const staff=createHostSessionConfig({
  role:'staff',identityURL:'/employees/current',logoutURL:'/employees/logout',
  loginURL:'/staff-sign-in',channelName:'my-system-auth',label:'业务服务',
});
export function CustomerPage(){
  return <HostSessionBoundary config={customer}>
    {({fetcher,storageScope})=><Chat businessAuthenticated fetcher={fetcher} storageScope={storageScope}/>}
  </HostSessionBoundary>;
}
export function StaffPage(){
  return <HostSessionBoundary config={staff}>
    {({request})=><Desk request={request} externalLoginURL={staff.loginURL} customerURL="/" hostLabel="业务系统"/>}
  </HostSessionBoundary>;
}
// Existing login page: ONLY after server confirms a successful login:
// announceHostIdentityChange(customer); then navigate to CustomerPage.
```

按实际文件位置调整 import。构建应扫描 host-session.jsx 的 Tailwind 类；参考 build-business.mjs 与 business.css。示例不引入新依赖，登录表单、品牌、首页路由和业务内容仍由接入方维护；Chat 中部分“示例站点”文案仍是验收页文案，不是最终白标产品。

## 组件负责什么

装配时还需注意：认证Cookie应覆盖 /host/*、/chat/v1/*、/desk/*（常见为 Path=/），不同角色不要覆盖彼此Cookie。当前Chat仍有返回根路径 / 的站点链接，接入方需提供有效根页面或同源跳转；并非所有导航和白标文案均可配置。

版本头策略必须明确：本地样例允许无 X-Business-Session 的非浏览器调用，但任何已提供的头都应严格验证，空值/重复/过期均拒绝。统一verifyHost不发送版本头，不涵盖浏览器版本隔离；业务若强制该头，需另配支持该协议的验收客户端，不要为了测试而在生产降级鉴权。

- 客户/员工共用同一身份边界：身份有效后才挂载业务子页面。
- focus/pageshow/visibilitychange 和约3秒可见页轮询重新核对；跨标签页通知只带角色、不带身份凭据。不同业务应使用不同 channelName。
- 身份变化时卸载旧页面、阻止旧请求及迟到结果；同账号重新登录也由新 version 隔离。配置变化同样重建边界。
- 返回 fetcher/request 给 Chat/Desk，固定同源请求并拒绝重定向；自动附加公开版本标识，保留原聊天 Bearer 头。
- 退出时先隐藏内容，退出失败仍保持隐藏并提示重试，不让轮询恢复旧页面。
- storageScope 按业务通道、角色、用户、登录版本隔离待确认消息。此版本将原维修页 business: 前缀改为通用分区；升级后旧待确认内容不会自动恢复或发送，历史仍从服务端读取。旧本地分区不做物理擦除。

边界：固定模拟回复只用于维修验收；前端模板不设置真实模型。页面卸载不回滚已接受的写操作；轮询不是即时撤销推送。模板不实现生产账号存储、HTTPS部署、MFA、分布式撤销或公开网关安全策略。

## 验证与阅读顺序

先读本文，再看 host-session.mjs 的配置与公开身份契约、host-session.jsx 的共享生命周期，最后看 business.jsx 的配置式装配。后端完整例子在 integrations/business-login/business.mjs。

```sh
node web/chat/build-business.mjs
node web/chat/build.mjs
node --test web/chat/src/*.test.mjs integrations/business-login/acceptance.test.mjs integrations/host-kit/adapter.test.mjs integrations/standalone-host/host.test.mjs integrations/cross-system/isolation.test.mjs
go test -race ./internal/webchat/... ./cmd/webchat-server
node integrations/business-login/browser-lab.mjs --allow-test-writes
```

## 本轮验收（2026-10-14）

另见[办公门户独立装配](../office-portal/README.md)：不同原生字段、角色和登录路径，只在业务侧映射，未修改共享组件。

- 59项前端测试与30项接入测试，合计89项通过；Go webchat、内嵌UI及webchat-server race回归通过。
- 浏览器使用新的共享边界完成客户密码登录、发消息、转人工、员工登录、接管回复、客户收到回复、关闭与发起新咨询。
- 第二客户标签切换 lin → zhou，旧页显示新身份且未发送草稿清空；退出后旧客户页只显示失效提示；员工禁用后工作台隐藏记录和操作。
- 页面保持原有布局及明确的加载/失效提示，未引入UI依赖。没有修改CRMEB、通用后端适配器或Go核心，没有调用真实模型。
- 不将本轮浏览器常规流程等同于网络竞态压测；旧请求/迟到响应仍由自动测试覆盖。账号与消息仅位于本轮临时内存服务，结束后停止并清理。
