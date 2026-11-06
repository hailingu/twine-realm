# 文档索引

本仓库建设**数字孪生的软件定义平台**：在网页画布中拖入标准中间件与服务组件，由 AI 完成数据模型设计、开发、测试与部署，画布即真实的 docker-compose / k8s 项目空间，并以运行状态实时回显构成业务系统的数字孪生。

平台开发在 `develop` 分支进行。`main` 分支承载的历史演示（能力实验室、模拟器）不构成本平台的约束或依赖，仅作背景参考。

## 目录结构

| 目录 | 职责 |
| --- | --- |
| [platform-design/](platform-design/README.md) | 平台设计：愿景、契约、运行与恢复、服务级 Git、技术栈工程文档、agents 规范加载、工作台与设计语言（本地登记切片已建立，其余原则、建议与待决分别标注） |
| [development/](development/README.md) | 开发规范：工作流、工程规范、Go / TypeScript 语言规范、工具链、质量检查与问题记录 |

## 阅读顺序

1. 根目录 [AGENTS.md](../AGENTS.md)：任务授权、范围路由、版本控制与底线规则。
2. [平台设计概览](platform-design/README.md)：理解平台要建什么、核心概念与不变量。
3. [开发规范](development/README.md) 索引：按任务类型阅读对应规范。

## 设计专题

- [组件、连线、接口与状态契约](platform-design/canvas-contracts.md)：参考 Infinity Canvas V3，适配具名端口、关系校验、原子编辑、首批机器契约和 SSE 恢复。
- [标准中间件 Schema 与服务运行契约](platform-design/standard-middleware.md)：预定义、版本化的配置 / 端口 / 连接 schema，细化 MySQL 注入、账号、迁移、就绪和应用访问；与业务数据库表结构分开。
- [运行、授权、资源生命周期与恢复](platform-design/operations-and-recovery.md)：持久操作与最终一致性、本机启动 / 安全、分离停止与删除、AI 权限 / 隔离 / 外发、备份恢复及分阶段故障验收。
- [服务代码仓库与版本追溯](platform-design/service-repositories.md)：每服务独立 Git、本地默认；首个切片最小登记与 commit 追溯，完整 Git UI 后续，GitHub 可选。
- [服务技术栈与工程规则](platform-design/service-engineering.md)：每种受支持技术栈提供对应工程最佳实践文档及离线版本，绑定规则集 / 模板与检查，统一 AI / 人工修改的审阅和追溯。
- [AI 开发的项目规范与 AGENTS 引用](platform-design/ai-development-standards.md)：每项目与每服务仓库引用适用规范，agents 任务前实际加载固定版本，记录读取证据、缺失阻塞、冲突与不可信内容边界。
- [AI 交互开发与简单代码编辑器](platform-design/code-editor.md)：项目 / 服务目录树、文本编辑与多标签，文件保存、未保存内容保护、AI / 人工冲突及安全路径边界。
- [工作台与页面布局](platform-design/workbench.md)：项目入口、组件目录、React Flow 画布、配置 / 运行 / AI 面板、变更审阅与事件日志。
- [设计语言](platform-design/design-language.md)：参考来源、独立语义 token、深浅主题、组件与画布规则、无障碍策略。

## 开发任务管理

[GitHub Issues](https://github.com/hailingu/twine-realm/issues) 记录具体工作与验收结论，[twine-realm Project #4](https://github.com/users/hailingu/projects/4) 管规划、排序与执行阶段。创建、关联、当前自动化及状态冲突处理统一见[Projects 与 Issues 协作](development/project-tracking.md)；本地 `issues/` 只保留历史、离线草稿和明确链接的快照，不成为第二份独立任务状态。

## 文档职责与同步

- 设计文档（`platform-design/`）定义目标与契约；开发规范（`development/`）定义实施要求。两者都必须区分**目标**与**已实现事实**，尚未落地的能力必须明确标注，不得把设计写成已完成。
- 实现与设计不同步时，在同一变更中更新对应设计文档与相关索引。
- 本仓库文档、代码注释与提交信息均使用中文。

当前运行见[根 README](../README.md)、[本地切片](development/local-project-slice.md)、[组件目录](development/component-catalog-slice.md)、[服务依赖声明](development/service-bindings-slice.md)、[MySQL 运行切片](development/mysql-runtime-slice.md)、[固定源码隔离构建](development/service-build-slice.md)和[Go 工程包 / 最小模板](development/service-profile-slice.md)。
