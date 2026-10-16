# 新系统最小接入包

目标：已有系统即使没有客服模块，也能复用客户聊天页、客服工作台和消息服务。业务方实现身份/路由适配，不修改通用核心。这里是仓库内接入材料，不是免配置安装器或生产认证系统。

## 先确认四件事

1. 客户与员工如何登录、退出、禁用？给出服务端验证会话的方法，不需要向文档提供密码。
2. 哪些员工有客服权限？确定稳定员工 ID 到坐席 ID 的映射。
3. 客户聊天页、员工工作台挂在哪两个同源地址？确认 Cookie 覆盖聊天 API 路径。
4. 谁负责客服专用 PostgreSQL、网关进程及密钥？不要使用业务生产库做迁移测试。

没有客服模块不是阻塞；没有可信客户/员工身份才需要先补认证。没有知识资料可先用 fake 回复，不需要运营资料或模型密钥。订单查询、退款、工单等业务工具不属于本包。

## 按顺序装配

| 步骤 | 复用材料 | 接入方需要完成 | 检查点 |
| --- | --- | --- | --- |
| 1. 私有网关 | [配置模板](../../../deploy/webchat-server.env.example)、[部署说明](../../../docs/host-onboarding.md) | 独立来源、服务端密钥、专用库；明确授权迁移 | -check-config、-check-resources 均通过，先 fake |
| 2. 身份适配 | [identity.example.mjs](../identity.example.mjs)、[后端装配示例](../README.md#最小后端装配) | 实现 sessions.authenticate；稳定 ID、角色、启用状态、登录会话及毫秒到期时间 | 匿名/禁用拒绝，重新登录换 sessionID；员工需显式坐席授权 |
| 3. 同源业务接口 | [接口契约](../../../docs/system-integration.md)、[浏览器契约](../browser.md) | 原登录/退出、两角色身份查询、精确代理白名单；校验 Origin 与登录版本 | 不透出宿主/坐席密钥；转发前后验证身份；未知路径拒绝 |
| 4. 页面 | [最小 JSX 装配](../browser.md#最小页面装配) | 挂载 Chat、Desk、HostSessionBoundary，配置身份/退出/登录 URL | 使用边界提供的请求函数，不绕过身份隔离；根路径导航有效 |
| 5. 自动验收 | 本目录 JSON 样板、现有 acceptance.mjs | 私下填入测试 URL/登录字段，先只读再授权写入 | 查看 checks 与 phases，不只看退出码 |
| 6. 目标专项 | [交接表](handoff.md) | 真浏览器、撤销、重启、部署及可选 AI 验收 | 每项附本系统证据；其他样例通过不代填 |

Node 后端可直接复用适配器。PHP/Java 等可按契约实现等价适配，或设计受保护的 Node 边界服务；不能因为有模板就声称无需适配。当前网关/适配器为回环部署，一实例一宿主；跨容器网络、生产会话存储和任意多租户并不由本包自动提供。

后端装配不创建业务登录页或账号表。浏览器公开 version 不是后端 sessionID，也不是认证凭据；X-Business-Session 只能与真实认证一起校验。业务若强制该头，现有写验收器不支持，需专项客户端，不能为通过测试关闭生产校验。

## 运行验收

从仓库根运行。把 [只读样板](preflight.example.json) 或 [闭环样板](acceptance.example.json) 复制到仓库外权限受限的文件；端口、路径、登录字段均须替换。不要把填好的凭据提交仓库。只读样板也由统一 acceptance.mjs 读取（外层含 preflight），不是直接给 preflight.mjs。

```sh
node integrations/host-kit/acceptance.mjs < /private/path/preflight.json
# 仅对授权的本机 fake 环境、两个可丢弃空会话客户和测试坐席执行：
node integrations/host-kit/acceptance.mjs --allow-test-writes < /private/path/acceptance.json
```

默认不登录、不发送、不注销。授权闭环会产生消息、接管、关闭、新建咨询并注销；失败也可能留下测试数据，不清库、不自动重试。CLI 仅支持本机回环、简单 Cookie/JSON 登录，不支持原生 JWT、SSO/MFA 自动登录。

| 结果 | 下一步 |
| --- | --- |
| fail / 退出 1 | 先看 configuration 与 phases.preflight 的 fix；修配置、认证或代理。只读失败时不会进入写入。闭环失败看 phases.lifecycle，核对测试状态与注销清理 |
| needs_verification / 退出 2 | 不等于失败，也不等于通过。补缺失测试会话或按交接表人工验收；phases.lifecycle.passed=true 只证明自动闭环 |
| pass / 退出 0 | 仅代表该工具已检查项目；当前统一入口始终保留目标专项未验证项，不会产出上线批准 |

## 装配前不要遗漏

后续已提供[共享安全代理](../proxy.md)：Node 同源宿主可以复用白名单、版本校验、正文限制与转发后身份核对，不再逐项复制。仍需创建/关闭 adapter、挂载处理器，并保护登录及其他非客服路由；非 Node 系统按契约实现等价边界。

[从无客服基线开始的实际验收](../../fresh-onboarding/README.md)证明本样板可复用，但也确认了以下成本：startAdapter 启动的是私有监听器，不会自动挂到现有业务服务器；必须补同源代理、精确路由白名单、登录版本及转发前后身份检查，并管理关闭生命周期。

先检查 /host/*、/chat/v1/*、/desk/* 与根路径导航是否与原站冲突。共享页面目前仍有示例文案，不能把完成技术闭环等同于白标交付。示例 20 秒 fake 代理不能直接用于 AI；真实 AI 超时链以 [部署说明](../../../docs/host-onboarding.md)为准。业务适配器重建后恢复历史也不证明网关/数据库重启恢复，交接表要写明究竟重启了哪一层。

## 完成标准与阅读顺序

完成适配需跑通真实业务登录→客户发送→人工接管回复→关闭/新咨询，确认双客户隔离和撤销，填写目标浏览器与重启恢复证据。生产开放还需 HTTPS、网络、备份恢复、容量和运行责任确认；AI 另经授权验收。保持当前仍有缺口的项目为“待验证/阻塞”，不可抄写办公门户通过结论。

阅读顺序：本文 → identity.example.mjs → browser.md → acceptance.example.json → handoff.md。需要完整字段差异实例再看 [办公门户](../../office-portal/README.md)，其 legacy.mjs 仅为本地认证替身，不应复制成生产 IAM。
