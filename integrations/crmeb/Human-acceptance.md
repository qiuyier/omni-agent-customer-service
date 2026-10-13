# CRMEB 人工接入验收（2026-10-13）

本轮完成客户申请人工、CRMEB 客服身份接入通用 Desk、接管/回复/已读/关闭，以及关闭后开启独立新咨询。
这是一种宿主适配示例，不是把客服核心改成电商专用；未新增商城业务表，未修改原商城客服登录、聊天入口。
客服工作台复用通用 React 组件，通过请求函数注入连接 CRMEB，不把商城鉴权放进通用核心。
前端设计技能用于沿用现有样式、加载/错误反馈和明确的本机草稿清理操作，没有引入设计依赖。

## 入口及配置

- 客户：/omni-chat-test/；先使用原商城用户登录。
- 新客服工作台：/omni-chat-test/desk.html；先在 /kefu 使用原客服账号登录。
- 原 /kefu/pc_list 保留，不与新的独立客服消息库混用。
- human_enabled 必须显式为 true；agents 按 CRMEB 客服内部 ID 配置 id 与独立 secret。
- 网关 WEBCHAT_AGENTS_JSON 的 source_id/id/secret 与宿主映射一致，不能复用客户宿主密钥。
- 所有密钥仅保留在私有实例配置和部署 .env，浏览器只读取原客服登录 Cookie。
- 本机当前 fake 模式，未调用外部模型；原知识/模型配置未删除，本轮没有验收 AI 质量。

安装映射新增 Transport.php、DeskBridge.php 到 extend/omni_chat/；更新原四个适配文件及 public/omni-chat-test 静态资源。
compose.yaml 读取 OMNI_AGENTS_JSON；原 PostgreSQL 命名卷保留，迁移版本为 2，无新增公开端口。
本次部署前文件备份位于 CRMEB runtime/omni-chat-deploy/human-backup-l2Qjt4；属于私有备份，勿放入 public 或提交版本库。

## 验收及修复

- PHP74：原桥接 30 项、新客服桥接 19 项通过。
- 前端：新增客服 Cookie、请求绑定、登录切换/退出、晚到响应、人工控制接口测试。
- Go：internal/webchat/...、cmd/webchat-server 测试通过；前端两套资源构建通过。
- human_check.mjs：29 项真实 HTTP 检查通过。凭据通过 stdin 提供，脚本注销自己创建的登录，不打印令牌。
- 浏览器：原客服登录成功，新工作台显示 CRMEB 坐席；客户转人工进入队列，客服接管并回复，客户收到相同文本；关闭后客户显示结束，点击新咨询得到不同编号且记录为空。
- 原历史仍保留；两个客户历史隔离，重复人工回复相同 event_id 不新增副本，关闭后不能发送。

首次 HTTP 回归发现：CRMEB 原客服 parseToken 会将误传的客户令牌用于客服表查询，失败后删除该令牌缓存。
修复：控制器先核对服务端缓存中的登录类型，再调用原鉴权做签名/有效期/状态验证；不以浏览器声称的角色授权。
回归同时验证客户令牌不能进入客服接口、客服令牌不能作为客户，且两种原登录均可继续正常使用。
客服身份还会复查数据库当前启用状态；请求结束再次核对身份。已提交的上游写操作不会因随后退出而回滚。

## 阅读顺序

1. 本文：交付范围与实际验收边界。
2. OmniChat.php、DeskBridge.php：宿主登录类型、启用状态、显式坐席映射、固定接口白名单。
3. Bridge.php、Transport.php：客户凭证绑定、转人工、新咨询失效与上游通信。
4. web/chat/src/crmeb-desk.mjs、crmeb-desk.jsx：原 Cookie 接入与通用 Desk 请求注入。
5. web/chat/src/crmeb.mjs、crmeb.jsx：客户人工状态与新咨询；desk.jsx、read-receipts.jsx：复用组件。
6. desk_bridge_test.php、web/chat/src/crmeb-desk.test.mjs、human_check.mjs：自动化边界和真实验收。

## 未覆盖的边界

本轮只有一个真实客服账号，未做两个真实坐席争抢接单；通用层已有并发测试不等于此次多账号现场验收。
不包含订单/退款操作、生产上线、集群锁、跨设备强制退出、AI 回答质量或高并发容量。
原客服的用户列表不显示新系统会话是预期：两套消息存储独立，新会话在新 Desk 处理。
