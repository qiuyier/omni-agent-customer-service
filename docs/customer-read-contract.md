# 客户只读查询：宿主回调 v1

这是接入方服务端代码与可信适配器之间的接口，不是浏览器 JSON API。无需新增网关路由、数据库迁移、模型调用或业务表字段。没有注册回调，普通聊天保持原样。

## 输入与输出

每次调用只提供 customerID（从已核验的业务登录取得，不取自对话）、content（不可信客户正文）和 previousExchange()（按需读取，Node 需 await）。后者只返回当前绑定会话机器人接待状态下、最近一对具有回复关联的客户问题与机器人回答，即 {question,answer} 或 null。它不是跨会话记忆，也不是授权凭据。查询必须按 customerID 和资源标识共同过滤。

| 本次决策 | 唯一允许的返回字段 | 适用情况 |
|---|---|---|
| pass | kind | 不匹配；交给原聊天流程，不查询个人记录 |
| clarify | kind、text | 缺输入，先问清楚，不读业务表 |
| answer | kind、text | 查到了，或没有可见记录；不存在与无权查看使用同一答复 |
| unavailable | kind、text | 数据源失败或无法确认；不暴露异常、不编造结果 |

后三种的 text 必须是非空 UTF-8 文本，最多 1024 字节；不返回行、客户资料、凭据或附加元数据。只有文本使用原有宿主签名协议进入网关并保存为聊天答复；kind 不作为新网关协议字段。能力只能做只读操作：重复事件可能重进回调，但同一事件在网关中使用第一次保存的答复。不要把查询计数当成业务写入的幂等保证。

## 给第三个业务的接入步骤

1. 在业务服务端写回调，先识别问题；需要消歧时才调用 previousExchange()。识别策略可以不同，不要求接模型。
2. 用宿主现有只读 API 或参数化查询取本人记录，只将允许的状态转成面向客户的文本。不要让模型或聊天输入指定身份、SQL 或回调地址。
3. 在可信代码里绑定：Node 将 customerRead 传给 mountSupport；PHP 将 callable 传入 Bridge 构造器最后一个参数，参数顺序见 OmniChat.php 的绑定示例。不要从浏览器或用户可编辑的 JSON 加载函数。Node 旧版 null/string 回调保留兼容，新接入请用上述结构。
4. 运行下面的公共行为测试，把本业务的提问、追问、两个虚构客户和数据读取器作为样例加入。再以该宿主真实登录与真实存储单独联调。

阅读实现：[PHP 订单](../integrations/crmeb/OrderShipping.php)、[Node 维修](../integrations/device-repair/customer-read.mjs)。业务字段和状态留在这两处；上下文校验和签名转发在各语言的适配器，幂等保存与人工接管规则在通用网关。

## 可重复验收

在项目根目录运行（Node 24+、PHP CLI 7.4+、Go）：

```sh
node --test integrations/customer-read-contract/contract.test.mjs integrations/device-repair/customer-read.test.mjs
php integrations/crmeb/shipping_dialogue_test.php
php integrations/crmeb/bridge_test.php
```

公共测试对两个实际回调运行同一套断言：普通问题不读表、追问不读表、换客户不能承接追问、本人取数、状态变化、旧事件回放、他人单号不可见、数据源失败。共用测试使用本机参考宿主登录、真实 Go 网关和虚构读取器，PHP CLI 驱动只用来调用实际 PHP 回调，不冒充 CRMEB 原生 HTTP 部署。维修另测内存 SQLite；订单的原生 PHP 宿主验收与公共测试分开。
