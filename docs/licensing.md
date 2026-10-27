# 项目许可说明

2026-10-27。权利人确认拥有 omni-agent-customer-service 与 omni-agent-kit 的授权权利，并明确同意两个项目采用 MIT。两个当前工作区均已添加标准 MIT 正文，版权署名沿用仓库作者名 QiuYiEr，年份为 2026。

## 适用边界

- 本仓库自有代码使用根目录 [LICENSE](../LICENSE)；第三方组件及素材的既有许可、版权和声明保持不变。
- [前端许可正文](../third_party/frontend-LICENSES.txt)及 [Go 根声明汇编](../third_party/go-root-LICENSES.txt)不是本项目 MIT 授权的一部分，不可据此把依赖全部重新标为 MIT。
- 没有删除或替换第三方许可；本轮仅通过正常 Go 模块流程更新 kit 版本和对应 go.sum，不改写缓存、旧验收包或其他依赖版本。

## omni-agent-kit 锁定版本

当前客服已锁定 `v0.1.0`。旧版本 `v0.0.0-20260805082658-f91829f653e0` 的模块包没有被改写；新版下载包已核对包含 MIT LICENSE。

2026-10-27 经授权已提交并推送 kit 的 MIT 许可变更，创建 `v0.1.0` 注解标签，指向 `53dfbc4d699bed154fbefbdf6a13c06d30b0936a`。公开前核对远端引用与全部 3 个提交、26 个历史 blob，有限敏感模式及已知私密值扫描无命中，远端议题/PR 均为空；历史作者姓名及邮箱保留。随后将 kit 仓库由 Private 改为 Public，匿名访问该标签 LICENSE 成功。此检查不是未知秘密不存在的绝对保证。

[kit v0.1.0](https://github.com/qiuyier/omni-agent-kit/tree/v0.1.0) 只增加 LICENSE、README 许可说明及 CHANGELOG，不修改 Go 代码或依赖；`go test ./...`、`go vet ./...` 通过。没有另建 GitHub Release 页面或分发预编译二进制。

此前正常 `go get` 曾遇到校验服务 404；2026-10-27 已成功完成正常下载与校验，客服 go.mod/go.sum 更新为 v0.1.0。不关闭校验、不添加 replace、不手填校验和。下载包 LICENSE 与 kit 工作区/标签版本逐字一致，`go mod verify` 通过，后续回归结果见 [更新验收](kit-v0.1.0-update.md)。

## 发布状态

“两个自有项目采用哪种许可证”的决策、kit 含许可证标签公开、客服依赖更新及本轮离线回归已完成；第三方来源/嵌套声明/runtime/素材检查及最终分发包复核仍未完成，见 [发布清单](release-readiness.md)。历史报告保留当时事实，以本说明作为后续更新。

本轮仅对 kit 提交、推送并公开 v0.1.0 标签；没有发布/推送客服系统。现有验收 tar 不含新增许可证与声明，不能直接当成最终公开分发包。
