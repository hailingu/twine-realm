# 运行时与工具链

本文定义平台各运行时与工具的版本基线、锁定位置与升级政策。版本原则：**采用官方当前最新稳定线**，LTS 优先于创新版；具体补丁版本在对应清单中锁定。本文于 2026-11-04 核对官方发布，后续升级须重新核对。

## 版本基线

| 组件 | 基线 | 本机默认 / 本次验证（2026-11-04） | 差距与动作 |
| --- | --- | --- | --- |
| Go（平台服务） | **1.27.x**（验证 1.27.3） | 默认 1.25.6；本任务独立工具链 1.27.3 | `server/go.mod` 锁定 1.27，未替换全局工具 |
| Node.js（前端与工具） | **26 LTS** | 默认 22.15.0；本任务 26.13.0 | `.nvmrc` 和根 / 前端 engines 已同步，未替换全局工具 |
| npm | Node 26 随附 11.20.0 | 默认 10.9.2；本任务 11.20.0 | 根与 web 各自锁文件，单一包管理器 |
| MySQL（平台元数据及首个项目实例） | **9.7.4 LTS** | 两类独立临时容器验证 | `deploy/compose.yaml`、内置目录和运行适配器固定相同 tag + digest；网络、卷与凭据隔离，项目库不发布宿主端口 |
| Docker / Compose | Docker 29.x / Compose v5.x | 29.9.2 / v5.6.1 | 已满足；compose 文件锁定 `version` 语义，不依赖已弃用的顶层 version 字段 |
| React / React Flow | React 19.3.1 / `@xyflow/react` 12.12.1 | 已建前端工程 | `web/package.json` / 锁文件固定实际依赖 |
| SonarQube 服务（本地门禁） | 本地 docker 社区版 | 26.8.0（`localhost:9000`，项目 `twine-realm` 已建立） | 已满足；服务地址与凭据只经环境注入，不入库 |
| sonar-scanner CLI | 7.x | 7.3.0 | 已满足；可用 `SONAR_SCANNER_BIN` 覆盖路径 |

## 锁定位置

| 内容 | 位置 |
| --- | --- |
| Go 工具链与依赖 | `server/go.mod`、`server/go.sum` |
| Node 版本 | `.nvmrc`（仓库根） |
| 仓库工具依赖与脚本 | 根 `package.json`、`package-lock.json` |
| Sonar 项目标识与扫描范围 | 根 `sonar-project.properties`（服务地址与凭据除外，只经环境注入） |
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

Issue #1 使用任务目录内独立工具链完成验证，不自动升级本机全局环境。MySQL 版本依据 [官方 9.7.4 发布说明](https://dev.mysql.com/doc/relnotes/mysql/9.7/en/news-9-7-4.html)，其他固定补丁以清单为准。
