# CRMEB 知识同步：完整快照、过滤与原子版本切换

这是独立接入工具，不修改 CRMEB 源码或客服核心，不注册业务工具，不调用模型。
导出读取数据库；同步仅读取文件。凭据从服务端配置加载，不进命令参数、日志或知识文件。

## 两步执行

前提：PHP 8+、pdo_mysql；使用受限 SELECT 账户优先。配置文件为 CRMEB 风格 INI，
`[DATABASE]` 包含 hostname、hostport、database、username、password、prefix，支持 CR/CRLF/LF。
配置、快照和输出目录必须私有；数据库网络必须可信。当前导出器未配置远程 TLS，不能直接用于公网数据库。
不自动执行 .env 内容，也不展开环境变量或 shell 表达式。

```sh
php scripts/crmeb_knowledge_export.php \
  --config=/private/config/crmeb.env \
  --source-id=store-a \
  --output=/private/knowledge/snapshot.json

go run ./cmd/crmeb-knowledge-sync \
  -input /private/knowledge/snapshot.json \
  -source-id store-a \
  -keywords /private/config/article-keywords.json \
  -output /private/knowledge/store-a
```

路径是示例，需替换为实际私有目录，导出文件父目录必须预先存在。
容器运行可将脚本和配置只读挂载后使用同样命令。通过 stdin 运行 PHP 时需用
`php -f /dev/stdin -- --config=...`，不要用省略脚本入口的形式。
必须检查导出退出码再同步；若导出失败，不应把上一次导出误当成本次新数据。

可选关键词文件格式：`{"1":["自提"],"2":["预约"],"3":["售后","退货"]}`。
键是文章 ID，上述仅适用于本机测试文章，生产需维护实际匹配关系。
没有配置关键词时使用完整标题，只能保证格式合法，不能保证自然提问召回。

客服知识路径设置为 `输出目录/current/faq.json`。文件均 0600，新目录 0700。
每个来源和每种运行模式使用独立输出目录；`source-id` 是管理员配置的稳定身份，不取自客户输入。

## 数据与发布规则

导出采用 InnoDB 一致性只读事务，直接查询 article/category/content，不经过会增加浏览次数的 HTTP API。
是包含所有文章状态的完整快照，不分页截断；超过 1000 条、分类或正文缺失、复杂/不匹配分类 ID 等均失败。
表前缀可配置；当前只支持文章单分类模型，不会把字符串 `1,2` 猜成分类 1。
快照最大 16 MiB，包含 source_id、complete、record_count、exported_at；不导出账号、订单或客户数据。
快照包含未发布正文，仍须视为敏感数据；过滤发生在进入 FAQ 时，而不是把导出文件公开。

同步要求快照完整、来源一致、计数一致、ID 唯一、状态字段完整、时间不超过24小时且不倒退。
正常模式只保留文章 status=1/hide=0 且分类 status=1/hidden=0/is_del=0 的记录，
并始终排除保留前缀 `omni-fixture-` 的测试标记。
展示状态不是内容发布授权审查，真实资料上线前仍需业务方确认其有效性。

显式 `-test-marker omni-fixture-20260928-v1` 可导入本次隐藏测试文章，
只匹配该标记且分类未删除的记录，不导入其他正常文章。与正常模式不能共用已发布的输出目录。
正文经 HTML 实体解码并提取纯文本，移除 script/style/iframe/object/svg/template；
不请求图片或链接。它不是业务真实性或提示注入检测器，资料仍是非可信参考数据。
所有条目复用现有 FAQ 校验；过长或不合法内容失败，不静默截断。

## 四类同步行为

- 新增：下一份完整快照中的合规条目进入新版本。
- 修改：同一来源文章 ID 保持稳定，文本替换，记录输入与输出哈希。
- 撤回：文章隐藏/停用、分类隐藏/停用/删除、文章从完整快照消失，都从新 FAQ 中移除。
- 失败恢复：导出失败不覆盖旧快照；同步校验失败不切换 current；下一次有效快照可重新同步。

输出包含不可变 `generation-*` 目录，各有 faq.json 与 metadata.json，记录来源、模式、导出/生成时间、数量和哈希。
先写完整版本，再原子替换 current 符号链接；历史版本保留用于审计，不自动清理。
以目录锁排除同目录并发。进程崩溃留下 `.sync-lock` 时停止并人工核对，没有自动抢锁。
需要可信的本地 POSIX 文件系统，不支持 Windows、共享网络目录或分布式锁。
这是进程级发布保护，不承诺断电下目录元数据持久性；磁盘/网络文件系统故障需另做恢复验收。

## 重要：文件同步不等于运行时热更新

现有客服启动时加载 FAQ，已运行进程不会因 current 改变就自动丢弃旧知识。
生产更新需要协调服务重载/重启及回归；本工具不会擅自重启服务。
紧急撤回时必须先停止使用旧知识的服务，不能只改文件后宣称旧答案已不可用。

结果为空默认报错并保留旧版本，需要人工确认是预期全部撤回而非来源故障。
确认后用 `-allow-empty` 发布 `[]`。现有 FAQ 加载器不接受空知识库，重启会失败关闭，
不会自动切换成无约束回答或沿用旧文件。正在运行的服务仍须先停用/替换，才能真正完成全部撤回。

## 验证与限制

```sh
go test -count=1 ./cmd/crmeb-knowledge-sync ./internal/knowledge/faq
go test -race ./cmd/crmeb-knowledge-sync
go vet ./cmd/crmeb-knowledge-sync
php -l scripts/crmeb_knowledge_export.php
```

自动测试覆盖新增、修改、单条隐藏、完整删除、显式空集、失败保留及恢复、旧快照、模式混用、
状态字段缺失、重复 ID、HTML 文本化和目录锁。修改/删除用合成快照，不在 CRMEB 中修改真实记录。

2026-09-28 本机实测：只读导出既有3篇测试文章；测试模式生成3条，正常模式全部排除，
显式空集生成0条。初次查询遇到 MySQL 1267（分类字段和连接排序规则不一致），
已改为分类 ID 的精确二进制字符串比较后导出成功；没有为兼容而修改数据库排序规则。
该实测验证数据库到文件的适配，不是付费模型回归、定时任务或部署完成。

阅读顺序：本说明 → `scripts/crmeb_knowledge_export.php` → `cmd/crmeb-knowledge-sync/main.go`
→ `cmd/crmeb-knowledge-sync/main_test.go`。
