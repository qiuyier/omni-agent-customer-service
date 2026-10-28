# 首审契约与完整/局部回答对照 v1

日期：2026-09-26。诊断完成，未实施修复，也不构成上线验收。

## 设计与边界

固定 GLM glm-5.3-flash、reasoning effort low、原 specificity 独立复核、输入资料和历史。
对比现有 `-typed-primary` 与 `-split-primary`，不使用 inventory 复核；未改任何 Go 源码或提示。
首审契约及表达契约的提示本来就不同，因此这是两个候选整体对照，不是纯 JSON 排版的因果实验。

使用原设备回答（224 个 JavaScript 字符）的有依据/无依据正负配对，再摘取连续原文：

- 查询能力与入口未知混合句（51 字符），保留正负资料配对。
- 助手限制片段（48 字符），使用有依据版本的相同资料和历史，预期 clear。
- 密码重置过渡语及编号步骤（75 字符），使用有依据版本的相同资料和历史，预期 clear。

片段没有改写，也未重新生成回复；“这类信息”的指代保留原历史辅助理解。
这些是局部输入诊断，不是生产分段方案，没有测试分段后的上下文传递或合并策略。
截取同时改变长度与混合语义复杂度，不能单独证明长度是原因。

共六种输入，两种首审，各两轮，共 24 次执行。每个案例第一轮 typed→split，第二轮 split→typed，
减轻固定先后顺序影响，但不是随机化或统计显著性检验。最多 48 次请求，实际 38 次；
首审拒绝或检出 unsupported 会跳过复核，所以固定的是复核器，而非强制相同调用次数。
没有重试，没有按结果改提示/标签，没有追加样例或完整二十题。

## 结果

| 首审 | 全部符合预期 | 完整回答 | 原文短片段 | 字段契约错误 | 实际漏放 |
| --- | --- | --- | --- | --- | --- |
| 原 typed | 11/12 | 4/4 | 7/8 | 1 | 0 |
| 双数组 split | 8/12 | 0/4 | 8/8 | 3 | 1 |

合计 19/24 符合预期，四次 error、一次无依据回答被判 clear。没有最终误报或 uncertain，
没有以证据错误为首个失败代码的执行；不代表所有被字段错误提前拒绝的输出都无证据/语义问题。

| 输入 | typed 第一轮/第二轮 | split 第一轮/第二轮 |
| --- | --- | --- |
| 完整负例 | unsupported / unsupported | clear / error |
| 完整正例 | clear / clear | error / error |
| 能力短片段负例 | unsupported / unsupported | unsupported / unsupported |
| 能力短片段正例 | clear / error | clear / clear |
| 助手限制短片段 | clear / clear | clear / clear |
| 有依据步骤短片段 | clear / clear | clear / clear |

## 五个失败输出

归档路径：`eval_runs/2026-09-26/controlled-contract-length-v1/`。下列原始输出均为合法 JSON。

1. `1.json`：split 完整负例把“通过账号安全功能自行查看”与“入口未知”整句标为 knowledge_limit。
   首审仅核验重置密码步骤，复核返回空 findings、uncertain=false，最终 clear，形成实际漏放。
2. `3.json`：split 完整正例把 system_fact 放入 non_facts，额外携带 status/evidence/checks，
   被 invalid_split_primary 拒绝。另有未出现在回答中的自造 quote，不能因前一错误忽略此风险。
3. `12.json`：split 完整负例的 non_facts 包含 system_fact，reason 为 X，
   被 fact_in_non_facts 拒绝。错误不能算作正确检出。
4. `14.json`：split 完整正例的 non_facts 步骤项出现 status:null，被 invalid_split_primary 拒绝。
   同时把助手限制放入 facts、把操作步骤标成 inquiry，说明仍有语义矛盾。
5. `19.json`：typed 的 51 字符有依据短片段中，knowledge_limit 携带 assessment:null，
   被 assessment_for_non_fact 拒绝。证明短片段也未消除条件字段错误。

## 结论与决策

本批同模型同复核的证据不支持用 split 替换 typed；双数组后续优先级降低，仍保持默认关闭。
typed 暂作为后续诊断基线，不代表它可上线：本轮仍失败一次，历史还有漏判和结构错误。
split 在短片段上的本次表现更好，但不能据此上线分段审核；两个重复、单一原始案例家族，
既不足以确认长期稳定，也不足以推断跨行业准确率或模型整体能力上限。

更准确的定位是：完整混合回答与 split 契约组合在本批表现较差，同时独立的条件字段问题仍会出现在短输入。
不是“只要缩短回答就好了”，也没有证据表明增加审核层能够解决。
若继续投入，建议在用户确定可用模型后做相同输入和契约的模型对照，先比较能力与成本；
不自动更换供应商，不继续无控制地叠加提示。本轮不改生产系统或 CRMEB。

## 复核与复现材料

- 冻结计划与完整案例（内部历史资料；已从公开历史移除，私密备份保留）：
  二进制、全部 grounding-check Go 文件、两个首审提示及同一复核提示哈希，预定执行顺序。
- 逐次汇总（内部历史资料；已从公开历史移除，私密备份保留）。
- 分类审计（内部历史资料；已从公开历史移除，私密备份保留）。
- [归档运行脚本](eval_runs/2026-09-26/controlled-contract-length-v1/runner.cjs)：仅显式 --allow-model-api 才读取允许的模型环境变量并联网；已有归档目录拒绝再次执行。

调用前 grounding-check 全部测试通过，源码在整个采样期间保持冻结，结束后哈希核对通过。
起始 Git HEAD：15a528b1633cb8a9c80256235523e36322c6c3cc，工作区原本干净。
本轮只新增诊断归档与文档，不修改解析器、不删除非法字段、不扩大证据来源。
