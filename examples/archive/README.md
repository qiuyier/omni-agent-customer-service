# 历史实验归档

保留原始成功与失败结果，不将不同模型、提示版本或样本范围的结果合并为当前通过率。

## 查阅入口

- [最小检索基线](knowledge/eval_baseline.md)、[语义检索记录](knowledge/eval_semantic.md)、[回复验收](knowledge/reply_acceptance.md)。
- [电商验收](knowledge/ecommerce/acceptance.md)、[开发集诊断](knowledge/ecommerce/diagnostics.md)。
- [跨业务主流程验收](knowledge/webchat/mainflow-acceptance-v3.md)、[迁移验收](knowledge/webchat/mainflow-transfer-v1.md)。
- [事实审查冻结验收](knowledge/webchat/grounding-frozen-validation.md)、[清单复核失败案例](knowledge/webchat/grounding-inventory-review-v1.md)。
- 各级 eval_runs/ 按原日期和实验名保留原始输出、冻结记录和汇总。更多专题可从[审查工具文档](../../docs/grounding-check.md)查阅。

## 保存与重跑约定

原始 JSON 和 eval_runs 内脚本内容保持原样，包括当时的路径、源码哈希、模型信息与失败记录。报告文档的导航和示例路径已更新；原始记录中的旧路径属于历史上下文，不是当前入口。

归档脚本可能硬编码旧目录、旧配置或外部数据库，并可能调用付费服务，不作为可直接执行的当前接入脚本。重跑前应审阅脚本、使用当前回归路径，并输出到新的独立目录；不要覆盖历史目录，也不要执行数据库种子脚本来复现纯模型实验。

归档不是随时可删的缓存：cmd/grounding-check/archived_contract_test.go 直接读取 inventory-v1-repeated 的真实输出。删除前必须检查代码、文档和复现依赖。
