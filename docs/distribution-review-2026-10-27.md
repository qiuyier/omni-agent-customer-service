# 分发材料收尾检查（2026-10-27）

结论：已补齐本轮已知许可文本缺口，并建立逐文件公开审阅清单；尚不能把整个仓库或旧 tar 直接对外分发。本轮不修改运行代码、不启动服务、不调用模型、不提交或发布。

## 许可结果

1. 从上游精确提交 846f52bd97c6 读取 cloudwego/eino-ext/LICENSE-APACHE。此前查找 LICENSE 未成功，真实文件名为 LICENSE-APACHE；锁定子模块的 Apache-2.0 许可头与此一致。
2. 扫描前轮三个本机二进制涉及的 60 个模块本地目录，收集嵌套许可；排除误匹配的 license.json 测试数据和 notice_response.go 代码。加入 kit v0.1.0、上游 Apache 全文和本地 Go toolchain 许可，共 21 份，见 [补充声明](../third_party/supplemental-LICENSES.txt)。来源、版本和哈希见 [审阅证据](evidence/distribution-audit-2026-10-27.json)。
3. 前轮根声明汇编和六份页面 LEGAL 文件继续保留；补充文件不替换原声明。[前端完整许可](../third_party/frontend-LICENSES.txt)保持不变。
4. 交付基线为源码和已构建网页，不包含 Go 二进制、node_modules、Go runtime、Docker 镜像或构建工具。镜像由使用者自行获取。runtime 许可仅作参考，不等于全部平台二进制审查通过。
5. 不把完整模块图中的 146 项都称为实际再分发代码；MPL lightningcss 为构建依赖，未将其平台包纳入交付物。有限文件名扫描不能代替最终分发内容审查。

精确上游来源：[eino-ext/LICENSE-APACHE](https://github.com/cloudwego/eino-ext/blob/846f52bd97c6/LICENSE-APACHE)。未用主分支推断历史许可，未重新许可第三方代码。

## 历史素材与隐私

544 个历史归档文件混有实验脚本、固定输入和原始证据，不能整目录删除。本轮清单对 30 张历史截图、466 份归档 JSON/JSONL/TXT/LOG/CSV 逐项记录路径、哈希和暂缓公开决定。这是保守分发决策，不是声称逐张、逐条内容已经通过审查。

抽查 CRMEB 截图为历史验收页面，不是运行必需素材。有限文本扫描发现本机用户路径出现在 CRMEB 发现报告及 6 份历史 freeze.json；两处 credential-like 命中位于配置样板语境（恢复工具输入格式和接入样板），未判为真实凭据泄漏。扫描无命中不等于没有未知敏感信息。

没有删除、脱敏改写或重画原始证据。docs/crmeb-integration-discovery.md 的本机路径另列为公开版本待整理。运行示例、测试夹具和接入样板保留，不因字段名含 password 就删除。

## 清单与剩余阻断

[逐文件审阅清单](evidence/distribution-manifest-2026-10-27.json)基于当前工作区，不是干净发布 commit。记录候选文件哈希及 hold 决定；排除 .git、.env、.local、node_modules。candidate 只代表未被本轮排除规则拦截，不是逐文件合规认证。

剩余两项具体工作：

- 生成不含 hold 材料的干净源码交付目录，把指向被排除证据的引用明确标成内部历史资料或换成公开摘要，检查必需构建/启动引用。清单不是已制作或已验收的安装包。
- 冻结实际交付版本与清单、哈希，对最终包复核凭据、声明、链接和最小构建；若公开 Git 仓库，还需单独检查历史，排除当前文件不能清除 Git 历史。

kit 公开及依赖更新已完成，不重复安排。本轮未发现需要新增客服功能的阻断问题；未获授权不更改客服可见性、不创建发布标签。
