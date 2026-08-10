# omni-agent-customer-service

面向企业系统的渠道无关智能客服服务，使用 Go、Eino、Hertz 和
omni-agent-kit 构建。

## 当前进度

第二阶段已经完成会话应用服务和 Repository 端口。目前仍不连接大模型、
HTTP、数据库或外部渠道，内存仓储只用于测试和本地开发。

第一阶段固定了以下业务规则：

- 机器人何时可以回复；
- 何时进入等待人工状态；
- 人工客服何时取得回复权；
- 会话关闭后禁止继续发送消息；
- 非法状态转换必须返回明确错误。

这些确定性规则由 Go 代码控制，不能交给大模型决定。

第二阶段在领域模型外增加了以下能力：

- 原子地创建会话并保存第一条客户消息；
- 根据会话状态返回下一步处理动作；
- 使用 `(channel, external_message_id)` 去重渠道重试；
- 相同幂等键携带不同数据时返回明确冲突；
- 会话、客户和渠道身份必须一致；
- 内存仓储支持事务回滚和并发安全访问。

## 分层结构

~~~text
未来的 Hertz / Eino / 数据库适配器
                  │
                  ▼
internal/customerservice   应用用例与流程编排
                  │
                  ▼
internal/conversation      领域模型、Repository 端口与内存实现
~~~

`customerservice.Service.ReceiveMessage` 只接收客户消息，不允许调用方传入
`Sender`，因此外部渠道不能借这个入口伪装成机器人、工具或人工客服。

首次消息按以下顺序处理：

~~~text
校验命令
  → 按渠道和外部消息 ID 查重
  → 在事务中查找或创建会话
  → 校验客户与渠道身份
  → 由领域模型检查发送权限
  → 创建并保存消息
  → 返回下一步动作
~~~

创建会话和保存第一条消息属于同一个事务：任何一步失败，两项修改都不会
提交。重复投递成功返回原消息 ID，但下一步动作为 `none`，避免再次调用
模型或重复通知人工客服。

应用服务返回的下一步动作：

| 会话状态 | 下一步动作 |
|---|---|
| bot_active | generate_bot_reply |
| waiting_human | wait_for_human |
| human_active | deliver_to_human |
| 重复投递 | none |

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

下一阶段将在应用服务之上定义机器人回复端口，并使用 Eino 实现第一个 AI
回复适配器。应用层仍只依赖接口，避免把 Eino 类型扩散到领域模型中。
