# 新系统接入验收流程

当前闭环基线：`webchat-receipt-v1`，报告 `phases.lifecycle.baseline` 标识本版。新增只读回执的 accepted/unknown/冲突、跨客户、关闭后、旧凭据及退出拒绝检查。`lifecycle.paths.receipt` 默认 `/chat/v1/message-receipt`，可按业务同源路径配置。未支持此路由会失败，不自动降级或跳过；旧通过报告仍是历史证据，不能当作本版通过。

回执查询不重发消息；未知或失败应保留待确认。此 CLI 验证 HTTP 契约，不替代浏览器缓存恢复、断网和 PostgreSQL 重启验收。只读预检不调用回执或发送消息，仍保留原行为。

统一入口复用 preflight.mjs 和 verify.mjs，不修改客服核心，不依赖电商字段。适用于已实现本包协议的本机 Cookie 认证适配层，不是任意 URL 的自动接入器；CRMEB JWT 原入口仍需专门适配和验收。

## 1. 只读检查（默认）

通过权限受限的私有文件或 stdin 提供配置，不把密码、Cookie 或输出中的业务材料提交仓库：

```sh
node integrations/host-kit/acceptance.mjs < /private/path/acceptance-input.json
```

输入示例（端口与账号需替换）：

```json
{
  "preflight": {
    "origin": "http://127.0.0.1:12345",
    "paths": {
      "customerIdentity": "/auth/customer/me",
      "staffIdentity": "/auth/staff/me"
    }
  },
  "lifecycle": {
    "customers": [{"account":"test-a","password":"REPLACE"}, {"account":"test-b","password":"REPLACE"}],
    "staff": {"account":"test-staff","password":"REPLACE"},
    "paths": {
      "customerLogin":"/auth/customer/login",
      "customerLogout":"/auth/customer/logout",
      "staffLogin":"/auth/staff/login",
      "staffLogout":"/auth/staff/logout"
    }
  }
}
```

lifecycle 可省略。已有测试会话通过 preflight.sessions 提供，见[只读契约](preflight.md)。默认即使存在登录配置也不登录，不执行写操作。两阶段共享一个 origin；me/deskMe/queue 路径必须一致。工具不读取 .env，不自动获取账号。

## 2. 显式授权测试写入

仅使用隔离测试实例、fake 回复模式、已启用人工服务、两个可丢弃客户的空 bot_active 会话和测试坐席：

```sh
node integrations/host-kit/acceptance.mjs --allow-test-writes < /private/path/acceptance-input.json
```

只读检查失败则阻止写入；证据不足不是失败，可以继续授权的闭环。闭环会登录、发送、重放事件、转人工、接管、回复、已读、关闭、新建咨询并退出。不会清库或自动重试。失败可能留下已发送消息；注销失败计为失败，不能声称完全恢复测试前状态。回环地址与 fake 声明是防误操作措施，不是恶意服务安全保证。

## 3. 目标系统专项证据

以下项目必须在实际目标上验证，不接收手工传入的“pass”覆盖：

| 项目 | 操作与验收标准 | 现有参考测试 |
| --- | --- | --- |
| 切号/重新登录 | 保存旧 Cookie、版本与聊天凭据；换用户或同账号重登后，旧读取和写入都被拒绝；新账号不见旧数据 | business-login、office-portal acceptance.test.mjs |
| 禁用/恢复 | 业务认证系统禁用客户、坐席；旧凭据不能读写；恢复后旧登录不复活 | 同上；目标业务方提供禁用入口，不由通用工具猜测 |
| 浏览器 | 两个标签页退出/切号，迟到响应不显示旧身份内容；检查 Cookie、缓存、页面清理 | web/chat/src 的边界测试仅为回归，仍需目标浏览器实测 |
| 部署 | 持久化重启、真实模型、HTTPS及代理配置 | 按目标部署专项验收，不由 fake 内存样例背书 |

专项记录至少保留：目标版本/环境、测试时间、场景、预期、实际结果、脱敏证据位置及未验证原因。不要保存密码、Cookie、聊天令牌或客户正文。样例中的通过只证明样例，不自动转移到新系统。

办公门户已补充[2026-10-15 真实浏览器专项证据](../office-portal/browser-acceptance.md)，包含多标签退出/切号、禁用坐席与延迟历史请求取消。其他目标仍必须独立验证，统一工具不会据此把 browser 项自动改为 pass。

独立宿主已补充[2026-10-15 PostgreSQL 重启专项证据](../standalone-host/persistence-acceptance.md)，涵盖人工状态、归属、已读/未读、事件重试及关闭禁发。它使用模拟登录和 API 客户端，不代替目标业务认证、真实浏览器恢复或数据库备份恢复；deployment 项仍需目标独立验收。

## 结果与回归

办公门户另有[PostgreSQL + 真实口令登录浏览器恢复记录](../office-portal/persistence-acceptance.md)，覆盖网关重启、业务后端重启后的重新登录、待确认消息手动重试和切号隔离。仅为本地样例专项证据，不自动改变统一验收结果。

统一 JSON 含 checks 与 phases。fail / 退出码 1 表示失败；needs_verification / 退出码 2 表示尚有证据缺口；pass / 0 保留给全部完成。当前入口始终保留专项未验证项，所以自动闭环全通过仍返回 2，这是正常行为。必须检查 phases.lifecycle 判断闭环是否通过，不能把整体 2 当作闭环失败或全通过。此报告不是生产上线批准。

维修业务与办公门户使用不同登录字段和身份源，复用相同入口；已有专项测试继续执行：

```sh
node --test integrations/host-kit/*.test.mjs integrations/business-login/acceptance.test.mjs integrations/office-portal/acceptance.test.mjs integrations/cross-system/isolation.test.mjs
```
