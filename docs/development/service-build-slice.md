# 固定源码隔离构建切片

Issue #14 在已保存的服务仓库关联与固定 commit 上建立本地镜像候选。它不生成服务源码、不提交原仓库、不推送镜像或部署业务。Todo 仍只是可替换示例。一期不接入账号、登录或 RBAC。

## 启用与本机边界

默认不启用执行器。第一组合为已发布 `go-net-http-mysql/1.0.0` 工程包、Go 1.27.3 + 标准库 net/http；控制面宿主须为 Mac/Linux 的非 root 可信 OS 用户，Docker 使用明确的本地 Unix socket、Linux daemon 与同 context 的单节点 docker-driver Buildx builder。初始化只核实工具和缓存，不创建业务资源、不自动安装或拉取。

1. 按平台 README 配置 `TWINE_REPOSITORY_ROOTS`、本机数据库及静态根。构建私有根须在仓库、平台源码、静态目录和 MySQL 运行根之外。
2. 显式准备工具链镜像：`docker pull golang:1.27.3@sha256:0c050feb47802ae436b9030833694a0b80160d08e3b3049ad30bf7c9e724e09d`。固定 digest 是多架构发布身份，实际 linux/amd64 或 linux/arm64 镜像 ID 另行核实。
3. 从 `server/` 执行 `go run ./cmd/build-init --directory /绝对路径/全新私有构建目录`。该路径必须不存在；初始化失败保留部分材料，不能换根接管旧记录。
4. 启动平台时显式注入 `TWINE_BUILD_ADAPTER=local-go-container-v1` 与 `TWINE_BUILD_DIRECTORY`。元数据库绑定安装身份及环境摘要，错误根、daemon 或环境不匹配时停止装配。

目录为当前用户的 0700，身份与目标为 0600；链接、权限改变、根替换和已有目录都不得静默修复。运行根与构建根互相排斥。恢复原根前保留备份，不删除运行数据库、数据卷或其他项目。

## 固定输入与真实检查

画布服务详情先明确保存完整 commit，再接受构建。当前 revision 只用于受理并发；输入摘要绑定项目、服务、原仓库身份 / 路径、固定 commit、完整工程方案身份、安装、工具环境和执行策略。仅拖动或保存布局不会改变同一源码的证明；固定 commit、方案、工具链、策略或安装改变不能沿用旧证明。

Git 读取原始 commit/tree/blob，导出到独占新目录，保留普通文件字节和执行权限，不运行 filter、hook 或 archive 替换。拒绝符号链接、子模块、特殊 / 越界路径及控制字符；最多 4096 文件、64 MiB。原工作区、AGENTS、索引、分支与 Git 配置保持原样；脏文件不参与。源码 SHA256 清单与实际 Git 版本进入检查追溯，Git 的原始 commit 身份仍单独保存。

完整工程规范、AGENTS 机器引用、工程方案锁和模块约束须与内置发布包相符。文档只提供约束，不能提供任意可执行 argv。随后使用平台固定命令进行以下检查：

| 阶段 | 实际行为 |
| --- | --- |
| 沙箱 | 固定工具链 image ID，非 root UID/GID，只读根、源码只读挂载，全部 capability 移除，no-new-privileges，无 socket、HOME、数据库秘密或其他项目挂载 |
| 依赖 | 固定 Go 环境、只读 go.mod/go.sum、公共 Go proxy/sumdb，禁止 VCS 回退、工具链自动下载与宿主环境覆盖；`go mod download` 准备主模块构建 / 测试所需依赖 |
| 断网 | 断开原 bridge，并实际核实网络集合为空后才执行业务代码；私有凭据代理和私有模块组合尚未支持 |
| 检查 | 实际 Go 版本、全部固定 Go 文件 gofmt、`go mod verify`、`go vet ./...`、`go test -race -count=1 ./...` |
| 编译 | `CGO_ENABLED=0 go build -trimpath -buildvcs=false`，输出 `./cmd/service` 静态 Linux 二进制；原模块始终只读 |
| 镜像 | 暂停并核实整个沙箱，提取二进制、核实 ELF 架构 / 静态属性 / SHA256，使用平台固定 scratch Dockerfile 与本地固定 builder；不执行服务 Dockerfile |
| 发布候选 | Buildx 元数据提供真实 manifest/index 与 config digest，另实际读取本地引擎 ID，按内容摘要、归属、架构与固定启动配置核实加载结果；同库发布候选仍受取消与执行围栏保护，完整候选JSON最多48 KiB以保留同库操作 / 回执载荷余量 |

策略 `go-container-local-v1/1` 固定：总执行 480 秒、每命令最多 180 秒、CPU 2、内存及 swap 上限 4096 MiB、PID 256、有界输出每流 1 MiB。Go 缓存 / 测试临时目录为 3072 MiB tmpfs，明确 exec/nosuid/nodev；执行生成的测试二进制不放宽容器能力。最终二进制使用独立、完整归属的 128 MiB local-driver tmpfs 输出卷，noexec/nosuid/nodev；没有宿主可写目录挂载。

