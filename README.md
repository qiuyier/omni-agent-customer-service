# omni-agent-customer-service

面向企业系统的渠道无关智能客服服务，使用 Go、Eino、Hertz 和
omni-agent-kit 构建。

## 当前阶段

第一阶段只实现会话领域模型和状态机，不连接大模型、HTTP、数据库或外部渠道。

这样安排是为了先固定以下业务规则：

- 机器人何时可以回复；
- 何时进入等待人工状态；
- 人工客服何时取得回复权；
- 会话关闭后禁止继续发送消息；
- 非法状态转换必须返回明确错误。

这些确定性规则由 Go 代码控制，不能交给大模型决定。

## 会话状态

~~~text
bot_active
  ├── RequestHandoff ──> waiting_human
  └── Close ───────────> closed

waiting_human
  ├── CancelHandoff ───> bot_active
  ├── AssignAgent ─────> human_active
  └── Close ───────────> closed

human_active
  └── Close ───────────> closed
~~~

不同状态允许的消息发送者：

| 状态 | 允许的发送者 |
|---|---|
| bot_active | customer、bot、system、tool |
| waiting_human | customer、system |
| human_active | customer、human_agent、system |
| closed | 无 |

## 本地验证

~~~bash
go test ./...
go test -race ./...
go vet ./...
~~~

下一阶段将在该领域模型之上实现会话应用服务和 Repository 接口。
