# 通用接入交付基线：回执验收

2026-10-20，本轮完成。只修改仓库内验收工具、测试和文档；未修改客服核心、CRMEB 业务代码、真实数据库或部署配置，真实模型调用为 0。

## 解决的问题

回执接口已实现，但原统一验收器没有检查，部分接入文档也缺少路由。现在 `verifyHost` 输出 `baseline: "webchat-receipt-v1"`，统一入口保留在 `phases.lifecycle`。旧版本报告仍有效地记录旧范围，不能证明本版通过；缺失回执不会静默跳过。HTTP 闭环通过后，统一报告仍保留人工、浏览器和部署的 `needs_verification`。

## 验收内容

- 确认回执必须匹配当前会话、原事件和发送返回的消息编号。
- 未知事件返回 false 且不含消息编号；原事件内容冲突返回 409。
- 混用另一个客户的凭据拒绝；客户 B 用自己的凭据查询 A 的事件不得获知消息编号。
- 回执前后历史不变；关闭后仍可核对原消息且不重开会话。
- 新轮次旧凭据失效；退出业务登录后查询拒绝。
- 可配置 `lifecycle.paths.receipt`；所有执行路径仍清理测试登录。

## 本轮实际测试

```sh
node --test integrations/host-kit/*.test.mjs integrations/fresh-onboarding/acceptance.test.mjs integrations/office-portal/acceptance.test.mjs integrations/business-login/acceptance.test.mjs integrations/cross-system/isolation.test.mjs
```

结果：89 项通过，0 失败（Node 统计包含父测试）。使用临时回环 HTTP 宿主、fake 回复、Go 内存网关和虚构账号；临时进程与目录由测试清理。首次受沙箱限制网关未启动，获授权后在沙箱外运行以上回归通过。

新增测试将真实 HTTP 回执改成缺失路由、错误消息编号、未知泄漏编号、冲突误成功、跨客户泄漏、关闭后丢回执、注销仍成功，均被检测为失败。自定义路由成功路径统计 8 次回执查询、3 次发送请求（首次发送、同事件重试、关闭后预期拒绝）；回执查询没有触发额外发送。

非电商空白业务、办公门户、密码登录及跨来源隔离复用现有链路通过，不新增行业样例。真实 HTTP 不等于真实企业 IAM；这些样例身份系统仍为测试实现。

## 边界

未重新执行浏览器故障恢复、PostgreSQL 重启、真实 AI 或生产验收；不据此更新其历史结论。回执 accepted 仅表示已收件，未知不代表在途请求以后不会落库。前端未知/查询失败保留待确认，不自动重发。CRMEB JWT 仍走专用适配和验收，不强套本地 Cookie CLI。

## 阅读顺序

1. [接口职责](system-integration.md)与[统一验收说明](../integrations/host-kit/acceptance.md)。
2. [验收器](../integrations/host-kit/verify.mjs)与[故障测试](../integrations/host-kit/verify.test.mjs)。
3. [配置样板](../integrations/host-kit/starter/acceptance.example.json)与[交接表](../integrations/host-kit/starter/handoff.md)。

旧 `/v1` 契约保留历史及接口范围标记，不把它的实例级凭据套用到 WebChat。
