# 清单复核 v1：重复验证未通过

日期：2026-09-24。结论：候选未达到验收条件，不能视为漏判修复完成，也不接入正式客服。

## 本轮变化

首审提示明确区分“系统能做什么”和“入口是否已知”，禁止用知识局限片段吞掉实际功能声明。
清单复核候选独立读取原始资料和回答，枚举业务声明、证据关系及非业务内容，不读取首审结论。
它替换第二次调用而非增加第三层；严格检查字段、原文证据和覆盖，不静默删除非法字段。
原始清单单独保存为 `inventory_review_raw`，结构失败与语义结果分别记录。

这些是跨行业规则。没有修改 CRMEB，也没有修改在线客服处理链。

## 冻结与范围

使用 GLM `glm-5.3-flash`，reasoning effort 为 low。
固定六题、预先安排三轮，共 18 次样例执行；最多 36 次请求，实际 29 次。
不根据结果追加重试、不修改预期标签。错误、不确定均不计通过。

采样期间源码、提示和题目冻结；结束时源码哈希核对通过。
冻结记录（内部历史资料；已从公开历史移除，私密备份保留）、
逐次结果汇总（内部历史资料；已从公开历史移除，私密备份保留）、
审计记录（内部历史资料；已从公开历史移除，私密备份保留）保留。

当时冻结版本用 `-typed-primary` 选择候选。失败后仅增加显式入口隔离和对应离线测试，
现在须用 `-typed-primary -inventory-review` 才选择相同候选，报告为 schema 9、`typed_inventory_experiment`。
采样后没有调整候选提示、解析器或标签来改善结果，没有再次调用模型。
单独 `-typed-primary` 恢复使用原复核（schema 8）；首审语义澄清仍保留，
因此“新首审 + 原复核”组合本轮未做真实模型验收，不能声称完整恢复原基线表现。

## 结果

18 次中 11 次符合预期，2 次误报、1 次不确定、4 次错误。
负例共 6 次：5 次正确检出、1 次错误，没有判为 clear；错误不是检出。
正例共 12 次：6 次正确放行。三轮结果如下：

| 固定案例 | 预期 | 第一轮 | 第二轮 | 第三轮 |
| --- | --- | --- | --- | --- |
| device-effects | unsupported | unsupported | unsupported | unsupported |
| device-effects-documented | clear | unsupported | uncertain | error |
| maintenance-download-unknown | unsupported | unsupported | error | unsupported |
| maintenance-download-documented | clear | error | clear | clear |
| maintenance-capability-unknown | clear | unsupported | clear | clear |
| no-evidence-safe | clear | clear | clear | error |

设备负例三次均在首审检出，未进入清单复核；不能把该结果归功于新复核。
相同设备回答补足依据后却三次均未通过，说明候选不能稳定区分正负例。
重复验证门槛未通过，因此未追加完整二十题回归，也不与历史二十题成绩直接比较。

## 原始输出定位

- 第一轮设备正例（`1.json`）：复核把“助手不能查询/代执行”列为 not_stated 业务声明，造成误报。
  声明描述虽承认这是助手限制，结构中仍放入业务核验清单。
- 第一轮维修正例（`3.json`）：非事实片段携带 `assessment: null`，首审严格校验拒绝。
- 第一轮能力未知正例（`4.json`）：复核把明确“无法确认是否支持”的求证建议列为 not_stated，
  同时又列入 non_facts，导致误报；没有用代码替模型纠正语义分类。
- 第二轮设备正例（`7.json`）：遗漏过渡句和步骤编号，程序覆盖检查保留 uncertain，即使模型自行报告不确定为 false。
- 第二轮维修负例（`8.json`）：首审失败代码同为 `assessment_for_non_fact`，不计为正确检出。
- 第三轮设备正例（`13.json`）：non_facts 项含非法 `claim` 字段，复核结构错误。
- 第三轮安全正例（`17.json`）：助手不能代修改预约被列为 entailed，但证据为空，复核证据校验拒绝。

上述文件均位于 本轮归档目录（内部历史资料；已从公开历史移除，私密备份保留）。
输出证明了分类、覆盖和契约遵循问题，但不证明模型内部成因，也不能证明仅改提示即可解决。

## 工程验证与后续边界

隔离后 `env -u TEST_DATABASE_URL go test ./...`、grounding-check 的 race 测试、
`go vet ./cmd/grounding-check` 和 `git diff --check` 通过。
未配置真实测试数据库的测试通过不代表数据库集成验收；离线模拟复核否决的测试也不代表真实模型语义通过。

下一步应分开处理严格结构输出和业务声明分类两个问题，先用小规模固定正负配对验证。
不得用放宽覆盖、忽略未知字段或叠加第三次调用掩盖本轮失败；当前候选保持默认关闭。
