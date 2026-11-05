# 数字孪生软件定义平台 Agent 指南

本仓库建设**数字孪生的软件定义平台**：用户在网页画布中以类 UML 图的方式拖入标准中间件（MySQL、RabbitMQ 等）与通用服务组件，由平台 AI 完成数据模型设计、开发、测试与部署；一个画布对应一个真实的 docker-compose / k8s 项目空间，服务以零感知方式替换升级，真实运行状态与配置实时回显到画布，构成业务系统的数字孪生。

仓库目标结构：Go 平台服务（`server/`）、TypeScript 前端画布（`web/`）、部署与项目空间模板（`deploy/`）、设计文档（`docs/platform-design/`）、开发规范（`docs/development/`）、本地历史 / 离线草稿（`issues/`）与仓库工具脚本（`scripts/`，含根 `package.json` 与 Sonar 项目配置）。**已建立 Issue #1 本地项目、仓库登记与布局持久化，以及 Issue #2 明确固定 commit、只读 Git 观察与源码回执恢复，Issue #6 版本化 MySQL 配置草稿与统一布局。用户已选首个业务组合为 Go + 标准库 net/http，Issue #10 提供该组合的离线工程包、核实 / 导出 CLI 与最小模板；AI 自动加载、中间件运行与业务部署等仍未实现。**

## 分支与历史边界

- **`develop` 是平台集成基线**，不直接开发或提交；每项开发使用独立任务分支和 worktree，经检查、推送与 PR 合入。
- **`main` 受保护**，承载历史演示（能力实验室 v4.0、模拟器 v2/v3）：它们是背景参考，**不构成平台的约束、依赖或可复用代码来源**；不主动合并、不引用其实现、不删除其内容。
- 平台按[平台设计概览](docs/platform-design/README.md)全新实现，不为历史演示的数据、接口或交互承担兼容。

## 技术栈与版本基线

- 服务端 **Go**（基线 1.27.x），前端 **TypeScript + React + React Flow**（Node 26 LTS），平台元数据 **MySQL**（最新 LTS 系列），项目空间编排本机以 **docker-compose** 为一等公民、接口按 k8s 可移植设计。
- 仓库工具与质量门禁由根 `package.json` 维护（Node 26.13.0，`.nvmrc` 锁定）：issue 元数据校验、Sonar 门禁（本地 SonarQube 的 `twine-realm` 项目，地址与凭据只经环境注入）与完整门禁编排；命令与边界见[质量检查与证据](docs/development/quality-gates.md)。
- 版本尽可能新但 LTS 优先，具体锁定位置、本机现状差距与升级政策见[运行时与工具链](docs/development/runtime-toolchain.md)。

本仓库的文档、代码注释与提交信息均使用中文；本指南亦以中文书写。

本指南适用于仓库根及全部子路径，除非更近的 `AGENTS.md` 增加了更严格的路径级规则。外部系统、开发者与用户指令保持其原有优先级。规则冲突或权限不明时，先停下询问仓库所有者，再做变更。

「必须」和「禁止」是强制要求。「应当」标识预期默认，偏离时须说明理由。「可以」为可选项。示例仅供说明，永不覆盖规则。

## 不可协商的底线

### 核心规则

- **提交前零告警**：前后端编译、类型、lint、构建及适用静态分析代码问题必须清零，根和 web 依赖审计不得留任何 low 及以上漏洞。执行 `npm run precommit`；安装 `.githooks/pre-commit` 的命令为 `npm run hooks:install`。禁止忽略 / 降级告警、静默禁用检查或使用 `--no-verify` 绕过。源码与配置须与待提交索引一致，具体机器检查和负例见[零告警提交门禁](docs/development/warning-free-commits.md)。

