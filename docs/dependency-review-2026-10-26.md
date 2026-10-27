# 依赖许可与分发材料检查

后续更新（2026-10-27）：权利人已确认两个自有项目采用 MIT，当前工作区已补齐 LICENSE，见 [许可说明](licensing.md)。本文以下保留 2026-10-26 的审查事实；旧锁定模块包未被重写，第三方审查未因此自动完成。

2026-10-26，核对版本 `d7075ec5398cb88df57040a2de192c2e3bd71eb3`。本轮仅增加清单/声明/文档，不修改运行代码，不发布。结论为**部分完成、仍有明确阻断**，不是法律合规保证。

## 范围与证据

- `GOPROXY=off GOSUMDB=off go list -m -json all` 得到 146 个非主模块；未联网下载模块。未缓存模块不能视为没有许可证。
- 前轮三份验证二进制的 `go version -m` 合计 60 个第三方模块，版本为 f4a422c、darwin/arm64、实际工具链 go1.27.0。后续仅文档变化；不是新编译或 Linux 验证，也不是证明 Go 1.25 实际构建通过。
- package-lock.json 包含 96 项，全部有 license 元数据，含开发工具和多平台可选包；不等于 96 项都进入浏览器包。
- [机器清单](evidence/dependency-inventory-2026-10-26.json)记录版本、是否进入上述二进制、根许可文件及 SHA-256。分类来自有限文本识别，BSD-like 等不是精确 SPDX 认证。没有把本机路径或凭据写入清单。
- 六份已提交 `.LEGAL.txt` 保留 React/react-dom/scheduler 版权注释，但不是完整许可正文。CSS 带 Tailwind MIT 标记。本轮补入 [前端完整许可](../third_party/frontend-LICENSES.txt)，对应 React 19.3.0、react-dom 19.3.0、scheduler 0.28.0、Tailwind 4.3.3 的已安装包 LICENSE 原文。
- [Go 根许可汇编](../third_party/go-root-LICENSES.txt)原样保留 58 个已查到根文件的二进制依赖模块所提供的 60 份许可/声明，包括 Hertz NOTICE。不能替代嵌套 vendored 文件、Go runtime 或平台库逐项审查。

## 具体发现与关闭条件

| 项目 | 发现 | 发布前关闭方式 |
| --- | --- | --- |
| 仓库本身 | 未找到根 LICENSE | 所有者决定开源许可证或其他分发条件，不由本轮自动授予 |
| omni-agent-kit | 锁定 `v0.0.0-20260805082658-f91829f653e0` 包内未找到许可文件，且进入二进制 | 权利人确认该版本的授权依据；不能把本地仓库未来新增 LICENSE 自动当成旧锁定包已带授权 |
| eino-ext/libs/acl/openai | 锁定包根无许可文件；chat_model.go 有 CloudWeGo Apache-2.0 许可头 | 核对精确提交及适用全文/NOTICE；本轮尝试读取上游固定提交 LICENSE 未成功，不用当前主分支替代历史证据 |
| 依赖深层声明 | 本轮仅采集根文件；部分完整图模块未缓存 | 依据最终分发内容核对嵌套来源、Go runtime 和第三方声明，不能仅靠根目录扫描盖章 |
| lightningcss | 锁文件的本体及平台包标注 MPL-2.0，均为开发依赖 | 当前不分发 node_modules 或工具二进制；若以后分发构建工具，单独处理相应许可与源码提供要求，不推断整个客服系统因此必须采用 MPL |
| 历史截图/证据 | 跟踪树中有 30 个 png/jpg/jpeg/woff/ttf 匹配文件，本轮未逐张审阅 | 最终交付清单须审阅素材来源及隐私；未审阅的历史截图/日志不直接对外打包 |

许可条文依据：[MIT 官方文本](https://opensource.org/license/mit)要求保留版权及许可声明；[Apache-2.0 第 4 节](https://www.apache.org/licenses/LICENSE-2.0)规定分发时的许可、修改标记及适用声明保留；[Mozilla FAQ](https://www.mozilla.org/en-US/MPL/2.0/FAQ/)说明 MPL 文件级范围及分发情形。这里用于工程检查，不替代针对最终交付物的法律判断。

## 本轮未执行

没有选择或改写项目/omni-agent-kit 的许可证，没有升级依赖，没有重新打包已发布版本，没有验证所有可选平台包，没有重跑模型、浏览器、数据库或服务。前轮源码 tar 是验收快照，**不是可不经许可和素材审查就公开的发布包**。

最小下一步：所有者先确认两个自有项目的权利与许可方向；随后补齐上述第三方来源/声明及素材筛选，按 [候选分发说明](v1-distribution.md)重新冻结分发清单和校验和。此轮清单不能将 R3/R4 自动关闭。
