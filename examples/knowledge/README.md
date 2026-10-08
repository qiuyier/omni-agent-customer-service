# 接入知识样例

[设备维修接入样例](../../integrations/device-repair/README.md)提供非 CRMEB 的本地启动与身份、知识、历史隔离验收，使用本地模型替身。

先看 [faq.json](faq.json) 的最小格式，再看 [webchat/faq.json](webchat/faq.json) 的跨业务知识样例。接入步骤见[通用接入契约](../../docs/integration-contract.md)和[网页客服 AI 文档](../../docs/webchat-ai.md)。

- [电商模拟资料](ecommerce/README.md)：虚构店铺政策，用来验证通用能力，不是生产运营资料。
- [CRMEB 测试知识快照](crmeb-fixture-v1/README.md)：独立测试库的接入示范，保留快照、清单与种子脚本；不要直接对其他数据库执行。
- [回归输入](../regression/README.md)：测试题与预期标签不属于运行知识库。
- [历史验收](../archive/README.md)：查看当时的范围、限制及失败项。

现有 FAQ 文件位置保持不变。接入其他系统时应提供该系统授权的知识和适配，不把电商规则写入通用客服核心。
