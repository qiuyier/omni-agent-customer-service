# 独立 H5 测试入口与验收

后续状态：已接入[持久化测试部署](../../deploy/crmeb/README.md)，独立入口保持运行。
本文下面的停机、清空和默认关闭描述记录的是前一轮临时内存验收收尾，不代表当前部署。

日期：2026-09-30（承接 2026-09-29 的实现）。本机 PHP74，CRMEB 原登录与人工客服入口保持不变。

## 已实现

新增独立静态页面 `/omni-chat-test/`，由 `web/chat/src/crmeb.jsx` 构建，采用项目已有 React/Tailwind。
不引入新依赖、远程资源或新账号密码表单。沿用现有视觉样式，提供加载、空记录、错误、失效登录、
待确认发送状态及键盘可操作的表单，不增加与验收无关的动效。

CRMEB 登录格式依据本机 `template/uni-app/store/modules/app.js`、`utils/cache.js` 及实际 H5 bundle：
读取同源 `localStorage.LOGIN_STATUS_TOKEN` 中既有的原始字符串。该读取只用于提交宿主认证，
绝不从 JWT 内容自行认定用户 ID。其他版本存储协议不同需调整独立适配层，不能盲目通用化。
无凭据时提示返回商城登录；客服短期凭证仅存在 JS 闭包中，不进入 DOM、React 状态或持久存储。

前端 `crmeb.mjs` 对接 `/api/omni-chat/session` 和 `/api/omni-chat/messages`：
CRMEB 凭证使用 `Authori-zation`，客服凭证使用 `X-Omni-Chat-Token`；禁止跟随跳转。
身份检查发生在发起请求前及收到成功/失败结果后；换号/退出、清理 API 后的迟到响应无法恢复旧身份，
迟到的 401 也不会在新身份下续期或重发。
浏览器 storage、focus、visibilitychange 触发检查，检测到切换则清空页面消息、草稿与内存凭证。
纯服务端撤销但本地存储未改变时，在下一次请求中拒绝，不宣称空闲页面能实时探知服务端撤销。

沿用通用前端的按会话保存待确认消息机制。刷新不自动发送；不确定失败由用户主动重试，
event_id 与正文不变。使用 `crypto.getRandomValues` 生成 UUID，不依赖 HTTP 测试域名下可能不可用的 randomUUID。
原始待确认消息可能短暂存在 sessionStorage，凭证不写入其中；存储不可用或记录损坏时暂停发送。

## 本轮修正

真实 Nginx 路径会内部生成 ThinkPHP 的 `s=/api/...` 查询参数。
原控制器检查服务端 QUERY_STRING，误将干净的浏览器 URL 判为非法。
已改查原始 REQUEST_URI 的查询部分，仍拒绝浏览器自行提供的 `?uid=...` 等参数。
CLI 模拟请求没有 REQUEST_URI 时才回退到 QUERY_STRING。

本机专用锁目录/空锁文件配置为 PHP-FPM www-data 组可访问。部署须保证这个目录归属正确，
不要把整个 runtime 或其他目录设为全员可写。权限检查当时已返回可读写，因此不把它断言为首次失败的根因。

## 验收证据

1. 用户二打开测试页，获得会话后缀 `f6e098372279`；发出“用户二隔离验收：乙侧独立消息。”并收到固定回复。
2. 刷新后保留同一个会话及一条客户消息、一条回复，没有重复发送。
3. 在另一个商城标签页通过原生“退出登录”退出用户二；测试页清空旧消息与输入框，提示重新登录。
4. 登录用户一，重新连接后得到不同会话后缀 `6dbc189cbf71`，初始为空，不显示用户二记录。
5. 用户一发送“用户一隔离验收：甲侧独立消息。”，只显示自己的消息与固定回复。
6. 窄屏观察时 DOM 实测宽度 609px，scrollWidth 同为 609px，无横向溢出。
   浏览器未准确应用请求的 390px/1280px 覆盖尺寸，因此不宣称完成这两个精确断点或手机真机验收。

页面截图：`h5-evidence/user2.png`、`logout.png`、`user1.png`。不含密码或 Token。

自动验证：

- 前端 `npm --prefix web/chat test`：21 项通过（含原有通用协议测试和新增 8 项适配测试）。
- 实际 Nginx/PHP74/私有 Go 网关 HTTP：13 项通过，见 `http-acceptance-report.json`。
  检查原始 URL、双方发送与隔离历史、凭证混用、伪造身份、异源请求、原生退出后读/写/换会话拒绝。
- 前一轮 PHP74 桥接 27 项单元检查和 19 项控制器/网关联调结果仍单独保留，不混称为本轮浏览器用例。
- 本轮未调用外部模型或 Embedding；回复为固定测试文本，不验证智能回答或知识质量。

上轮网关超时退出后，本轮续跑重新生成私有密钥并启动新的内存网关，重跑用户二与用户一流程。
旧内存记录已丢弃，不把跨网关重启误当作持久化恢复。

## 当前交付状态

页面文件保留在 CRMEB `public/omni-chat-test/`，原客服入口未替换。
测试结束已恢复 `app/api/config/omni_chat.php` 与默认模板完全一致；公开桥接返回 404。
已核对临时网关 PID 并停止，删除该轮随机密钥及私有配置；其内存测试消息随停机消失。
HTTP 验收自行创建的登录已退出；浏览器最后保留用户一的商城登录，不主动退出用户当前账号。
桥接缓存中的短期凭证由原 TTL 自动过期；不扫描或清空 CRMEB 共享缓存。

现在打开测试页会提示“测试服务尚未启用”，不是登录损坏。
这是可重复启用的测试入口与通过的验收，不是常驻服务或生产上线。
下一阶段应先确定同容器/同网络命名空间私有网关的常驻部署和持久化方式，再决定是否正式展示入口。

## 构建与阅读顺序

```sh
node web/chat/build-crmeb.mjs
npm --prefix web/chat test
```

构建产物 `integrations/crmeb/h5/` 安装到 CRMEB 新建的 `public/omni-chat-test/`；
不要用新产物覆盖用户修改的同名目录。独立协议不修改通用 `protocol.mjs` 或示例宿主页面。
`http_check.mjs` 是固定本机域名的测试脚本，凭据经 stdin 传入，只用于明确授权的独立测试环境。

阅读：本报告 → `web/chat/src/crmeb.mjs` → `web/chat/src/crmeb.jsx` → `web/chat/src/crmeb.test.mjs`
→ `integrations/crmeb/OmniChat.php` 中原始 URI 校验 → HTTP 报告。
