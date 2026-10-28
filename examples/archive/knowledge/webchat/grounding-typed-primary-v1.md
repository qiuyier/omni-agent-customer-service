# 按类型区分的首审契约：定向验收（2026-09-24）

## 结论

新契约实现与工程测试完成，七题定向验收六题符合预期、一题结构错误。
原来两条空证据安全正例、长回答正例和工单有依据正例均通过；新维修申请正反例均判对。
旧工单负例仍未修复，不能认为整项可靠性问题已解决，也不能上线。
本轮没有修改 CRMEB 或正式客服回复链路，新模式默认关闭。

| 样例 | 预期 | 本次结果 |
| --- | --- | --- |
| ticket-entry | unsupported | error：invalid_typed_scope |
| ticket-entry-documented | clear | clear |
| no-evidence-safe | clear | clear |
| no-knowledge-safe-guidance | clear | clear |
| long-documented-guide | clear | clear |
| repair-channel-unknown | unsupported | unsupported |
| repair-channel-documented | clear | clear |

七题采用独立的单题批次，共十二次实际请求，预算十四次；没有重试。
首题错误使该单题批次退出 1、complete=false，其余六个批次完成；不将失败批次描述为通过。
这是单轮开发样例回归，不是全二十题验收、重复采样稳定率或同条件因果对照。

## 改动

新开关 `-typed-primary` 替换实验首审契约，不增加分类调用，与旧的分类辅助/回放模式互斥。
首审输出 segments，使用六种 scope；业务事实只有 system_fact 一类能填写 assessment。
assistant_limitation、knowledge_limit、inquiry、social、correction 只描述片段及其类型，
不允许带 supported/unsupported 判定、证据或检查；连 assessment:null 也拒绝。

system_fact 的 supported 仍需逐字原文证据，事实细节检查和全文覆盖没有放宽。
通过新结构校验后仅转换表示，复用已有判分和独立全文复核，不自动修复或重分类。
错误类型也不会按别名容错接受。模型分错类型仍可能漏判，因此非事实片段不能免除独立全文复核。

首审实际输出保留为 typed_primary_raw；raw 为兼容转换后的结果与可选复核，不能混淆两者。
新报告 schema 8，结构错误带 primary / typed_primary_parse 诊断。

## 本次得到的改善证据

两条空资料安全回答把不能代办归为 assistant_limitation、资料未说明归为 knowledge_limit，
均没有生成 supported 空证据，复核通过。长回答亦正确分开未知入口与助手限制。
工单有依据正例接受了有明确资料的提交渠道；维修反例识别资料只有时间而没有提交渠道，
补充渠道依据的同一回答得到 clear，说明本次没有一概放过或拒绝操作指导。

这些只是本次观察，不能宣称已稳定消除所有空证据错误。
工单有依据正例的整句仍合并了提交工单与联系人工，细节只核验提交能力；
这与当前标签兼容，但并列语义的颗粒度并未由本轮证明稳定。

## 剩余工单失败：格式与语义两层问题

首审把“建议您自行通过官方渠道提交工单或联系人工客服处理账号问题”写成
scope=information_seeking，而新结构只允许 inquiry 等六种标签，所以严格解析报错，未运行复核。
新提示末尾复用了共享边界说明，其中仍出现旧 information_seeking 标签；
这是枚举混用的一个可见风险来源，不能仅凭单次输出断言模型内部原因。

更重要的是原始 reason 仍把“提交工单”解释成通用求证，没有识别未经支持的提交渠道。
所以即使只把标签改成 inquiry，也不能说语义问题已修复。
本轮不做别名映射、不修改旧标签、不把错误改计为成功检出。

## 冻结及工程验证

原五题从上一轮固定失败语料原样提取；新增维修配对题只改变 reference 是否提供渠道依据。
调用前冻结语料、首审/复核提示、main.go、typed.go、typed_test.go 与二进制哈希；
调用后检查源码未变。模型 GLM glm-5.3-flash，reasoning_effort=low。

- 全量 `env -u TEST_DATABASE_URL go test ./...`：通过，未连接真实业务数据库。
- `go test -race -timeout 60s ./cmd/grounding-check`、`go vet ./cmd/grounding-check`、`git diff --check`：通过。
- 新测试覆盖非事实禁止 assessment（含 null）、业务事实禁止空证据、缺失细节覆盖 supported、
  非法类型/字段拒绝、全文覆盖保留、复核否决错误分类、原始输出保留、首审失败不复核、离线与模式互斥。

证据：[固定七题](../../../regression/knowledge/webchat/grounding_typed_cases.json)、
冻结记录（内部历史资料；已从公开历史移除，私密备份保留）、
完整摘要（内部历史资料；已从公开历史移除，私密备份保留）。
同目录 0.json 至 6.json 为逐题原始报告，各自附带进度和退出记录。

## 下一步

新模式保持关闭。先消除新契约说明里的旧枚举混用，并针对“提交办理 / 咨询求证”的混合句验证语义边界；
不能仅修类型名便放行。该负例正确识别后，再跑原二十题完整回归。
本轮实现已交付，完整可靠性验收仍未完成。

阅读顺序：本报告 → `cmd/grounding-check/typed.go` → `typed_test.go` → 固定语料与原始结果。
