# 可配置知识库接入与跨业务验收

后续已进行[当前依据与历史依据边界修正](Knowledge-history-boundary.md)；本文保留修正前的原始失败记录。

2026-09-30。本轮只新增部署接线、虚构资料包与验收脚本，复用通用 FAQ 关键词检索和热重载。
不修改模型提示，不写电商专属核心逻辑，不接订单、退款或维修执行工具。

## 部署和资料边界

主入口显式使用 `ai-knowledge` 模式：在 AI override 后加载 `compose.knowledge.yaml`，
将 `knowledge.local/crmeb-webchat` 整个目录只读挂载到 `/knowledge`。
主机更新通过同目录临时文件 rename 原子替换 faq.json；不单独挂载文件，避免替换后容器仍读旧 inode。
使用 keyword 检索，预算 2 秒，不调用 Embedding。客户消息和匹配的虚构资料会交给已配置 GLM。

资料包位于 `knowledge-fixtures/`，明确标注虚构，不代表 CRMEB 或真实商城政策：

- ecommerce-v1：自提开放时间周二至周六 10:20—16:40，以及两个明确的用户操作步骤。
- ecommerce-v2：改为周三至周日 11:10—17:50，其他范围不变。
- withdraw：显式 `[]`，启用知识但没有参考内容，不等同关闭知识约束。
- repair-v1：独立虚构维修服务的收件时间与送修准备；不包含报价、上门或预约页面。

维修使用单独的临时 webchat-demo 进程、内存库和模拟身份，不把维修资料加载到 CRMEB 主实例。
这验证资料和配置可替换，不代表已实现同实例多租户知识隔离。

## 运维操作

从客服仓库根目录运行：

```sh
# 仅首次准备；已有文件会拒绝覆盖：
node deploy/crmeb/knowledge.mjs ecommerce-v1 --prepare
# 开启 AI + 已准备好的知识目录：
node deploy/crmeb/set-mode.mjs ai-knowledge
# 更新、显式撤回、恢复；会核验网关报告的实际版本哈希：
node deploy/crmeb/knowledge.mjs ecommerce-v2
node deploy/crmeb/knowledge.mjs withdraw
node deploy/crmeb/knowledge.mjs ecommerce-v1
# 显式不加载知识，保留 AI；或改回固定回复：
node deploy/crmeb/set-mode.mjs ai
node deploy/crmeb/set-mode.mjs fake
```

脚本固定允许本测试实例及指定虚构资料包，不是面向浏览器的管理 API。
重载命令收到信号不算成功，必须等 `knowledge_reload_succeeded` 且版本与实际文件 SHA-256 一致。
失败则不声称新资料生效；缺文件、坏 JSON 的现有核心行为是保留旧版本，并非清空。
运行中请求可能沿用已取得的旧版参考；已保存历史和事件重放不改写。
撤回资料不删除旧历史，也不能硬保证模型绝不重复历史事实，详见 `docs/knowledge-reload.md`。

## 验收与复跑

### 本轮结果

技术验收：7 条真实回复、30 项检查通过。v1 → v2 → 空数组 → v1 的实际加载版本均已核对。
更新和撤回后原历史字节内容不变；维修临时进程已停止，主入口最终恢复虚构 ecommerce-v1。
FAQ/reload/bootstrap/webchat-server 测试以 `-count=1` 重跑通过。

**回答质量未全过：4 条符合预期，3 条存在错误追溯否认。**

- 有据自提、无据退款，以及维修有据/未知问题均符合本轮预期。
- 无命中的多轮退款追问中，模型拒绝猜测按钮，但额外错误声称之前有据的自提回答也缺乏依据。
- v2 回答给出正确新时段，却错误称 v1 的旧时段“没有资料依据”，而不是“旧版已被替代”。
- 撤回后正确表示当前无法确认，但又将之前确实存在的 v2 来源和时段说成“属于无依据内容”。

这是“当前参考集”与“历史证据有效性”混淆，不是数据库历史被改写或重载失败。
当前空参考集不能证明过去从未存在依据。上述回答仍保留在测试历史中，未删改成好看的结果。
本轮不扩大为模型提示调优，不将技术 passed 当成回答质量通过；下一项优先应针对这个失败案例，
明确无命中/替代/撤回的表达边界，再做小范围多轮回归，而不是继续扩展业务工具。

这里的通过/问题分类是人工审阅这 7 条虚构回答，不是独立自动事实验证器或生产质量保证。

`knowledge_check.mjs` 通过真实 CRMEB 登录、桥接和 PostgreSQL 发送五条电商问题，
检查加载版本、更新/撤回时历史不变，再启动隔离维修实例发送两条问题。
账号凭据只从 stdin 读取，结束退出自身创建的登录；主实例恢复 ecommerce-v1，维修进程停止。
报告 `knowledge-acceptance-report.json` 的技术 passed 与回答审阅分开，不能仅凭 HTTP 200 判断知识回答正确。
复跑会产生真实模型费用，并短暂更新主测试入口的资料；不要在有其他并发验收时运行。

```sh
go build -o /tmp/omni-knowledge-live-demo ./cmd/webchat-demo
# 使用受保护 stdin 传入一个测试账号对象，禁止把密码写进命令参数或文档：
node integrations/crmeb/knowledge_check.mjs
go test -count=1 ./internal/knowledge/faq ./internal/knowledge/reload ./internal/bootstrap ./cmd/webchat-server
```

阅读顺序：本说明 → `deploy/crmeb/compose.knowledge.yaml` → `knowledge-fixtures/`
→ `deploy/crmeb/knowledge.mjs` → `knowledge_check.mjs` 与验收报告 → 通用 `docs/knowledge-reload.md`。
