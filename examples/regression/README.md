# 回归资料

这里只存测试输入，不存运行知识或模型输出。

- knowledge/：最小知识检索开发题、保留题和回复题。
- knowledge/ecommerce/：电商模拟开发题、保留题和回复题；FAQ 仍在 [knowledge/ecommerce](../knowledge/ecommerce/README.md)。
- knowledge/webchat/：跨业务事实约束、依据审查、回放提示和语句分类语料。

## 离线检查

从仓库根目录运行，不需要真实模型或 Embedding API：

~~~bash
go test ./cmd/grounding-check ./cmd/knowledge-eval ./cmd/reply-check ./internal/knowledge/... ./internal/ai/eino ./internal/bootstrap
go run ./cmd/knowledge-eval -faq examples/knowledge/faq.json -cases examples/regression/knowledge/eval_cases.json -retriever keyword -format json
~~~

离线通过仅说明代码和固定样本检查通过，不代表真实模型质量。联网实验必须显式授权并设置调用预算。

部分回归测试直接读取[历史归档](../archive/README.md)中的真实失败输出（如 inventory-v1-repeated），这些 JSON 是回归依赖，不能当作临时文件删除。