- 保护用户与其他任务的改动。不得覆盖、重排格式、暂存或清理无关工作。
- 永远不得硬编码、暴露或记录密钥、token、密码、私钥及敏感用户数据；中间件连接凭据只经配置或环境变量注入。
- 用满足已批准范围的最窄变更。未经明确授权，不得新增依赖、变更契约或重构相邻代码。
- 实现前必须先查阅受影响代码、适用规范与既有内部能力，优先复用合适的组件、契约、schema 或脚本。
- 新增公共文件、包、模块、类型、组件、函数与方法，必须在实现体之前有表达意图的中文文档注释。
- 失败必须显式处理并如实上报：Go 返回并包装 `error`，TS 处理每个 Promise 的拒绝；统一诊断入口建立前使用标准日志能力，不得静默吞错。禁止提交包含 `console.log` 的代码。
- 禁止提交未使用的导入、变量、类型与依赖。
- 对每个受影响路径运行非交互检查，并如实报告命令、结果与未运行项；当前未配置自动检查时做结构化评审并如实说明，不得虚报或臆造命令。
- 维护的生产源码文件不得超过 **800 物理行**，测试文件不超过 **1800 物理行**；超过 600 / 1000 行时先做职责拆分评审。函数、方法、闭包、组件保持在 **80 代码行**以内（注释不计，空行计入），超过 60 行触发评审；评审发现的超限单元在本次变更内拆分。
- 遵循[通用工程规范](docs/development/software-engineering-standard.md)与对应语言规范（[Go](docs/development/go-standard.md)、[TypeScript](docs/development/typescript-standard.md)）。禁止循环依赖、含糊的公共杂物模块，以及为单一实现无依据地增加抽象。
- 平台核心不变量（孪生一致性、零感知替换、声明原子性、凭据隔离、AI 产物不豁免验证、配置生效语义显式）在任何实现中不得削弱，定义见[平台设计概览](docs/platform-design/README.md)。

## 变更分类

编辑前先对请求的工作分类：

| 类别 | 包含 |
| --- | --- |
| 只读 | 调查、检索、评审、问答——不产生写入 |
| 编辑性文档 | 不含治理、契约、安全、部署或流程含义的 Markdown |
| 规范性 / 治理文档 | 定义治理、契约、安全、部署或流程行为的 Markdown 或指令文件 |
| 源码 | 应用、库或测试代码 |
| 配置 | 构建、CI、部署、环境或基础设施配置 |
| 运维 | 产生留存或分发产物、或变更运行中/共享/外部系统的构建、发布、部署、回滚动作 |

只读工作与编辑性文档无需任务本身的额外授权。规范性/治理文档、源码与配置工作遵循下文「执行工作流」与「版本控制安全」。**运维工作在执行前还需仓库所有者的明确授权**；运行测试、静态检查或本地验证编译不属于运维，前提是其一次性产物在任务完成前删除。

本项目中的典型运维操作：`docker compose up/down/build`（含本机项目空间）、`docker build` / `docker run` / `docker push`、平台自身的构建与部署、对任何远端或生产数据的操作。

**注意区分：为平台实现编排与部署能力（源码工作）不等于授权对运行环境执行编排（运维工作）。**

可复现缺陷修复必须先复现（失败的测试或日志证据），再实施修复；源码功能应当伴随对应测试。纯文档变更不要求制造失败测试，改做结构化评审。

## 关键状态与不变量矩阵

源码功能或可复现缺陷修复在修改生产代码前，须记录简明矩阵（前置状态、动作顺序、预期可观察结果、跨转换不变量与所有者、对应测试或验证缺口），放在任务计划、设计文档或变更说明即可。平台特有的验证方向与示例矩阵见[开发工作流](docs/development/workflow.md)。

## 执行工作流

1. 阅读本指南及适用于请求范围的全部更具体指令。
2. 提议或实施变更前，检查相关代码、文档、构建文件与当前工作区。
3. 对变更分类。只读工作无需任务分支；开发变更必须使用独立任务分支和 worktree，遵循版本控制政策并保留已有工作。
4. 检索既有内部能力，选择满足请求的最小连贯变更。
5. 源码功能或可复现缺陷先记录状态与不变量矩阵；只实现已批准的范围，保留工作区中的无关改动。
6. 先运行最窄相关的非交互检查，再运行「范围路由」各行要求的更广检查。任务完成前删除一次性验证产物并报告结果。
7. 更新因本次变更而过时的每一份文档、索引或交叉引用；实现与设计不同步时，必须更新[平台设计概览](docs/platform-design/README.md)并区分已实现、待实现与已知边界。
8. 报告变更文件、验证结果、已知限制与稳定证据。必要工作未完成时不得声称完成。