Docker 官方说明 [docker cp 不支持容器内 tmpfs 等特殊挂载](https://docs.docker.com/reference/cli/docker/container/cp/#corner-cases)。本切片使用 daemon 挂载的独立限额输出卷，在暂停后提取；不会为绕过该限制取消冻结。所有沙箱及输出卷只在完整标签、名字、镜像、用户、挂载、资源限额、端点及 daemon 相符时清理。失败清理不覆盖原错误，也不能发布候选。

本地引擎 ID、config digest 与 manifest/index digest 分开保存。classic 存储通常以 config digest 作为本地 ID；containerd 存储的本地 ID 可为目标 manifest/index digest，见 [Moby 固定实现](https://github.com/moby/moby/blob/6a43e3d/daemon/containerd/image_inspect.go)。本切片只接受实际 ID 严格匹配本次 Buildx 已检查的两份内容摘要之一，后续查询还必须匹配原记录的实际本地 ID；不能因后端差异接受任意第三摘要、放宽归属或只检查 tag。

## 持久状态与恢复

机器契约位于 [v3 build](../../server/contract/v3/build.schema.json)、[输入](../../server/contract/v3/build-inputs.schema.json)与[产物](../../server/contract/v3/build-artifacts.schema.json)，与旧画布、源码、运行契约并存。原键、目标、操作、步骤 / 版本及事件在同一平台 MySQL 事务提交后，后台才可导出或调用 Docker。构建模块使用独立迁移历史，仍在同一元数据库。

| 前置与动作 | 可观察结果 | 不变量 / 所有者 |
| --- | --- | --- |
| 相同键、相同意图重试或丢失响应 | 原回执与原操作；异意图 409 | 数据库唯一键，同事务受理；提交未知不开始下一外部步骤 |
| accepted → 领取 → 每项检查 | executing 与独立步骤、单调版本；202 不等于成功 | SQL 当前指针 / 执行代次 / 租约 + 操作文件锁 |
| 检查或规范失败 | failed 或 needs_attention，固定诊断，无本次候选 | 不能降低检查阈值、忽略告警或把空结果当作通过 |
| 明确取消 | 先 cancelling，实际收回沙箱后 cancelled | 独立 mutationId、原操作身份与已观察版本下界；进度前进不使取消失效，未来版本和旧操作仍拒绝；长命令期间监测取消，迟到成功不能发布 |
| 浏览器关闭或换服务 | 客户端读取消，后台继续；重开从持久快照恢复 | 前端与后台生命周期分开，轮询不覆盖画布 / 连线草稿 |
| 平台退出、进程丢失、过期租约 | 核实原资源并收回，原检查结果缺失则 needs_attention | 不自动重跑任意业务代码；明确取消原未知操作后，才能确认新代次 |
| 外来同名 / 改名资源或 daemon 改变 | 保留原对象并报错 | 不能按名称前缀、空列表或“进程消失”推断归属 / 成功 |
| 成功后相关输入改变、原对象 / 镜像失联 | 保留历史候选，candidateCurrent=false，附明确原因 | 历史成功与当前可用性分开；本地镜像存在也不等于已部署或健康 |

路由为 `/api/v3/build/capabilities`、服务下 `build` / `builds` / `builds/{operationId}` / `builds/{operationId}/cancel` 及 `/api/v3/build-mutations/{mutationId}`。前端只接受生成 schema 及跨字段归属检查通过的响应；未知提交保持原意图、原键核实 / 重试。操作和画布使用各自版本，不混用 revision。

只返回安全诊断和内容摘要；有界工具失败详情仅保存到私有根 0600 文件，不进入 API、事件或前端。构建源副本、诊断、候选和本地镜像保留以便追溯；本切片不提供自动回收历史材料的产品入口。临时沙箱和输出卷在执行结束后清理。人工处理保留材料须先核实归属和备份，不能泛化删除 Docker 资源。

## 验证与后续边界

`npm run test:platform` 显式准备固定工具链缓存，初始化本次全新运行 / 构建根，运行真实 Git、SQL、Go 容器、Buildx 和 Chromium；产品初始化仍不自动拉取。浏览器覆盖原键丢响应、脏源码、布局草稿、候选重开 / 重启、输入失效、取消与控制面退出。测试只清理本次随机安装的候选 tag；有沙箱 / 输出卷尚未确认收回时，保留私有根并使检查失败。

完整提交门禁仍包括零代码告警、根 / web 审计、真实平台回归与本次 Sonar。模块完整性检查不等于漏洞库扫描；远程仓库、私有依赖认证、任意框架、跨架构模拟执行、镜像推送、业务迁移 / 部署、全局 SSE 和项目级停止恢复不属于本切片。后续部署须再次核实当前候选、原镜像和独立运行配置。
