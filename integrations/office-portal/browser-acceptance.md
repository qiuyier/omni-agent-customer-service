# 多标签页与撤销验收 · 2026-10-15

范围：本机办公门户样例、真实 Codex 内置浏览器、独立内存 Go 网关、fake 回复。使用启动器随机生成的可丢弃账号；未访问 CRMEB、真实业务数据库或模型。本报告不代表任意系统或生产部署已验收。

## 结果

| 场景 | 实际操作与观察 | 结果 |
| --- | --- | --- |
| 跨标签切号 | A（MEM:731）发送独有测试消息并留下草稿；第二标签登录 B（MEM:864）；旧标签显示 B，消息与草稿清空 | 通过 |
| 跨标签客户退出 | 第二标签点击退出；旧聊天标签显示“未显示身份/登录已失效”，无消息输入框 | 通过 |
| 跨标签坐席退出 | 坐席接管咨询并填写未发送草稿；第二工作台退出；旧工作台不再显示历史、回复框和关闭按钮 | 通过 |
| 角色隔离 | 坐席退出后客户标签仍为 MEM:731，能显示自己的原会话 | 通过 |
| 后台禁用坐席 | 重新登录并打开已接管咨询；终端禁用 operator staff；两个工作台均显示身份失效，操作控件消失 | 通过 |
| 延迟旧历史响应 | 暂扣 A 的完整历史响应；另一标签登录 B；放行后旧标签仍是 B 的空历史，A 的唯一标记不存在 | 通过：浏览器取消路径 |

延迟场景日志依次为 armed → captured → cancelled_before_release。实际观察到的是切号时请求已被取消，**不是**旧正文成功送达新页面后再被丢弃。首次尝试在手动刷新后请求取消，随后自动放行，未作为切号成功证据；第二次在 captured 后立即登录 B，再执行 release，作为本表证据。

补充自动证据：

- host-session.test.mjs：模拟旧请求随后返回 200，包装器抛 AbortError，不交付旧数据。
- business-session.test.mjs：迟到 401 不得让新身份失效。
- office-portal/acceptance.test.mjs：旧坐席 Cookie 在退出或禁用/恢复后，claim、reply、close 均得到 401；客户身份不受坐席退出影响。浏览器中控件隐藏不能代替这些服务端拒绝断言。

本次未发现需要修改共享身份组件的缺陷；新增的是可重复的延迟测试装置与证据。后台禁用依赖现有轮询/下次请求发现，不声称零延迟推送。未测多浏览器内核、移动端、SSO、断网恢复、生产 Cookie/HTTPS，以及已收到响应头后慢速正文的真实浏览器场景。

本轮自动回归 122 项通过，git diff --check 通过。结束后两个临时环境均正常 stop 退出，临时构建目录由启动器清理；本轮浏览器标签已关闭。未修改正式页面、业务适配器或 Go 核心。

~~~sh
node --test --test-reporter=dot web/chat/src/*.test.mjs integrations/host-kit/*.test.mjs integrations/office-portal/*.test.mjs integrations/business-login/acceptance.test.mjs integrations/standalone-host/host.test.mjs integrations/cross-system/isolation.test.mjs
~~~

## 复现

~~~sh
node web/chat/build-office.mjs
node integrations/office-portal/browser-lab.mjs --allow-test-writes
~~~

1. 使用输出的回环 URL 与临时账号，开两个成员标签，登录 A，发送唯一标记。
2. 第二标签打开成员登录页，预填 B，但先不提交。
3. 终端输入 hold；保持 A 聊天可见，等待自动读取（约 5 秒），确认终端出现 captured。不要再手动刷新取消该请求。
4. 立即在第二标签登录 B；终端输入 release。检查两个标签都是 B，且不含 A 标记与草稿。
5. 终端 status 应显示无 armed/held。单次暂扣最长 120 秒；自动放行仅防资源悬挂，不作为切号验证。
6. 进行客户/坐席多标签退出；坐席已登录时输入 disable operator staff，检查工作台清空。enable 仅恢复登录资格，不恢复旧登录。
7. 输入 stop 停止测试服务，清理该启动器的临时构建与内存数据。

代理只在此浏览器测试启动器使用，控制仅走终端，不新增 HTTP 管理入口；只暂扣下一条 GET /chat/v1/messages，响应上限 1 MiB，日志仅固定事件名，不记录 Cookie、口令或正文。它验证外部 Host/Origin 后重写内部源，不改变正式 portal 或客服核心。其他请求保持原协议，不能将此装置部署到生产。

截图为本轮临时本机证据（不含口令/Cookie）：

- /tmp/omni-office-account-switch-20261015.jpg：延迟场景后 B 的空历史。
- /tmp/omni-office-staff-revoked-20261015.jpg：禁用后工作台身份失效。

临时文件可能被系统清理；持续审计时应由项目方选择受控存储位置。