## 范围路由

对每个受影响的路径或操作，按下列顺序评估，命中首行即停；多个路径受影响时各自独立匹配。

| 范围 | 先读 | 工作目录 | 验证命令 | 备注 |
| --- | --- | --- | --- | --- |
| `server/**` | 本指南、[Go 规范](docs/development/go-standard.md)、[本地切片](docs/development/local-project-slice.md) | `server/` | `go build ./...`、`go vet -tags=integration ./...`、`go test ./...`；根 `npm run test:platform` 含真实 MySQL / Git 与 `-race` | 根 `gofmt -l server` 零输出 |
| `web/**` | 本指南、[TS 规范](docs/development/typescript-standard.md)、[本地切片](docs/development/local-project-slice.md) | `web/` | `npm run contract:check`、`npm run typecheck`、`npm run lint`、`npm run test`、`npm run build`；根 `npm run test:platform` 含浏览器 | 不以 mock 代替持久化验收 |
| `deploy/**` | 本指南、[运行说明](README.md) | 根 | `docker compose --env-file .env -f deploy/compose.yaml config --quiet` | 注入本机或临时配置，不回显秘密 |
| `AGENTS.md` 与伴随 agent 入口文件 | 本指南「不可协商的底线」「执行工作流」「版本控制安全」各节 | 仓库根 | 未配置——做结构化评审并报告无自动检查 | 纯文档变更不要求红-绿-重构 |
| `README.md`、`docs/**` | 本指南与 [docs/README.md](docs/README.md)（文档索引） | 仓库根 | 未配置——做结构化评审并报告无自动检查 | 设计只在 `docs/platform-design/` 维护；目标与已实现必须分开表述 |
| `issues/**` | 本指南、[问题记录规范](docs/development/issue-standard.md)、[issues/README.md](issues/README.md) | 仓库根 | `npm run issue:status`；验收与证据仍做结构化评审 | 自动校验文件头字段、枚举、编号唯一与日期一致，并报告状态分布及未关闭记录（未关闭不构成失败，元数据违规才非零退出）；`--json` 输出结构化摘要。只校验本地历史 / 草稿，不代表 GitHub Issues 或 Project 状态；远端任务与关联遵循[协作规范](docs/development/project-tracking.md)，本命令不读取或同步远端 |
| `scripts/**`、`sonar-project.properties`、根 `package.json`、`package-lock.json`、`.nvmrc` | 本指南、[通用工程规范](docs/development/software-engineering-standard.md)、[质量检查与证据](docs/development/quality-gates.md) | 仓库根 | `npm run test:quality`；实现变更后执行 `npm run quality:gate` | Node 行为测试覆盖 Sonar 门禁、完整门禁编排、零告警检查、Git 提交入口与 issue 状态检查；完整门禁含本次脚本覆盖率导入与真实 Sonar Quality Gate。Sonar 服务地址及凭据只从环境读取（`SONAR_HOST_URL` 与 `SONAR_TOKEN` / `TWINE_REALM_SONAR_TOKEN`），不得写入仓库 |
| `.github/**` | 本指南与[质量检查与证据](docs/development/quality-gates.md) | 仓库根 | 未配置本地检查——做结构化评审（YAML 语法与 action 钉版核对）；真实行为证据是 GitHub 上 ci 工作流运行 | GitHub Actions CI：`ci.yml` 面向 develop / main 的 PR/push 执行 tooling 检查（`npm audit --audit-level=low`、`npm run test:quality`、`npm run issue:status`），Node 版本取自 `.nvmrc`、action 按提交 SHA 钉版；Sonar 保留在本机门禁，CI 不削弱它。另有 platform job 执行 Go / TS / 真实数据库 / Chromium 和 Compose 配置检查；CI 结果须按当前提交独立读取。`dependabot.yml` 按 npm、gomod 与 github-actions 周粒度开依赖 PR 到 develop，只开 PR 不自动合并。凭据与日志不引入密钥 |
| `.gitignore` 及其他根配置 | 本指南与[运行时与工具链](docs/development/runtime-toolchain.md) | 仓库根 | 未配置——做结构化评审 | 不提交密钥与本地环境文件 |

新受维护模块须同次接入配置、路由、完整门禁和文档；未接入前不得声称受自动保护。

