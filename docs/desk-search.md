# 坐席会话搜索

通用工作台新增“查找会话”，用于从当前队列之外找回自己处理过的咨询。不是全站客户搜索，也不赋予管理员式跨坐席读取权限。

## 使用与权限

- 可组合消息关键词、部分会话编号、状态；空条件可分页浏览。
- 关键词和编号按原文包含匹配，区分大小写；`%`、`_` 是普通字符，不是通配符。关键词最多 256 UTF-8 字节，编号最多 128 字节。
- 仅搜索同一业务来源中等待人工的会话，或分配给当前坐席的会话（包括已结束）。其他坐席已接管的会话不可见。
- 只匹配客户、助手和人工坐席消息，不匹配 system/tool 内部消息；结果只有编号、状态和 revision，不返回消息摘要或客户资料。
- 每页最多 20 条，按会话编号升序，不是按最近消息时间排序。下一页使用最后一条编号作为游标；并发状态变化时结果不是固定快照，可重新从首页查找。
- 点击“查看记录”沿用原历史入口并重新鉴权。搜索结果不是授权凭证；已结束的历史只能查看，不能继续回复或重新接管。
- 条件不写入 URL 或浏览器持久存储；修改条件、刷新/操作会话和窗口重新获得焦点会清除旧结果。搜索本身不标记已读，打开历史沿用原已读处理。

## 接入契约

Node host-kit 与仓库内的 CRMEB PHP 参考适配器共用同一网关搜索。已部署的旧 PHP 接入不会自动升级：新路由、控制器、DeskBridge 和配套工作台静态资源需一起更新。此变更未部署到实际 CRMEB 站点。

其他宿主可按三步接入：

1. 在宿主同源的坐席 API 暴露固定 POST 路由，通过原登录验证员工仍有客服权限，在服务端映射其网关坐席凭据。
2. 核对请求中的 expected_agent_id，只转发允许的搜索条件至私有搜索接口；不要接受客户端指定业务来源，不需要读取宿主的订单或聊天表。
3. 返回结果前再核验登录和坐席映射；工作台丢弃切号后的迟到结果，点击历史时重新走原有鉴权入口。

浏览器 `POST /desk/search`：JSON 包含 `keyword`、`conversation_id`、`status`、`after`，以及必须匹配当前业务坐席的 `expected_agent_id`。状态为空或 `waiting_human`、`human_active`、`closed`。私有 adapter 校验真实业务登录后映射坐席凭据，去掉 expected 字段，再调用 `POST /internal/chat/desk/search`。客户端不能指定来源或替换坐席凭据。

返回 `{items: [{conversation_id, status, revision}], next?}`。客户端在请求前后核对当前坐席；adapter/proxy 继续执行撤销、身份版本、Origin 和固定路由校验。网关沿用坐席限流、并发与超时预算。未实现可选 `DeskSearcher` 的仓库返回 404，不退化为全量下载历史。

Memory/PostgreSQL 均实现相同契约；没有新增 schema、迁移或依赖。PostgreSQL 使用参数化 `strpos`/`EXISTS`，`LIMIT` 限制返回行数，但不保证大规模数据扫描成本。本轮不宣称全文检索性能，不新增正文搜索索引。

## 2026-11-17 验收

- 全量 `go test ./...`、`go vet ./...` 通过（不配置外部数据库）；网页逻辑 172 项通过。
- 新搜索 Memory 与 PostgreSQL 同契约测试：20+6 分页、状态/编号/关键词组合、字面通配符、大小写、内部消息排除、跨来源/跨坐席隔离、取消请求和无效输入。
- PostgreSQL 专项使用新建隔离 tmpfs 容器，`go test -race -count=1 -v ./internal/persistence/postgres -run '^TestDeskSearchContract$'` 实际通过、无跳过；已删除本轮容器及虚构数据，无持久卷。
- 网关验证客户凭据拒绝、接管后权限变化、关闭会话搜索/历史可用而回复仍拒绝；adapter 验证搜索期间切号/撤销拒绝旧响应。
- 真实浏览器使用临时内存参考业务及固定回复：关键词找回等待会话，接管并关闭后按状态和部分编号找回历史，关闭后没有回复控件；不存在的关键词显示空结果。临时服务已停止，未调用模型或访问业务数据库。
- `go test -race ./internal/conversation ./internal/webchat` 通过；host-kit 全部 45 项通过、无跳过。
- 默认恢复回归通过：web-and-host 183、gateway-recovery 10、knowledge-core 82、knowledge-entry 8、knowledge-admin 9 个 pass 事件，均无失败/跳过。报告目录为本机临时 `omni-recovery-check-d87xWZ`；该运行未启用数据库模式，不能替代上面的独立 PostgreSQL 专项。通用恢复报告列出的跨标签切号/隐藏恢复人工项目本轮未重新执行。
- 五个受影响前端构建通过；`git diff --check` 通过。未提交、推送或发布。

## 阅读顺序

### PHP 7.4 隔离原站验收（2026-11-19）

在本机 CRMEB 原应用代码的隔离实例中，通过 HTTP 调用原客户和客服密码登录接口。未使用模拟身份，也未操作线上或原本机站点。PHP 7.4.33；独立 MySQL 8 从安装 SQL 建立 157 张表，不导入原始数据行；仅临时建表使用兼容日期模式和动态行格式。客服网关使用独立 PostgreSQL、固定回复。临时网络不发布端口。

- 一名虚构客户发送消息并转人工；坐席 A 搜到等待会话，接管并结束，再按关键词、部分编号和已结束状态查回同一条咨询。
- A 能读取原消息；关闭后发起回复返回 409。坐席 B 搜索结果为空，直接打开同一历史返回 403。
- A 通过原客服接口退出后，再用旧登录令牌打开历史返回 401。
- 以上是原站 HTTP 接口验收，不是 PHP 站点的浏览器点击验收。未调用真实模型，未复用业务数据库或原站私密配置，临时测试容器、数据库及网络均已清理。

### 实现文件

1. 本文：语义、接入范围和权限边界。
2. `internal/conversation/desk_search.go` → `internal/persistence/postgres/desk_search.go`：仓库查询契约。
3. `internal/webchat/desk_search.go` → `integrations/host-kit/adapter.mjs`：认证后的搜索链路。
4. `web/chat/src/desk-search.mjs` → `desk-search.jsx` → `desk.jsx`：身份核验、分页与历史入口。
5. 各层 `desk_search*test.go` / `desk-search.test.mjs`：隔离和失败路径。
