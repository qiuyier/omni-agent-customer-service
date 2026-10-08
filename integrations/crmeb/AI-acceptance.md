# CRMEB 独立入口：真实 AI 接入验收

日期：2026-09-30。本轮复用通用客服核心与既有 GLM provider，不接订单、退款或人工处理工具。
主 `.env` 为 `glm-5.3-flash`，旧 `.env.local` 的 DeepSeek 配置未合并；不加载知识库或 Embedding。

## 已实现

- 基础部署仍默认 fake；`deploy/crmeb/compose.ai.yaml` 显式启用 AI。
- `prepare-ai.mjs` 只从主 `.env` 提取模型白名单字段到私密 `.ai.env`，拒绝覆盖，不拷贝商城凭据或数据库配置。
- 镜像增加公开 CA 根证书；模型端点要求 HTTPS，不关闭证书校验。私密文件不进镜像或版本库。
- `set-mode.mjs` 先检查配置，再暂时禁用桥接、启动网关、等待 readiness，最后同步桥接模式；失败不偷偷降级 fake。
- 浏览器明示 AI 数据用途与无业务操作能力；沿用现有加载、错误和待确认状态。
  历史机器人消息统一标“客服回复”，不根据当前模式把旧固定回复标成 AI。
- POST AI 等待 150 秒，fake/未知模式及读历史 45 秒；401 续签后重新选择预算，重试沿用原 event_id 和正文。
- 现有模型生成预算 75 秒、网关 110 秒、PHP cURL 120 秒；本机 Nginx FastCGI 300 秒，无需修改商城代理配置。

## 真实模型结果

`ai-acceptance-report.json`：36 项检查通过，4 条成功生成的真实回复。
两个测试账号分别提供虚构代号与不同说明偏好，第二轮均正确复述自身上下文，未出现另一账号代号。
各次回复约 4.2–7.0 秒；会话不同，混用凭证拒绝，重新换取会话后历史一致。
相同事件重放返回原客户消息/回复 ID 且历史不变；同事件改正文返回 409。
这证明本次小样本链路正常，不是普遍回答质量保证，也未按供应商账单核算内部重试次数。
脚本凭据由 stdin 传入，结果不含密码或 Token，自行创建的商城登录已退出。

浏览器额外完成 1 条真实回复：询问能否代执行退款，得到“不能，我无法替你执行退款，也没有退款操作或查询工具，仅能根据已有资料作解释说明。”
界面先显示生成等待状态，完成后清除待确认状态；刷新仍为原会话，问题与回复各一条，见 `h5-evidence/ai.png`。
本轮共观察到 5 条成功真实回复（脚本 4 条 + 浏览器 1 条），不把旧固定回复计入。

## 故障验证范围

使用本地模拟模型而非人为破坏真实供应商：

- `TestAIGatewayProviderErrorIsSafeAndSameEventCanRetry`：上游报错映射为脱敏 503，保留客户消息，原事件重试只补一条回复，成功后重放不再生成。
- `TestAIGatewayCanceledGenerationCanRetryWithoutDuplicatingCustomer`：取消传播、重试不重复客户消息。
- `TestAIGatewayRequestDeadlinePreservesMessageForRetry`：请求超时后保留可重试消息。
- 前端新增 AI 503 不自动重发、手动重试正文和 event_id 不变，以及明确模式提示的测试；前端总计 24 项通过。
- Go bootstrap/webchat/webchat-server/customerservice 测试通过；webchat 与 webchat-server race 测试通过。

本轮没有对真实供应商制造超时，也没有覆盖完整 Nginx/PHP/真实供应商链路的故障注入。
上述三个本地故障测试本次以 `-count=1` 重跑通过；首次沙箱禁止监听，获准本地回环监听后通过。
不承诺模型收费 exactly-once：供应商已处理但响应丢失、生成后未保存即崩溃等仍可能再次调用。

## 运行与切换

从客服仓库根目录执行。私密 `.ai.env` 已生成，不要再次运行 prepare-ai 或打印配置。

```sh
# 首次：node deploy/crmeb/prepare-ai.mjs
docker compose -f deploy/crmeb/compose.yaml -f deploy/crmeb/compose.ai.yaml build gateway
node deploy/crmeb/set-mode.mjs ai
# 显式回到固定回复，不删除历史：
node deploy/crmeb/set-mode.mjs fake
```

密钥更新需先安全更新私密模型环境再重建容器；主 `.env` 的后续改动不会自动同步到 `.ai.env`。
AI 文件采用 Compose raw env_file（本机版本已验证支持）；禁止用整份根环境文件替代，避免带入无关凭据。
不要单独运行基础 `up gateway` 改模式而漏改桥接元数据。php74 生命周期、命名卷和恢复边界见部署 README。
启用后该测试入口的后续新消息会调用模型并可能产生费用；停用请显式切回 fake。

阅读顺序：本报告 → `deploy/crmeb/compose.ai.yaml` → `prepare-ai.mjs` / `set-mode.mjs`
→ `web/chat/src/crmeb.mjs` / `crmeb.jsx` → `ai_check.mjs` / `ai-acceptance-report.json`。
