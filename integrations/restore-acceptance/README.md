# 恢复后的标准 HTTP 验收

第二种宿主已通过同一流程：[独立宿主验收说明](Standalone-acceptance.md)。CLI 支持 `crmeb` 与 `standalone-demo` 两种适配器；后者为 Cookie 示例登录，不是生产密码认证，也不是第二次备份恢复演练。

## 2026-10-21 验证结果

16 项自动测试通过（含中途人工回复失败后安全停止及拒绝盲目重跑）；与已有代理测试合跑共 18 项通过。真实恢复副本先证明三份旧登录可用，再运行工具，按预期在旧客户凭据检查处失败。隔离入口停流并轮换 5 条身份版本后，在同一副本连续运行两轮，每轮 17 项检查通过且三个登录注销成功；基线登录最后也已注销。见 [原始脱敏报告](real-report-2026-10-21.json)。本次无模型调用、无原服务/原库修改，外围临时服务由本轮操作者停止。未新增浏览器验收，报告明确标记 false；上一轮页面证据见 CRMEB 恢复 HTTP 报告。

通用流程 `core.mjs` 不依赖 CRMEB、数据库、容器名或本机目录；`crmeb.mjs` 负责 CRMEB 登录及接口映射。新增业务系统时实现同一适配协议，不将业务登录逻辑放进通用流程。

## 范围与安全边界

这是**恢复完成后的验收工具**，不是备份导入、权限轮换、服务编排或生产开放工具。数据库恢复及权限轮换仍使用业务系统自己的操作流程（CRMEB 见 `../crmeb/Restore-acceptance.md`）。工具始终返回 `support_may_open: false`，没有开放入口的代码或命令。

默认只解析配置并输出计划，不发请求。运行必须同时显式确认“独立恢复副本”和“公网入口保持关闭”。这两项是操作者承诺，不是工具完成的网络隔离证明；报告明确标记 `public_entry_state: operator_attested_not_verified`。只允许字面量 `http://127.0.0.1:显式端口`，禁止重定向、远端域名、URL 内凭据及路径。**回环地址本身不能证明后端是副本**，须先核对该端口绑定的数据库、缓存、签名密钥和来源映射，绝不能指向原环境。

只接受声明为 PostgreSQL/fake 的客服会话，检查发生在发送消息之前；运行期间禁止修改回复模式。使用专用的两个客户和一个客服测试账号，只发送随机标记的虚构消息。不能用真实用户的活跃咨询代替测试会话。

## 输入与调用

Node 原生模块，无第三方依赖。配置 JSON 从 stdin 读取，上限 64 KiB；不要把密码或 token 放在命令行。私有文件权限应为 0600，不提交 Git。不要启用 shell tracing。

```json
{
  "adapter": "crmeb",
  "environment": "isolated-restored-copy",
  "origin": "http://127.0.0.1:REPLACE_PORT",
  "timeout_ms": 10000,
  "accounts": {
    "a": {"account": "TEST_CUSTOMER_A", "password": "PRIVATE"},
    "b": {"account": "TEST_CUSTOMER_B", "password": "PRIVATE"},
    "staff": {"account": "TEST_STAFF", "password": "PRIVATE"}
  },
  "old_tokens": {"a": "OLD_CUSTOMER_A_TOKEN", "b": "OLD_CUSTOMER_B_TOKEN", "staff": "OLD_STAFF_TOKEN"}
}
```

`old_tokens` 应来自该副本停流/轮换之前、且已证明当时有效的真实登录，保存对应的轮换前证据。**随机无效 token 同样会被拒绝，工具不能仅凭 401 证明权限轮换有效**。默认计划不要求账号/token；执行时必须提供不同的客户账号和三份不同的旧 token。

```sh
# 默认离线计划；必须把示例端口替换为真实数值。
node integrations/restore-acceptance/cli.mjs < /private/restore-input.json
# 明确允许副本登录和虚构消息写入，公网入口始终关闭。
node integrations/restore-acceptance/cli.mjs --run --confirm-isolated-copy --confirm-public-entry-offline < /private/restore-input.json
node --test integrations/restore-acceptance/acceptance.test.mjs
```

stdout 为单份脱敏 JSON，可由调用方保存到新的报告文件；不输出服务端正文、异常信息、账号、凭据或聊天内容。成功退出 0，失败退出 1。失败阶段和清理失败分开报告。每个请求有超时及响应上限，不自动重试写入。

## 检查顺序及重复运行

旧客户/客服凭据拒绝 → 新密码登录 → 会话/凭据隔离 → 回执及相同事件去重 → 他人历史不可见 → 转人工/接管/回复/关闭 → 重读历史一致 → 注销本轮登录。

每次生成新事件标记。已有关闭咨询通过正常业务接口进入下一轮；已有人工处理中/等待人工咨询则失败停止，**不会为跑绿测试擅自关闭它**。在中途失败后先复核残留咨询再重跑，不删除历史，不回滚权限版本，不自动追加消息。

报告中的 `browser_verified: false` 是刻意保留的边界：重读 HTTP 历史不等于浏览器刷新验收，页面仍需单独验证。

## 资源与失败处理

当前 CLI 不创建容器、代理或数据库，因此不停止任何预存服务。它在成功或失败时都尝试注销所有已取得的本轮登录；一项注销失败仍继续其他项，并将整轮标记失败。网络中断导致登录响应丢失时，客户端可能无法获得 token 进行注销；负责人应通过副本缓存/身份轮换收尾，不把清理成功推断为服务器绝无残留。

调用方必须负责关闭自己创建的临时服务，并在失败、异常或进程被杀死时保持公网入口关闭。本工具的 `finally` 无法保证 SIGKILL、机器断电或外部服务管理器的行为；不存在“报告写了 false 就已经切断流量”的保证。

## 通用适配协议

`preflight`、`oldDenied(who)`、`login(who)`、`staff()`、`session(who)`、`crossDenied()`、`history(who)`、`newConsultation()`、`send(payload)`、`receipt(payload)`、`handoff()`、`desk(action,id,fields)`、`cleanup()`。

`who` 为测试角色 a/b/staff，不是业务用户 ID。会话、消息、回执和人工状态使用通用客服 API 字段。适配器必须将业务异常向上抛出，不能吞掉错误后返回空成功。`cleanup` 必须尝试清理其全部自有资源；通用 runner 保证调用它，但不会替适配器停止任意外部服务。
