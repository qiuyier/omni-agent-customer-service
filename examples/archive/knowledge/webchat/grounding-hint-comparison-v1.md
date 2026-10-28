# 固定分类提示有无对照：五条失败诊断（2026-09-23）

## 结论

本轮诊断完成，不是修复或上线验收。五条失败样例各运行有/无提示一次，实际十四次请求，
预算最多二十次，没有重新分类、自动重试或修改标签。
提供提示组零条符合冻结预期，不提供提示组两条符合；其余三条两组都未解决。
本次未观察到提示收益，但单次配对和失败样例选择不能证明分类提示导致退化，
更不能推算总体准确率。不启用组合模式或修改正式客服/CRMEB。

| 样例 | 预期 | 提供归档提示 | 不提供提示 |
| --- | --- | --- | --- |
| ticket-entry | unsupported | uncertain | uncertain |
| ticket-entry-documented | clear | unsupported | unsupported |
| no-evidence-safe | clear | error | error |
| no-knowledge-safe-guidance | clear | unsupported | clear |
| long-documented-guide | clear | error | clear |

两个 uncertain 不能计为成功检出，三个 error 不能计为可靠拒绝。

## 控制条件

- 只选上一轮组合验收的五条失败，回答、资料、历史、标签及归档分类原样提取。
- 两组模型均为 GLM `glm-5.3-flash`，`reasoning_effort=low`。
- 首审均使用同一个 assistedPrompt，复核均使用 specificityPrompt；
  仅首审 user JSON 的 classification_hint 有无不同。无提示组不是旧 v5 提示词基线。
- 不再次运行分类，固定上一轮分类意见。两组复核都不接收分类或首审结论。
- 各题两组先后顺序交替，五题分别为有先、无先、有先、无先、有先；没有声称随机分组。
- 每题每组最多首审加条件复核两次；十次独立样例运行均完整结束，错误记录未被重试。
- 在调用前冻结十个运行计划、源码、二进制、语料、提示和归档分类哈希；运行后核对一致。

## 已定位的失败行为

### 工单负例：仍是语义漏检，并非修复

两组首审都把“通过官方渠道提交工单”归入通用求证；复核 findings 均为空且 uncertain=false。
最终 uncertain 来自本地全文覆盖检查：均遗漏“我可以告诉您的信息是”，
有提示组还遗漏来源标注。因此没有直接 clear，但仍没发现真正无依据的渠道能力。

### 工单有依据正例：并列修饰范围的误判持续

两组都接受资料支持的工单提交，却把并列“联系人工客服处理账号问题”解释成
必须另有资料证明的渠道能力。按冻结的通用人工咨询标注，两组均为误判。
应明确这种并列句的业务验收口径；本轮不事后改标注来算通过。

### 无资料安全说明：两组都混淆输出类型

no-evidence-safe 的两组都把“我无法代您修改预约”写成 supported，却没有 reference 证据。
有提示组还把“资料未提供办理方式”写成 supported 空证据。
本地报 primary / judgment_parse / invalid_reference_quote，符合当前严格契约。
这是首审模型输出契约问题，不是网络故障，也不是必须放宽证据的理由。

### 另两条：提示组仍误用正确分类

- no-knowledge-safe-guidance：提示里已经是 self_limitation，首审却因 reference 为空，
  把“不能代登录”判 unsupported；无提示组将其作为非业务说明，复核通过。
- long-documented-guide：提示里已有 uncertainty/self_limitation，首审仍将未知入口与
  不能代办放入 supported，附空 evidence 而报错；无提示组本次通过。

这些结果说明“提供正确分类”不足以保证首审保持正确语义，且问题在无提示组也能复现。
不能凭这一轮断言准确的内部模型成因，也不能把两条无提示通过说成永久修好。

## 实现与离线验证

增加诊断用 `-replay-hints` 和 `-omit-replayed-hints`，默认不联网。
回放严格匹配 reference/history/answer，拒绝缺失、重复和非法分类；不修改原有判定规则。
schema 7 记录提示交付模式和归档哈希，两组都可保留归档分类作诊断，但仅有提示组发送它。

全量 `env -u TEST_DATABASE_URL go test ./...`、race、vet、diff 检查通过。
新增测试确认精确数据匹配、默认离线、开关互斥、两组仅改变提示、复核始终不接收提示。
没有连接真实业务数据库。

## 证据与阅读顺序

1. 本报告。
2. [固定五题](../../../regression/knowledge/webchat/grounding_hint_comparison_cases.json)与[原样归档分类](../../../regression/knowledge/webchat/grounding_hint_comparison_hints.json)。
3. `cmd/grounding-check/replay.go` 和 `replay_test.go`。
4. 冻结计划（内部历史资料；已从公开历史移除，私密备份保留）、
   十次结果摘要（内部历史资料；已从公开历史移除，私密备份保留）、
   条件核对与覆盖差异（内部历史资料；已从公开历史移除，私密备份保留）。
5. 同目录 `0-recorded.json` 至 `4-omitted.json` 是各题原始报告，含完整输入及模型原文。

## 下一步建议

不要增加审查层数或把分类改成硬免审规则。
优先简化首审的输出契约：区分“需要 reference 支持的业务事实”与“助手自身限制/资料局限”，
重点回归两组都复现的空证据错误，同时保留工单负例作为不能放过的反例。
对“提交/办理”和“一般求证”的边界单独固定验收口径，再决定是否继续组合方案。
这属于后续修改，本轮仅新增诊断能力和实测证据，没有宣称失败已修复。
