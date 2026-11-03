# 运行时与工具链

本文定义平台各运行时与工具的版本基线、锁定位置与升级政策。版本原则：**采用官方当前最新稳定线**，LTS 优先于创新版；具体补丁版本在对应清单中锁定。本文于 2026-11-03 核对官方发布，后续升级须重新核对。

## 版本基线

| 组件 | 基线 | 本机现状（2026-11-03） | 差距与动作 |
| --- | --- | --- | --- |
| Go（平台服务） | **1.27.x**（最新稳定 1.27.3） | 1.25.6 | 初始化 `server/` 前升级本机 Go 到 1.27.x，`go.mod` 锁定 `go 1.27` |
| Node.js（前端与工具） | **26 LTS**（Active LTS，26.13+） | 24.18.0（Maintenance LTS） | 初始化 `web/` 前升级，`.nvmrc` 固定版本，`engines` 开启严格检查 |
| npm | 随 Node 26 附带版本 | 11.16.0 | 升级 Node 后核对，`package-lock.json` 锁定 |
| MySQL（平台元数据） | 最新 LTS 系列 | 未安装（本机经 docker 使用） | 平台自用元数据库经 deploy compose 定义并锁定镜像 tag；画布内业务 MySQL 属于中间件目录，版本由目录定义 |
| Docker / Compose | Docker 29.x / Compose v5.x | 29.9.2 / v5.6.1 | 已满足；compose 文件锁定 `version` 语义，不依赖已弃用的顶层 version 字段 |
| React / React Flow | React 19.x / `@xyflow/react` 12.x | 未安装 | `web/package.json` 安装时锁定最新稳定补丁 |

## 锁定位置

| 内容 | 位置 |
| --- | --- |
| Go 工具链与依赖 | `server/go.mod`、`server/go.sum` |
| Node 版本 | `.nvmrc`（仓库根） |
| 前端依赖与脚本 | `web/package.json`、`web/package-lock.json` |
| 平台自用 MySQL 镜像 | `deploy/` 下的 compose 定义 |
| 中间件目录版本 | 目录定义文件（后续建立），每类中间件锁定镜像 tag |

## 升级政策

- 升级任何基线组件属于明确批准的任务范围，须满足：核对官方发布说明与破坏性变更、完成对应范围路由的适用验证、同一变更内更新本文与相关清单。
- 依赖升级不做「顺手升级」：与任务无关的依赖保持不动，批量升级单独立项。
- 版本基线变化时，同步更新根指南 AGENTS.md 中引用的工具链说明。
- 不引入与基线冲突的重复工具（如再引入 pnpm/yarn/bun）；单一包管理器原则。

## 本机环境说明

- 本机项目空间编排以 docker-compose 为一等公民；`docker compose` 子命令可用。
- 平台服务本地运行所需的 MySQL 也经 docker 提供，不在本机裸装数据库服务。
- Go 与 Node 的升级动作（安装工具链）属本机环境变更，执行前与仓库所有者确认方式。
