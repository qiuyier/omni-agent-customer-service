# 接入前只读检查

`preflight.mjs` 先检查配置和读取接口，不运行聊天业务验收。它只发送 GET；不会登录、退出、禁用账号、创建/续签聊天凭据、发消息、接管、标记已读或关闭咨询。没有开启写操作的隐藏开关。

当前仅接受 `http://127.0.0.1:端口`，用于授权的本地测试接入，不探测公网、localhost DNS或任意远端系统。配置的路径必须确实是业务方提供的只读接口；HTTP GET不自动保证一个错误实现的服务端无副作用。服务器日志、认证缓存等读取附带行为不等于零内部状态变化。

## 最小运行

把以下内容保存到私有配置文件，替换本机端口与身份路径：

```json
{
  "origin": "http://127.0.0.1:12345",
  "paths": {
    "customerIdentity": "/support/identity/member",
    "staffIdentity": "/support/identity/operator"
  }
}
```

```sh
node integrations/host-kit/preflight.mjs < /private/path/preflight-input.json
```

只从stdin读取JSON，不接受命令行凭据，不自动读取.env或浏览器Cookie，不保存报告文件。输出一份JSON报告；配置/网络错误也不会输出原输入、原异常或响应正文。
paths 的可选覆盖字段是 me、deskMe、queue，默认 /host/me、/desk/me、/desk/queue；五个路径必须不同，且为不含查询、片段、转义和点段的同源绝对路径。接口需符合[浏览器契约](browser.md)及宿主协议。

## 报告如何理解

每项包含 id、status、detail；成功取得JSON的传输项还记录httpStatus。需修复或人工确认的类别附 fix，指向身份映射、业务代理、坐席授权、缓存头、脱敏或专项验收。

- pass：该项在此次只读请求中满足预期，不代表整个系统可上线。
- fail：配置错误、连通性/格式异常、身份或权限不符等，先修复相应业务适配层。
- needs_verification：证据缺失，或无法用只读请求证明。不是“跳过后算通过”。

整体有任何fail则为fail（退出码1），否则有未验证项则为needs_verification（退出码2）；全部通过的通用退出码为0。**当前版本始终保留状态转换和浏览器/安全审查待验证项，因此正常完成只读检查通常退出2，不会产生“生产全通过”。** CI不能简单把2当作接口错误，也不能忽略报告中的未验证项。

匿名默认检查：公开身份格式/角色应为空、宿主元数据与非demo认证、匿名不能获得坐席或读取队列，以及响应禁止缓存/JSON格式/明显泄密特征。匿名模式不会连到网关的所有内部功能；需有效坐席的队列读取或显式业务验收才能进一步确认上游。

## 提供已有测试会话（可选）

`sessions` 支持 customer、staff、revokedCustomer、revokedStaff、unassignedStaff。每项为 `{cookie,expectedID}`，其中cookie为原始请求Cookie字符串，expectedID为业务适配后的稳定ID（不是显示名、原始编号或密码）。每个槽提供对应角色的独立测试Cookie，不混入另一角色的有效Cookie。未提供的槽明确标为待验证。

- customer/staff：已有有效登录。检查角色隔离、宿主ID/坐席ID与expectedID一致、队列权限，以及GET接口上当前版本接受/错误版本拒绝。
- revokedCustomer/revokedStaff：已在工具外撤销的旧登录Cookie。仅证明这些给定值目前不能恢复身份；不能证明工具执行过退出、禁用或该Cookie曾有效。
- unassignedStaff：有效员工登录但未配置客服权限，必须能查询自己的员工身份，却不能取得坐席或队列权限。

测试账号建立、取得Cookie和撤销由已有授权测试流程负责；工具不会自动做这些操作。可以由测试代码在内存中调用 `preflightHost(config)`，避免把Cookie写文件。若使用文件需限制访问权限，勿提交版本库、放入URL或粘贴到日志。工具仅支持当前简单Cookie格式，不是任意JWT/SSO认证插件，也不是完整浏览器Cookie jar。

所有请求有超时和64KiB响应上限，禁止重定向，不接受Set-Cookie来改变检查身份。有效坐席GET队列会读到业务数据，但工具只检查结构、不输出队列内容；请使用可丢弃测试账号。

## 不应扩大解释的结论

敏感字段、提供的Cookie长值（至少8字符）和明显堆栈检测是启发式；没有发现不代表任意未知秘密都不会泄露。仅规范化错误响应可通过错误格式检查。HTTPS、生产网络隔离、Cookie属性、浏览器标签页清理、写接口鉴权和撤销竞态另行验证。

只读检查完成后，需要完整流程时再显式运行现有 `verify.mjs --allow-test-writes`（见[接入包说明](README.md)）。该验收会产生测试消息和退出登录，不与只读工具合并；两者结果不能互相冒充。

## 测试与阅读顺序

先读本文 → preflight.mjs（输入、检查、脱敏报告）→ preflight.test.mjs（GET/CLI/失败场景）→ office-portal/acceptance.test.mjs（实际本地认证与Go网关联调）。

```sh
node --test integrations/host-kit/preflight.test.mjs integrations/office-portal/acceptance.test.mjs
```

测试中登录/切号由外围harness准备；通过记录检查器请求方法与路径、对比队列/身份前后状态，验证检查器自身未调用写接口。它不是使用测试预期替代真实HTTP检查。

本轮验收（2026-10-14）：新增10项工具测试和1项办公门户实服联调子场景；与既有回归合计109项通过，Go webchat/UI/webchat-server race检查通过。真实CLI测试确认只读正常但不完整时退出2；异常输入、越权、超时、超限和脱敏失败不会误报通过。本轮为命令行工具，不新增浏览器UI验收；未修改CRMEB或调用真实模型。
