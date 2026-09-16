# 待人工处理记录的处置闭环

本轮在 `internal/integration` 内增加 `Inspect` 和 `Resolve`，仅供可信操作端调用。
不新增 HTTP 管理接口、页面或自动处置机器人，不改变客服协议、模型、数据库或业务规则。
操作者的认证与授权由调用方负责；`ActorID` 只是可信操作端提供的身份声明，不是鉴权。

## 查看与处置

`Inspect(ctx, jobID)` 返回当前 Job、该记录的审计历史和当前允许的动作。重点查看：

- `AttentionFrom` / `LastFailure`：停止阶段与固定错误分类。
- `Attempts`：当前阶段已消耗的尝试；`TotalAttempts`：所有阶段累计尝试。
- `DeliveryAttempts`：已保存的交付调用意图数，包括调用前崩溃等结果不明情况。
- `MayHaveDelivered`：可能已经发送，不能从后续资格拒绝或超时推断“肯定没发”。
- `LegacyHistory`：旧格式缺少完整历史，累计数是下界；0 不证明从未发生过调用。
- `Revision`：处置命令必须基于这个版本。`AllowedActions` 只是查看时的结果，提交时重新校验。

| 动作 | 允许条件 | 结果 |
| --- | --- | --- |
| `confirm_delivered` | 交付阶段的待处理记录，已存在交付键与可能发送标记；操作者已核对回执 | 标记 `completed`，不发送、不重新检查资格 |
| `stop` | 任意 `needs_attention` 记录 | 标记独立终态 `stopped`，保留失败原因、阶段及全部去重标识；不代表已送达，也不撤回先前发送 |
| `retry_once` | 新记录明确保留恢复许可，属于传输/超时/取消/繁忙/暂不可用/尝试预算类故障，且未用完人工追加次数 | 原阶段追加一次尝试，不立即调用外部端口 |

确认送达处理的是**过去发生的事实**，所以之后转人工或关闭不阻止它；必须先凭原交付键
核对真正的送达证据。库要求 `EvidenceRef` 非空，但不会联网验证回执，不能把填入引用
等同于验证成功。没有证据就不要确认送达。

资格过期、关闭、归属冲突、无效响应、未知故障、缺少检查能力、鉴权错误等不开放人工重试。
有限自动重试期间的未知错误也不自动获得人工恢复许可。超过支持范围的 Retry-After
保持禁止恢复，不能靠人工入口绕过等待策略。

## 使用顺序

1. 从 `List` 筛选 `needs_attention`，调用 `Inspect` 查看原因、历史和可执行动作。
2. 核对原事件、原回复和原交付键；调查是否已发送、故障是否已消除。
3. 提交 `Resolve(ctx, ResolutionCommand{...})`。必填 `OperationID`、`JobID`、
   `ExpectedRevision`、`Action`、`ActorID`、`Reason`、`EvidenceRef`。
4. `retry_once` 成功后，由现有调度方按照 `NextAttemptAt` 再调用 `Advance`；其他两个动作为终态。

示例（引用必须来自真实核对，不能直接照抄示例占位值）：

```go
view, err := adapter.Inspect(ctx, jobID)
if err != nil { return err }
_, err = adapter.Resolve(ctx, integration.ResolutionCommand{
    OperationID: operationID, // 操作端生成并持久保留，重试提交时不更换
    JobID: jobID,
    ExpectedRevision: view.Job.Revision,
    Action: integration.ConfirmDelivered,
    ActorID: authenticatedOperatorID,
    Reason: "已按原交付键核对渠道回执",
    EvidenceRef: verifiedReceiptReference,
})
return err
```

操作者 ID、操作 ID 等沿用非空、最多 256 字节的标识规则；原因与证据引用各最多
1024 字节。不要填入凭证、访问令牌、完整外部错误正文或无必要的客户个人信息。

## 重试与并发保障

- 同一状态目录内，`OperationID` 全局唯一。同 ID、完全相同命令可跨重开重放，返回
  原审计和**当前** Job，不重复追加审计、不再次执行处置。同 ID 改内容返回 `ErrOperationConflict`。
- 新操作必须匹配当前 `Revision`，否则 `ErrStaleRevision`，应重新查看而非自动刷新版本后重试。
  同一版本的两个不同处置命令，最多一个成功。无权执行的动作返回 `ErrResolutionNotAllowed`。
- 每次人工恢复只追加一次当前阶段尝试，每条 Job 总计最多 3 次人工追加；不清零
  `Attempts` 或 `TotalAttempts`，不改内容、触发消息 ID、事件 ID 或交付键。
- 追加尝试成功后可以正常进入下一阶段；失败则再次停到待处理，不自动启动另一批重试。
  调用前持久化 `ManualInFlight`，若进程在调用结果落盘前中断，重开后也停止等待复核，
  不因更改启动时的尝试上限而重新获得自动重试批次。
- 上游有效的 Retry-After 保存为 `RetryNotBefore`。人工追加后的执行时间不早于该值，
  重开也不会丢失等待约束。每次交付仍执行最新资格检查，不复用此前的允许结果。
- `completed`、`stopped` 及其去重标识全部保留；重复入站和 `Advance` 不会复活记录。
  终态无撤销/重新打开入口；人工追加额度耗尽后只能核对确认或停止。

## 审计、存储和迁移

每次成功处置保存命令、操作者、原因、证据引用、服务端 UTC 时间、原失败阶段与原因、
尝试数、交付不确定性、人工追加次数前后值以及目标状态和版本。
处置本身没有外部调用；状态与审计在同一快照中原子提交。保存失败沿用 `ErrStore`
及实例停止机制，修复存储并重开后再用同一命令重试，不手改状态文件。
拒绝的命令不写入成功处置历史；操作入口需要另行记录认证失败或越权尝试。

状态格式从 v1 升级为 v2。打开有效旧记录时保留原 ID，并先原子保存升级结果才提供服务。
旧交付阶段如果曾尝试过，保守标记为“可能已发送”；旧累计统计不完整，不从旧失败码推断
人工恢复许可。若以后出现一次新的明确可重试失败，可按新规则处理。
损坏状态或迁移写入失败会拒绝打开；旧程序不能读取 v2，升级前应按数据保留要求备份，
不要将 v2 文件直接交回旧程序或删除新字段降级。

Job 仍为值类型，审计在快照单独保存，查询返回独立切片；调用方修改返回结果不影响内部状态。
审计最多每条 Job 3 次恢复加 1 次终态处置；仍受 128 条 Job 的本地原型容量限制。
审计是本地明文、非防篡改日志，需可信目录、访问控制和备份；不是独立合规审计系统。
交付资格查询仍有检查与发送之间的并发窗口，见[接入适配器](integration-adapter.md#交付前资格校验)。

## 离线验证

```sh
CGO_ENABLED=0 go test ./internal/integration -count=1
go test -race ./internal/integration -count=1
```

测试使用虚构事件和模拟端口，覆盖送达确认丢失、确认后不重发、终止后不复活、三阶段受限恢复、
恢复时资格变化、重复/冲突/过期命令、并发处置、等待时间、额度耗尽、审计副本隔离、保存失败、
旧状态迁移和人工尝试中断。竞态检测需要本机可用的 CGO 工具链；这些测试不调用真实模型。
