# 通用人工接管最小闭环

后续已补充[自动更新](automatic-updates.md)和[客服未读提醒](desk-attention.md)。未读使用消息级确认，后台历史读取不清除提醒；数据库需升级至版本2。下方日期验收保留当时范围。

本轮新增的是通用网页渠道能力，不依赖CRMEB。用户显式申请后进入人工队列，坐席查看历史、接管、回复，最后关闭会话。未配置坐席时默认关闭，不按用户自然语言自动触发，不把模型说“已转人工”视为执行结果。

## 本地体验

```sh
node integrations/standalone-host/browser-lab.mjs --allow-local-postgres --allow-human-desk
```

打开终端输出的地址，选择客户并进入聊天，点击“申请人工处理”。同一地址下 /desk/ 是工作台，选择乔杉或温宁，打开队列条目、接管后发送人工回复。客户点击“刷新记录”读取人工消息；坐席“结束并关闭会话”后，客户刷新可看到关闭状态，不能再发送。

此启动器生成独立随机宿主/坐席凭证，仅保留在服务端，使用专用PostgreSQL和固定回复，不读取 .env、不请求模型。终端stop停止本轮服务及数据库容器，保留数据卷和证据。两个坐席和客户均为无密码模拟选择，绝不是生产认证。工作台按前端技能沿用原项目的中性色、单一绿色强调、响应式双栏与明确空/忙/失败状态，不引入新UI依赖。

## 网关与权限

WEBCHAT_AGENTS_JSON 是可选的服务端JSON数组，每项只有 source_id、id、secret。source_id必须匹配WEBCHAT_HOST_ID，坐席ID在同一来源内唯一，每个secret须32—128字节，与宿主及其他坐席凭证独立。最多32个坐席。未设置或空数组关闭人工接口；-check-config及-check-resources会校验配置，但不验证业务侧真实登录。

实际凭据由密钥管理器注入，不能把填好的JSON放进版本库或浏览器。独立Node示例通过startHost的agents参数注入{id,name,secret}，仅由后端选择已登录坐席的凭证；用户和坐席Cookie分别维护，不能互相充当身份。生产必须替换模拟选择为真实员工认证、权限分配、HTTPS/Secure Cookie与可撤销会话，精确代理白名单；不得公开私有/internal/chat路径。

| 路径 | 身份 | 行为 |
| --- | --- | --- |
| POST /chat/v1/handoff | 当前客户短期凭证 | 只接收{}，只能申请自己的会话，不接受conversation_id或agent_id |
| GET /internal/chat/desk/queue | 独立坐席凭证，仅服务端 | 最多100条，按更新时间/id排序；只返回同来源等待人工及本人接管的会话 |
| POST /internal/chat/desk/action | 独立坐席凭证，仅服务端 | conversation_id/action；action为history、claim、reply、close、read；角色由凭证决定，不接收agent_id |

claim/close传revision，事务内校验状态和版本，已完成的同坐席同状态重试可安全返回；reply传event_id/content，最多4000 UTF-8字节，重复相同事件返回成功，改内容返回409。history最多返回最近100条且总正文预算128 KiB，只含客户、机器人和人工消息；当前数据层仍读取完整历史后裁剪，不是数据库分页。

等待队列对该来源的配置坐席开放，可先读历史；一旦接管，其他坐席不能读取、回复、接管或关闭该会话。不能跨来源访问，即使知道会话ID也不允许。Node公开端只提供/desk/me、login、logout、queue、action，原始坐席凭证不返回浏览器；退出后迟到响应也会重新检查登录绑定。

## 状态、并发与重试

bot_active → waiting_human → human_active → closed。状态变更、权限校验、人工回复写入共享数据库会话锁；两个坐席同时抢单只有一个成功。人工申请可与正在生成的AI请求并行；状态变更后，既有核心的revision检查会拒绝迟到机器人回复落库。已在状态变更前保存的机器人回复不会撤回，外部模型请求也未必被取消或免计费。

等待人工及人工处理中，客户消息仍保存，但不触发模型。人工回复按来源/会话/坐席/事件组合去重；前端发送前将待确认内容存入该坐席/会话的sessionStorage，失败后重新打开原会话手动重试，不能改成新事件自动重发。退出/切换坐席不把原待确认内容带到其他坐席。

关闭仍是永久关闭，不重开旧会话。独立 Node 示例现支持客户点击“发起新咨询”：创建独立 bot_active 会话，清空坐席归属和本轮历史；旧会话及其消息保留。详见 [新咨询轮次](consultation-rounds.md)。

本轮不含真实员工认证、人工通知/实时推送、排班/转派/工单、队列分页、操作审计台或服务时限承诺。用户和坐席目前主动刷新，不声称实时送达。模型知识/提示若仍声称“无人工能力”，接入方须随实际功能更新；本轮固定回复验收未改动历史AI验收资料。

## 2026-10-09 验收

[网络与进程记录](../examples/archive/integration/standalone-host/2026-10-09T09-52-44-271Z-797b94e8/report.json)。实际浏览器：客户申请并留言→乔杉看到队列与历史→接管回复→客户刷新看到人工内容→客户继续留言且无机器人回复→强制重启网关→队列和人工历史保留→温宁登录看不到乔杉会话→乔杉重登关闭→客户刷新后禁发。

SQL只读核对本轮会话：closed、revision=4；2条customer、1条human_agent，各自不同ID数等于行数；0条bot。外部模型调用0。验收结束后本轮进程、临时标签页与专用数据库容器已关闭，数据卷和证据保留。

自动测试：网关/会话/PostgreSQL包及启动入口的race回归通过；Node宿主4项测试通过；网页协议25项通过；构建、go vet和git diff --check通过。新增覆盖客户不能冒充坐席、请求字段伪造、跨来源、抢单、已接管的跨坐席读写/关闭拒绝、原事件重放/冲突、关闭禁发、正在生成的AI迟到不落库、坐席登录与客户Cookie隔离、跨源拒绝及退出后迟到响应不泄漏。

竞态使用可控本地模型桩，非真实供应商调用；浏览器用PostgreSQL和固定回复。不能据此承诺所有多副本/网络故障场景已覆盖或生产认证已完成。

![人工工作台完成接管回复并关闭](../examples/archive/integration/standalone-host/2026-10-09T09-52-44-271Z-797b94e8/desk-finished.jpg)

![客户收到人工回复且关闭后禁发](../examples/archive/integration/standalone-host/2026-10-09T09-52-44-271Z-797b94e8/customer-finished.jpg)

阅读顺序：internal/webchat/human.go（权限及事务）→human_test.go（关键竞态）→internal/conversation/queue.go与internal/persistence/postgres/queue.go（队列）→integrations/standalone-host/host.mjs（身份与代理）→web/chat/src/desk.jsx及app.jsx（工作台与客户入口）。
