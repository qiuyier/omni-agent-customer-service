# 接入预检验收记录

日期：2026-10-09。范围：私有网页客服网关配置与资源预检，不启动网页、不请求真实模型、不修改CRMEB或现有模型配置。

## 实际独立数据库检查

使用 deploy/standalone-host/compose.yaml 的专用测试PostgreSQL及既有schema。不运行迁移，不写入消息；测试结束后将本轮启动的容器恢复为停止状态，数据卷保留。模型和embedding配置均指向本机计数桩，仅使用虚构凭据。

机器输出记录（内部历史资料；已从公开历史移除，私密备份保留）：7项全部符合预期，provider计数0。

| 项目 | 预期与实际 |
| --- | --- |
| 离线配置检查，数据库端口不可用 | 退出0，明确未检查连接 |
| fake资源检查 | 真实PostgreSQL连接/schema通过，知识未检查 |
| AI keyword资源检查 | 3条FAQ格式及数据库检查通过，无模型调用 |
| AI semantic资源检查 | 本地FAQ/schema通过，明确未构建索引，无embedding调用 |
| AI无效FAQ路径 | 退出1，知识文件错误，无fake降级 |
| 数据库不可用 | 退出1，安全PostgreSQL诊断，无内存降级 |
| 缺少宿主身份 | 退出1，指出WEBCHAT_HOST_ID |

## 自动回归

以下命令通过，模型类用例仅使用本机mock，不使用真实密钥。初次沙箱内执行因禁止监听本机端口失败，批准在沙箱外重跑后通过。

```sh
go test -count=1 ./cmd/webchat-server ./internal/bootstrap ./internal/knowledge/faq
go test -race -count=1 ./cmd/webchat-server
go vet ./cmd/webchat-server
git diff --check
```

新增用例覆盖：fake忽略缺失知识、AI有效/空/缺失/错误格式/未知字段、非法模式、数据库打开与schema失败、超时与取消、连接关闭、输出失败脱敏、互斥参数及semantic模式零provider调用。原有离线配置检查和AI启动不降级测试继续通过。

资源预检未验证真实模型凭据/可用性、semantic索引质量、端口占用、业务登录/代理或生产安全。它不是收费探测或生产上线认证。接入步骤及配置模板见[接入清单](host-onboarding.md)。
