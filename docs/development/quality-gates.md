# 质量检查与证据

本文定义平台的检查现状、计划接入的门禁与完成证据的口径。**核心原则：报告只列实际运行的命令与真实结果；未配置的检查如实说明，不得虚报、不得臆造命令、不得复用历史绿色结论。**

## 当前状态（2026-11-06）

已建立 Go / TypeScript 工程、平台元数据库 Compose、[Issue #12 项目 MySQL 运行检查](mysql-runtime-slice.md)、[固定源码构建](service-build-slice.md)及[单服务 MySQL 部署检查](service-deployment-slice.md)。根 `.nvmrc` 锁定 Node 26.13.0（npm 11）；`server/go.mod` 使用 Go 1.27。以下命令在根执行，模块单独命令见[本地切片](local-project-slice.md)和[运行说明](../../README.md)。检查存在不等于本次已经通过，交付时必须报告实际结果。

| 范围 | 命令 | 说明 |
| --- | --- | --- |
| 工具脚本行为测试 | `npm run test:quality` | 原生 Node 测试覆盖 Sonar 门禁、完整门禁编排与 issue 状态检查，涵盖失败传播、报告隔离与凭据脱敏 |
| 本地 issue 元数据与状态 | `npm run issue:status` | 仅解析 `issues/` 的历史 / 草稿文件头，校验必填字段、枚举、编号唯一和日期，报告本地状态（`--json` 可用）；不读取 GitHub / Project，不证明远端总数、同步、验收或证据质量，未关闭记录不构成失败 |
| 单独 Sonar 复核 | `npm run sonar` | 只扫描与核对本次分析；不自动复用本地覆盖率报告，不替代完整门禁 |
| 平台完整检查 | `npm run test:platform` | 临时平台库、全新运行安装、gofmt / build / vet、契约、TS / lint / Vitest / Vite、真实 Chromium、Compose / 认证 / 数据见证 / 创建响应丢失 / 重启、SQL 迁移 / 回滚 / 围栏与 Go `-race`；不接入开发数据 |
| 固定源码隔离构建 | 完整 `test:platform` | 固定 commit / 原仓库指纹、真实断网 Go 检查及镜像摘要、SQL 原键 / 取消 / 旧租约围栏、浏览器草稿 / 重开 / 中断；见[构建切片](service-build-slice.md) |
| 单服务 MySQL 部署 | 完整 `test:platform` | v4 预览 / 原键 / 目标围栏、用途秘密与最小权限、迁移内容核实、真实 Compose / 独立回环入口 / CRUD / 数据保留恢复及浏览器；见[部署切片](service-deployment-slice.md) |
| Go 业务工程包 | `npm run profile:check`；完整 `test:platform` | 固定规范 / 模板摘要、实际导出、Go 版本 / 格式 / 模块 / vet / race / 构建、独立用途账号、真实迁移与 HTTP / SIGTERM / 重启；见[模板切片](service-profile-slice.md) |
| 提交前门禁 | `npm run precommit`；`npm run hooks:install` | 全部代码告警和依赖漏洞清零；真实 Git hook 还核对索引前后一致，失败不提交；见[详细规则](warning-free-commits.md) |
| 完整质量门禁 | `npm run quality:gate` | 同次脚本覆盖率、issue 校验、平台检查与真实 Sonar；失败立即停止，只清理归属已确认的临时资源，清理未知时保留绑定文件并失败 |

除上述自动检查外，文档与规范变更仍做结构化评审：核对一致性、路径与链接、真实命令描述、适用范围和未完成事项。GitHub Issue 与 Project 的关联、状态和关闭结果按[协作规范](project-tracking.md)独立读取核对；现有 CI、脚本与 Sonar 不承担远端同步，本次未改变其行为。

## Sonar 门禁

项目标识为本地 SonarQube（社区版 26.8，`localhost:9000`）的 `twine-realm`，由根 `sonar-project.properties` 维护。扫描维护脚本、Go 服务和 TS 前端，单独分类测试与生成声明；Go / LCOV 只导入本次真实报告，Go 还合并浏览器平台进程的原生覆盖率，前端路径转换到仓库基准。平台无 Java，扫描器不传字节码参数；保留适配器已有能力。

Issue #10 的模板保留真实 Go 源文件，位于 `server/` 扫描范围；其独立导出模块及真实子进程的覆盖率同次合并回原模板路径。模板检查不继承模型、云端或外层 Git 凭据；本次测试管理身份只供受信内置模板集成夹具创建和清理自有对象，不交常驻应用或迁移子进程。模块完整性验证不等同于 Go 漏洞数据库审计。

`server/internal/httpapi/testdata/` 的 Go 文件仅是集成测试导出到临时独立仓库的业务夹具，不属于平台路由或已发布工程包。它们在 `sonar.test.inclusions` 中归为测试代码；仍进入相应静态分析，不作为平台生产覆盖率分母。平台源码和已发布模板继续按生产代码采集覆盖率，不增加覆盖率排除项。源码与测试范围分离按 [Sonar 官方范围规则](https://docs.sonarsource.com/sonarqube-server/project-administration/adjusting-analysis/setting-analysis-scope/excluding-files-based-on-patterns)维护；交付须报告这一分类修正与实际新增测试，不能把分类变化全部称作测试覆盖增长。

- 本地终端从 `~/.zshrc` 加载 `SONAR_HOST_URL` 与 `TWINE_REALM_SONAR_TOKEN`，也支持专用于本次运行的 `SONAR_TOKEN`。凭据只经扫描器环境与 API 认证头使用，不放入命令行、配置文件或报告；诊断输出自动脱敏。无凭据或服务不可达时门禁失败，不得输出 shell 配置全文。
- 扫描器等待服务端 Quality Gate（默认 Sonar way），再按本次任务的 analysisId 确认结果为 `OK` 且项目全部未解决代码问题为 0；任务、项目、报告、HTTP、状态不明或扫描失败均返回非零。不采用项目上一次绿色结果，不放宽服务端门槛。默认等待 300 秒，`SONAR_GATE_TIMEOUT_SECONDS` 可设 1–3600 秒；扫描器可通过 `SONAR_SCANNER_BIN` 指定（本机 sonar-scanner CLI 7.3.0）。
- 覆盖率结论核对采集范围与本次服务端分析；缺失报告拒绝扫描，不回退历史 `coverage/`。2026-11-03 的历史初始分析与脚本分析不能证明新增平台已通过；本次门禁分析 ID 和结果在交付报告中记录，不调整服务端门槛。
- 纯文档编辑仍可仅结构化评审、链接与空白检查；按用户新增要求，真正提交前统一执行零告警门禁，不保留跳过提交检查的例外。包含源码或配置（含 `scripts/`、`package.json`、`sonar-project.properties`）的变更须通过适用检查与完整门禁；混合变更不能按纯文档豁免。

## 持续集成（GitHub Actions）

`.github/workflows/ci.yml` 面向 develop / main 的 PR 与 push 运行 `tooling` 和 `platform` 两个 job（`ubuntu-latest`，Node 版本取自 `.nvmrc`，npm 缓存，action 按提交 SHA 钉版），执行范围路由的工具链与 issues 行检查：`npm audit --audit-level=low`（任意 low / moderate / high / critical 漏洞或审计执行错误阻断；无重试包装，审计端点瞬时网络故障按失败处理，如实重跑）→ `npm ci` → `npm run test:quality` → `npm run issue:status`。范围路由检查因此在 GitHub 上有独立、绑定提交 SHA 的记录，不再只依赖本机环境。

Sonar 扫描与完整门禁不进 CI：Sonar 服务在本机，GitHub 托管 runner 不可达；覆盖率与 Sonar 证据由本地门禁保留，CI 不重复扫描也不削弱本地门禁。`.github/dependabot.yml` 按 npm（根及 web）、gomod（server）与 github-actions 周粒度（周一，Asia/Shanghai）开依赖更新 PR 到 `develop`，只开 PR 从不自动合并，合入仍须通过 CI；minor/patch 成组降噪，major 单独评审。

新增 `platform` job 从 `server/go.mod` 和 `.nvmrc` 取得固定工具链，安装前端锁文件 / Chromium，执行 `npm run test:platform` 与仅配置解析的 Compose 校验；不连接本机 Sonar。CI 配置已建立，每次交付须读取最新 PR head 的真实 GitHub 运行结果；本地验证不能冒称 CI 已绿。分支保护必需检查尚未配置，启用属仓库设置变更。

## 模块检查与后续门禁

Go / TS 已接入根编排，compose 配置单独只读校验。项目 MySQL、固定源码镜像候选及单个 Go 服务的真实部署均已接入对应检查；它们各自有独立受理、执行与验收边界，不把数据库就绪或镜像构建当成业务部署成功。[Issue #18 状态切片](project-state-slice.md)另覆盖连续通知、RR信封、真实长连接 / 窗口与浏览器断网 / 平台重启。[Issue #20 生命周期切片](project-lifecycle-slice.md)补充原资源停止/恢复、外部未知、迟到观察围栏和跨项目隔离的真实验收；运行版本替换仍待后续。

| 范围 | 门禁 | 接入时机 |
| --- | --- | --- |
| Go（`server/**`） | 根 `gofmt -l server` 零差异；模块内 build、`go vet -tags=integration ./...`、`go test ./...`；完整检查含真实 MySQL 和 `-race -tags=integration` | 已接入 |
| TS（`web/**`） | `contract:check`、`typecheck`、`lint`、`test`、`build`；根平台检查含真实 Playwright | 已接入 |
| 项目空间编排 | 真实 Compose、认证 / 数据见证、未知结果、重启、归属冲突及保留数据恢复 | MySQL、单服务部署与项目保留数据停止/恢复已接入 |
| compose / 部署文件 | `docker compose --env-file .env -f deploy/compose.yaml config --quiet`；完整平台检查中的受控业务 Compose | 平台库配置只读校验；业务实际隔离、配置、入口与归属另经真实运行核实 |

部署检查结束后，先按本次随机运行安装、构建安装和 daemon 身份核实业务容器及入口网络，再清理本次构建和 MySQL 资源。只有完整标签、确定名称、实际镜像和用途配置均属于本次测试才删除；未知时保留材料并失败，不清理用户服务、源仓库或既有数据。新增清理脚本行为测试同时进入根 `test:quality` 和完整门禁的覆盖率清单，避免只有独立命令覆盖而提交路径遗漏。

## 证据与报告口径

- 报告包含：实际运行的命令、工作目录、环境版本、结果摘要、未运行项与原因。
- 优先使用稳定证据：提交号、符号、标题、命令输出摘要、Sonar 任务与分析标识；可变行号仅作辅助。
- 完成的判定标准：请求的行为、必需的文档、必需的检查与必需的证据全部就绪——而非仅仅完成一次实现尝试。
- 跳过、受阻或失败的检查连同完整原因一并报告；凭据缺失、环境故障不能绕过门禁，如实报告即可。
- 纯文档编辑做结构化评审与链接检查；实际提交仍统一经过零告警门禁。

## 禁止事项

- 禁止把目标门禁（如「计划接入 golangci-lint」）描述为已执行。
- 禁止以构建成功替代类型检查、以单测通过替代集成验证、以 mock 调用断言替代真实边界结果。
- 禁止为通过检查降低断言强度（删除边界场景、放宽超时、更换更轻替身）后宣称问题解决。
- 禁止复用历史覆盖率或上一次绿色 Sonar 结果作为本次门禁证据；禁止把模拟替身输出当作真实 CLI 证据。

## 生命周期真实验收的分组

Issue #20 早期生命周期套件曾独立实测约 417 秒；这是旧分组的历史数据，不代表当前逐例串行耗时。当前平台检查先用运行器真实列举核对完整审核清单：普通 Go 用例按包精确分组，所有真实物理顶层用例及旧 schema 迁移用例逐例执行，十八个浏览器场景也逐例运行。每组或单例使用同一个本轮元 MySQL 中的独立 schema；测试进程和在途命令确认结束后，先按完整安装归属清理业务资源，再撤销授权并删除 schema，避免前例队列被下一例继续执行。

默认入口持有跨进程排他锁并固定本机 Docker 身份，最多同时三个 MySQL（元库与业务 A、B），不清理外部实例。Go 命令保留 `-count=1 -p=1 -race -tags=integration -coverpkg=./...`，每条命令仍限 600 秒；精确清单和实际通过集合必须一致，不使用宽泛前缀跳过用例。

所有本次 atomic 覆盖率先严格验证模式、坐标、语句数与计数后累加，再与本次浏览器和模板报告走既有归并。任一用例、清理或报告校验失败均不产生成功证据。未知/超时保留的资源必须先核实原安装与执行状态，再经现有 Recover/cleanup 路径处理，不能为了重跑强删现场。完整本地 hook 和 Sonar 门槛保持不变。

2026-11-09 的独立真实时序诊断只运行 `TestRealProjectRunDeletedApplicationFailsClosed`：优化前中断轮已完成该例，顶层用例为 140.92 秒、包为 142.747 秒；固定 Unix endpoint 的只读预算优化后，顶层用例为 103.09 秒、包为 104.575 秒（包耗时减少约 26.7%），测试命令墙钟为 105.723 秒。初始化为 11.923 秒，业务及最终清理分别为 1.359、0.892 秒，最终 MySQL 普查为零。该诊断只证明这一用例和本轮清理，不构成整套通过或总耗时证据。

全量串行计划包含十八个浏览器场景、二十个普通 Go 包组和二十个物理/迁移单例。CI `platform` 作业的总预算由二十分钟调整为六十分钟，以容纳完整串行工作及环境准备；六十分钟是整套作业预算，不是已实测的总时长。每条命令的六百秒期限、全部用例、断言、资源上限与本地完整 hook 均不变。此前被安全中断的整轮仍记为未通过，后续必须取得完整门禁的新结果。
