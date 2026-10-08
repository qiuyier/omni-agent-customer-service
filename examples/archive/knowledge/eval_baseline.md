# 本地 FAQ 检索初始基线

第七阶段第二步的人工相关性评测，不是模型回答评测或生产准确率承诺。
三个知识条目与 20 个问题均为虚构演示数据。保留原有关键词子串检索和同分文件顺序。

## 复现条件

从项目根目录执行：

```bash
go run ./cmd/knowledge-eval -faq examples/knowledge/faq.json -cases examples/regression/knowledge/eval_cases.json -top-k 3 -format json
```

输入文件 SHA-256：

- `faq.json`：`649383d9d9e8f54e1ac62720ecc62126e70a668592929928921c64a17b7c864f`
- `eval_cases.json`：`d4ca9c2d4141a1cc6a13d82c384bd86704a74f9d1bf7f9f573ab196d94481238`

算法、FAQ、标注及 K 都会影响成绩；输入有变化时，应明确说明并另行记录基线，
不要覆盖这里的初始成绩。相关集合没有标注相关资料之间的优先级。

## 成绩

| 指标 | 初始结果 |
| --- | --- |
| 问题总数 | 20：15 个有相关参考，5 个无相关参考 |
| 首条命中率（有相关参考） | 10/15，66.7% |
| 正确无命中率（无相关参考） | 2/5，40.0% |
| 集合完全匹配率（全部） | 12/20，60.0% |
| Top-K 内漏检问题数 | 4 |
| 含无关命中的问题数 | 4 |

## 逐条差异

以下列出全部 8 条集合未匹配的样例，其余 12 条集合匹配。ID 对应 `eval_cases.json`。

| 样例 ID | 实际现象 | 人工相关标签 |
| --- | --- | --- |
| `paraphrase_support_hours` | 空结果 | `demo-support-hours` |
| `paraphrase_password` | 空结果 | `demo-password-reset` |
| `paraphrase_invoice` | 空结果 | `demo-invoice` |
| `password_email_troubleshooting` | 空结果 | `demo-password-reset` |
| `ambiguous_service_time` | 返回 `demo-support-hours` | 空集合：排队模型术语与客服排班无关 |
| `literal_invoice_identifier` | 返回 `demo-invoice` | 空集合：代码变量名与开票无关 |
| `negated_password_positive_invoice` | 先返回 `demo-password-reset`，再返回 `demo-invoice` | 仅 `demo-invoice`：密码主题被明确排除 |
| `negated_topics_unknown_returns` | 返回 `demo-support-hours`、`demo-invoice` | 空集合：有效需求是资料中没有的退货操作 |

这揭示两类改进方向：口语改写缺少关键词导致漏检；歧义和否定表达中的关键词
被机械匹配导致误检或错误排序。本阶段仅记录问题，不调整算法或关键词。
`relevant_incomplete_holiday` 与 `relevant_incomplete_invoice_status` 检索正确也不能证明
具体节假日排班或订单已经开票：相关资料不等于完整答案，更不等于业务执行结果。
