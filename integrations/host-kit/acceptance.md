# 新系统接入验收流程

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

## 结果与回归

统一 JSON 含 checks 与 phases。fail / 退出码 1 表示失败；needs_verification / 退出码 2 表示尚有证据缺口；pass / 0 保留给全部完成。当前入口始终保留专项未验证项，所以自动闭环全通过仍返回 2，这是正常行为。必须检查 phases.lifecycle 判断闭环是否通过，不能把整体 2 当作闭环失败或全通过。此报告不是生产上线批准。

维修业务与办公门户使用不同登录字段和身份源，复用相同入口；已有专项测试继续执行：

```sh
node --test integrations/host-kit/*.test.mjs integrations/business-login/acceptance.test.mjs integrations/office-portal/acceptance.test.mjs integrations/cross-system/isolation.test.mjs
```