无路由行命中时，为每个受影响路径发现并运行最窄相关的非交互检查；不存在自动检查时，做结构化评审、报告未配置自动检查、记录实际检查了什么，不得臆造命令。

## 安全

- 在信任边界校验外部输入：后端 API 入参、AI 流水线产物与阶段间传递、docker / DB 适配器返回值、前端收到的服务端事件。
- 对用户、服务、凭证与基础设施应用最小权限。数据库连接串、AI 提供商密钥、镜像仓库凭据只经环境变量或配置注入，禁止入库、禁止进入前端代码与 AI 对话记录。
- 认证、授权与密码学采用成熟框架与库，不自造算法。
- 避免不安全的命令构造：compose 文件与镜像名经白名单校验，不拼接未校验的外部输入到 shell 命令；优先使用各客户端 SDK 而非命令行拼接。
- AI 生成的服务代码与模型变更必须通过与人工代码相同的测试与部署门禁，在受控目录构建；注意 Vite 的 `VITE_` 前缀变量会被打入前端产物，敏感值不得进入。

## 版本控制安全

- 切换分支、拉取、暂存或提交前检查工作区。
- 只暂存明确授权的路径，提交前检查已暂存 diff。
- 未经项目要求的授权，不执行破坏性或对外发布操作。

### 分支、提交与推送政策

