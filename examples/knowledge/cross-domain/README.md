# 跨行业迁移验收资料

[图书馆资料](library.json)与[场馆资料](venue.json)均为虚构测试知识，不是实际运营政策。

使用同一套通用客服规则，分别检验有依据的入口、按必要条件区分答案、未知政策和无工具代办边界。模型可询问必要条件或列出已有分支，不要求机械地总是追问。

- [固定6场景、8轮题目](../../regression/knowledge/cross-domain/live-cases.json)
- [本轮结果及限制](../../archive/knowledge/cross-domain/eval_runs/2026-10-08T09-36-51-627Z/review.md)

在仓库根目录显式执行，可能产生模型费用：

```sh
node integrations/device-repair/live-check.mjs --allow-model-api --cross-domain
```

复用现有本地验收启动器，不连接 CRMEB。每个场景使用独立内存实例，调用前冻结资料与题目，最多8次请求，不重试；每次运行另建历史目录，结束后停止临时进程。只从根目录 .env 读取模型配置，凭证不写入报告。读取说明见[验收工具](../../../integrations/device-repair/README.md)。
