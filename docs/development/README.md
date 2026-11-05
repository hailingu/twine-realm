# 开发规范

本目录集中维护平台开发的中文规范。根目录 [AGENTS.md](../../AGENTS.md) 定义任务授权、范围路由与版本控制政策；本目录补充工程实施要求，不额外授予提交、推送、发布或外部写入权限。

## 当前状态

已建立 [Issue #1 本地切片](local-project-slice.md)：Go 服务、React / React Flow 画布和平台库 Compose。根 `test:platform` 验证真实数据库、Git、浏览器与重启；`quality:gate` 另含本次 Sonar。其余长期能力仍为目标。

平台目标形态见[平台设计概览](../platform-design/README.md)。`main` 分支的能力实验室、模拟器是历史演示，不约束新实现，也不作为可复用代码来源。

## 阅读顺序与适用范围

| 文档 | 何时阅读 | 主要内容 |
| --- | --- | --- |
| [本地项目切片](local-project-slice.md) | 运行 / 调试 Issue #1 | 契约、持久化、只读 Git、恢复和一期安全 |
| [开发工作流](workflow.md) | 开始任务、规划切片时 | 技术方向、状态矩阵、独立分支 / worktree、PR 合入 develop、同步与安全清理 |
| [运行时与工具链](runtime-toolchain.md) | 初始化工程、安装或升级依赖时 | Go / Node / MySQL / Docker 版本基线、本机现状差距、锁定位置 |
| [软件工程规范](software-engineering-standard.md) | 修改生产或测试代码前 | 职责、依赖、规模、抽象、状态与错误边界 |
| [Go 规范](go-standard.md) | 平台服务与后端代码变更时 | 项目布局、错误处理、context、并发、数据库与 API 边界 |
| [TypeScript 规范](typescript-standard.md) | 前端画布与 TS 工具变更时 | 严格类型、React 与 React Flow 投影、Promise 与错误处理 |
| [零告警提交门禁](warning-free-commits.md) | 检查和提交前 | warning 拒绝、依赖漏洞清零、真实 Git hook 与负例 |
| [质量检查与证据](quality-gates.md) | 验证、提交与完成报告前 | 当前已接入（工具链门禁）与计划接入的检查、证据与报告边界 |
| [问题记录规范](issue-standard.md) | 记录缺陷、功能、债务或评审意见时 | GitHub Issue 的内容、验收与关闭证据；本地历史 / 草稿格式 |
| [Projects 与 Issues 协作](project-tracking.md) | 创建 / 领取 / 跟踪 / 关闭任务时 | Project #4 与 Issue 的职责、去重关联、实际自动化配置与状态冲突修复 |

所有代码先读通用工程规范，再读对应语言规范。

本目录约束平台自身开发；用户业务服务按所选技术栈采用[对应的工程最佳实践文档包](../platform-design/service-engineering.md#每种受支持技术栈的工程最佳实践文档)，每服务仓库 `AGENTS.md` 引用适用规范，agents 开发前加载固定版本并记录。业务通用条款须经范围映射后发布，平台专属 Go / React、目录与工具命令不自动套用；当前尚未发布业务技术栈文档包或实现自动加载。

## 文档职责

- 规范定义实施要求；工具与实现事实由 `go.mod`、`package.json`、构建配置与源码决定。发现差异时修正规范或在对应任务中补齐工具，不得虚报通过。
- 新增规范须登记本索引；修改技术栈、脚本、目录或工具版本时，同步更新根指南的范围路由与相关说明。
- 规范文档自身采用结构化评审：核对一致性、路径与链接、真实命令、适用范围和未完成事项。
