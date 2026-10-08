# 固定双数组首审 v1：验收未通过

日期：2026-09-24。实验实现和验证已完成，但结构稳定性未修复，候选不进入正式客服。

## 修改范围

新增默认关闭的 `-split-primary`。业务事实统一使用 facts 数组中的固定字段，
非事实使用 non_facts 数组，不再在同种片段上条件增删 assessment。
语义规则复用 typed 提示；程序把结果转换后交给原 typed 和证据、覆盖校验，不修复模型判断。
非法字段（包括多余的 null assessment）仍拒绝，证据仍只能来自 reference。
复核使用原 specificity reviewer，不使用上一轮未通过的 inventory reviewer，不增加第三次调用。

这是提示与输出表示实验，不是服务端严格 Schema；它不能阻止模型选错数组、漏字段或编造枚举。
原 typed/inventory 模式保留，生产链与 CRMEB 未修改。

## 验证设计

沿用 grounding_inventory_cases.json 的六题及原预期标签，预先安排三轮，总共 18 次执行，
每题最多两次调用，实际 26 次请求。GLM glm-5.3-flash，reasoning effort low。
源码、二进制、提示、复核提示和案例哈希在调用前冻结，结束后源码哈希核对通过。
无重试、无中途改提示或标签。错误不计检出，也不计正确放行。

[冻结记录](eval_runs/2026-09-24/split-v1-repeated/freeze.json)与
[逐次汇总](eval_runs/2026-09-24/split-v1-repeated/summary.json)保存了全部执行信息；
各编号 JSON 保存首审原文 split_primary_raw，不能用转换后的 raw 替代原文分析。

## 结果

18 次中 13 次符合预期、5 次错误，零次最终误报、漏放或 uncertain。
“零漏放”不等于六个负例都检出：负例五次正确检出、一次错误；正例八次放行、四次错误。

| 案例 | 预期 | 第一轮 | 第二轮 | 第三轮 |
| --- | --- | --- | --- | --- |
| device-effects | unsupported | error | unsupported | unsupported |
| device-effects-documented | clear | error | error | error |
| maintenance-download-unknown | unsupported | unsupported | unsupported | unsupported |
| maintenance-download-documented | clear | clear | clear | clear |
| maintenance-capability-unknown | clear | clear | clear | clear |
| no-evidence-safe | clear | clear | clear | error |

设备有依据正例三轮均未通过，验收门槛失败，因此没有追加完整二十题。
不能把本轮 13/18 与上轮 11/18 的差异归因于双数组：本轮使用原独立复核，上轮使用清单复核，
且没有同期随机配对控制。固定样本结果不是新盲测或生产准确率。

## 五次失败的原始证据

全部失败发生于 primary / split_primary_parse；原文均为合法 JSON。

| 报告 | 分类 | 观察 |
| --- | --- | --- |
| 0.json | 字段契约 | non_facts 中出现非法 scope `system_fact? no`，reason 为 placeholder；其余内容也未完整覆盖 |
| 1.json | 证据边界 | 助手限制被放入 facts，引用系统提示“当前助手没有查询、提交、修改工具。”作为证据，但该句不在 reference 中 |
| 7.json | 字段契约 | non_facts 中出现 scope=system_fact，且 reason 为空；先被 fact_in_non_facts 拒绝 |
| 13.json | 字段契约 | 问候与过渡语片段漏掉 scope，转换后被 invalid_typed_scope 拒绝 |
| 17.json | 证据约束 | “我无法代您修改预约”被放入 facts，status=supported 但 evidence 为空 |

报告路径均位于 [归档目录](eval_runs/2026-09-24/split-v1-repeated)。
这里的错误类别表示首先触发的检查，并不代表原文没有其他问题。
同一模型可以在 reason 中解释“助手限制无需业务背书”，却仍将其放入 facts；
这说明可观察到的分类与结构矛盾仍存在，不能仅凭自然语言解释判为正确。

## 工程验证

- 新增等价性测试：支持/缺失/冲突证据、空证据、最终否决优先级与原 typed 结果一致。
- 新增非法字段、数组缺失/null、错误 scope 和缺失覆盖测试。
- 新增首审失败不重试、不调用复核，以及复核独立读取原文的测试。
- 新增离线零调用及模式互斥测试。
- `env -u TEST_DATABASE_URL go test ./...`、grounding-check race 测试、go vet 与 git diff --check 通过。

工程测试不代表真实模型通过；未配置真实数据库的运行也不代表数据库集成验收。
已有上一轮能力核查文档和历史回放测试保留，没有覆盖其他工作。

## 结论与下一步边界

候选保持默认关闭。不能通过自动补 scope、移动数组、删字段、放宽证据或再次加审查层宣称修复。
本轮不再追加提示迭代。下一项决策应先判断当前模型在固定契约任务上的可靠性是否足够：
若继续试验，应保持同一复核做受控对照，或在用户选定替代模型后做同题能力对照；
未经确认不更换供应商，不将失败候选用于在线拦截。