- **保护分支**：`main` 禁止直接提交与推送，其历史演示内容保持原样。
- **集成基线**：`develop` 只通过 PR 接收开发变更；禁止直接开发、提交或推送到 `develop`。
- **独立开发**：核对远端与本地基线后，从 `develop` 创建 `feature/`、`fix/`、`docs/` 或 `chore/` 任务分支，并在独立 worktree 中开发；一个任务复用自己的分支 / worktree，不覆盖其他任务或用户修改。
- **交付顺序**：完整门禁与真实 pre-commit 通过 → 推送任务分支 → 创建目标为 `develop` 的 PR → 核对当前 head 的 CI、验收与审阅 → 合并 → 同步本地 `develop` → 安全清理本次 origin / 本地分支与 worktree。合并、Issue 关闭与项目状态分别读取核实，不能提前 Done。
- **安全同步与清理**：仅快进更新本地 `develop`；未提交内容先保留并核实归属，冲突或远端分歧时停止该部分，禁止强推、hard reset、强删分支或强制移除 worktree。只有确认 PR 已合入 `develop`、本地与远端一致、任务提交均已合并且没有未保存数据，才删除本次已合并分支与 worktree；保留 `main`、`develop`、未合并分支、其他任务与用户数据。详细步骤见[开发工作流](docs/development/workflow.md#版本控制与交付)。
- **授权与发布**：提交、推送、PR、合并和清理在用户已授权范围内接续执行，不重复索要同一许可；本规范不额外授权无关的远端写入。向 `main` 的合并属发布动作，按运维级授权处理。
- **提交信息**：使用中文「类型：描述」（如 `功能：画布期望态存储`、`文档：补充 Go 规范`），Conventional Commits 风格（`feat:`、`fix:` 等）亦可接受。

## 验证与完成证据

- 可复现缺陷修复先复现：先给出失败测试或日志证据，再修复并观察检查通过。纯文档变更做结构化评审。
- 先跑聚焦检查，共享行为受影响时再跑更广检查；使用「范围路由」中的确切命令与工作目录。
- 跳过、受阻或失败的检查必须连同完整原因一并报告。
- 优先使用稳定证据：提交号、符号、标题、命令结果摘要；可变行号仅作辅助证据。
- 完成的判定标准：请求的行为、必需的文档、必需的检查与必需的证据全部就绪——而非仅仅完成了一次实现尝试。

## 评审与问题记录

- 每项意见先核对可复现触发、契约依据、影响和授权范围，再记录已修复、无需变更、延期或等待决策；不得凭假想极值扩大重构范围。
- 实质性回复包含「处置、理由与变更、验证、后续」四项；本地修改、已提交、已推送必须分清。未经授权不向外部 issue、PR 或评审线程发消息。
- 项目具体工作以 [GitHub Issues](https://github.com/hailingu/twine-realm/issues) 为权威记录，[twine-realm Project #4](https://github.com/users/hailingu/projects/4) 管规划、排序和阶段；先查重并关联原 Issue，按[Projects 与 Issues 协作规范](docs/development/project-tracking.md)核对状态与关闭结果，不假定自动同步。根 [issues/](issues/README.md) 仅保留历史、离线草稿及明确链接的证据快照，不为同一工作另建独立状态。记录质量遵循[问题记录规范](docs/development/issue-standard.md)；未完成或仅记录边界的工作不得描述为已修复。

## 事实来源

| 关注点 | 权威路径或发现方式 |
| --- | --- |
| 平台愿景、核心概念、关键不变量、目标架构 | `docs/platform-design/README.md` |
| 文档索引与分类 | `docs/README.md` |
| 开发规范索引、工作流、状态矩阵、目标目录布局 | `docs/development/README.md`、`docs/development/workflow.md` |
| 通用工程规范 | `docs/development/software-engineering-standard.md` |
| Go / TypeScript 语言规范 | `docs/development/go-standard.md`、`docs/development/typescript-standard.md` |
| 版本基线、锁定位置与升级政策 | `docs/development/runtime-toolchain.md` |
| 检查现状、计划门禁与证据口径 | `docs/development/quality-gates.md` |
| 工作、规划与处置 | GitHub Issues（范围 / 验收 / 关闭事实）、Project #4（排期 / 阶段）、`docs/development/project-tracking.md`、`docs/development/issue-standard.md`；`issues/` 仅历史 / 离线草稿 |
| 依赖与工具版本 | 根 `package.json`、`package-lock.json`、`.nvmrc`（仓库工具）；`server/go.mod`、`web/package.json`、`deploy/` compose 定义（建立后） |
| `main` 分支历史演示（仅背景） | `main` 分支 Git 历史，不承担兼容义务 |
| 版本化源码、配置与实现证据 | Git 提交与合并请求 |

## 本地开发环境

启动、配置和停止见[根 README](README.md)，实现边界见[本地切片](docs/development/local-project-slice.md)。一期不接入账号、登录、配对或 RBAC；只支持单可信 OS 用户的本机工具。未来身份层可在 HTTP 边界外组合。

根命令 `npm run test:quality`、`npm run issue:status`、`npm run test:platform`（Go / TS / 临时 MySQL / Chromium / 重启）、`npm run quality:gate`（完整检查与本次 Sonar）。秘密只经环境注入，测试只清理自有资源。

## 附录：开发者守则

1. 以认真查阅为荣，以暗猜接口为耻
2. 以寻求确认为荣，以模糊执行为耻
3. 以声明假设为荣，以隐瞒假设为耻
4. 以先读代码为荣，以跳过阅读为耻
5. 以沿用范式为荣，以无视范式为耻
6. 以核对源码为荣，以臆造知识为耻
7. 以最小实现为荣，以过度工程为耻
8. 以直写功能为荣，以过早抽象为耻
9. 以说明取舍为荣，以忽略权衡为耻
10. 以择优推荐为荣，以冗列方案为耻
11. 以最小变更为荣，以扩大差异为耻
12. 以专注所需为荣，以旁改无关为耻
13. 以读全报错为荣，以盲修错误为耻
14. 以先行复现为荣，以未先复现为耻
15. 以单改单测为荣，以多改混测为耻
16. 以主动测试为荣，以跳过验证为耻
17. 以追查根因为荣，以绕过根因为耻
18. 以审慎加包为荣，以轻添依赖为耻
19. 以说明因果为荣，以只贴代码为耻
20. 以主动示警为荣，以隐去风险为耻
21. 以标明决策为荣，以暗作决策为耻
22. 以先求认可为荣，以失控重构为耻
23. 以函数短小为荣，以冗长费神为耻
24. 以渐进还债为荣，以放任腐化为耻
25. 以风格统一为荣，以随意编码为耻
26. 以零感知替换为荣，以裸停服务为耻
27. 以门禁验 AI 为荣，以豁免检查为耻
28. 以声明期望态为荣，以画布外漂移为耻
29. 以显式管凭据为荣，以硬编码密钥为耻
30. 以第一性原理深析问题本质为荣，以停留表象为耻
