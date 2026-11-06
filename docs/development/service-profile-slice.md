# Go + net/http 工程包与最小模板（Issue #10）

本切片对应 [Issue #10](https://github.com/hailingu/twine-realm/issues/10)，依赖已完成的本地服务仓库能力，与服务端口 / MySQL 依赖声明保持独立能力边界。交付首个可离线读取、核实及导出的业务工程包；固定源码检查与镜像候选另见 [Issue #14 构建切片](service-build-slice.md)；agents 自动取得原文、画布方案绑定与业务部署仍由后续切片验收。

## 版本、来源与文件所有权

首个组合为 `go-net-http-mysql / 1.0.0`：Go 1.27.3、标准库 `net/http`、`database/sql` + `go-sql-driver/mysql v1.10.1`、`golang-migrate v4.20.1`，本机 macOS / Linux 与独立 MySQL 9.7.4 验证。版本沿用当前已验证基线，不以“最新”作为可复现身份，也不将其他版本或语言标为受支持。

权威发布源在 `server/internal/engineering/assets/go-net-http-v1/`：`profile.source.json` 定义兼容组合、规范角色、真实命令与探针；`_template/` 保存完整模板和业务适用规范。`go.mod.template` 仅在导出时恢复为 `go.mod`，避免把嵌套业务模块当作平台包。其余模板 Go 源码保留真实 `.go` 扩展，接受格式 / 静态 / 实际编译测试及同次 Sonar 分析，不借模板扩展名避开告警。

`npm run profile:refresh` 根据完整字节生成 `.twine/profile.json`、`.twine/engineering.lock.json` 和 `release.json`，`npm run profile:check` 拒绝漂移。文档、发布源与模板是第一版受控材料；已发布 ID / 版本不原地改义，升级须新增版本并评审适用与兼容影响，不通过重新计算摘要偷换已接受服务的规范。

通用规范从平台通用工程规范逐项映射，保留来源仓库、commit、条款标识、适用范围与排除项。服务完整快照包含通用规范、Go / HTTP / SQL 最佳实践、验证说明和来源映射。平台的 React、画布、控制面库、Project 状态及具体分支名称不套用给业务项目。上游能力依据包括 [Go HTTP](https://pkg.go.dev/net/http)、[SQL](https://pkg.go.dev/database/sql) 和已锁版本的迁移 / 驱动源码；超时、布局与验证参数属于此工程方案的具体选择，不冒称上游强制值。

## 只读核实与安全导出

在仓库 `server/` 目录，用当前 Go 工具链运行：

```sh
go run ./cmd/service-profile inspect
go run ./cmd/service-profile export --directory /绝对路径/全新服务目录
go run ./cmd/service-profile validate --directory /绝对路径/服务目录
```

`inspect` 返回固定方案与摘要；`export` 只创建明确的新目录，不覆盖已有内容、不初始化 Git 或登记画布，失败时保留部分新目录供核实。服务根 `AGENTS.md` 用相对链接及唯一的 `twine-engineering` 机器块声明完整必需规范、版本和范围；独立本地 Git clone 后仍可核实。

`validate` 只信任二进制内登记的方案：版本 / 摘要、完整文档、AGENTS 引用与适用范围必须匹配，Go 与必需依赖版本也须属于当前组合。读取限于明确根目录、有界普通文件；拒绝跳转、符号链接、命名管道与其他特殊文件。缺失 / 改变 / 不兼容都返回 `ENGINEERING_*` 阻塞码，不返回外部材料内容、不执行文档命令，也不把验证失败当作未匹配而忽略。

`documents-verified` 只证明实际文件可达和版本 / 摘要匹配，不证明 agents 已读、业务实现正确或依赖无漏洞。用户后续业务源码可变，模板来源不替代当前源码的真实检查；规范和兼容组合变化须显式接受新的受控版本。

## 模板实际运行契约

最小只读示例返回服务名和实际持久初始化时间，没有 Todo、账号或 AI。应用通过 `DB_HOST`、`DB_PORT`、`DB_NAME`、`DB_USER`、`DB_PASSWORD_FILE` 注入配置；迁移命令单独使用 `MIGRATION_USER` 和 `MIGRATION_PASSWORD_FILE`。密码只从获准绝对路径、仅所有者可读的普通文件读取，不进源码、环境密码值、命令参数、普通错误、日志或 API。这里的数据库用途账号不属于一期排除的用户账号系统。

`serve` 不执行 DDL。缺省监听 `127.0.0.1:8080`，容器内监听须显式配置；容器镜像、网络、宿主端口与文件注入策略由后续部署方案固定，不把本机进程验证说成容器部署已支持。业务页面不托管为平台控制台同源内容。

| 命令 / 端点 | 当前行为 |
| --- | --- |
| `service migrate` | 专用迁移身份、数据库锁、内嵌 SQL、版本与 SHA-256 内容证据；重复不重置数据 |
| `service migration-status` | 应用只读身份查询 version、dirty 和 integrity |
| `service serve` | 启动常驻进程，以探针判断实际依赖就绪 |
| `GET /livez` | 进程可响应，数据库故障仍可存活 |
| `GET /readyz` | 认证访问业务库，要求当前源码需要的干净版本与完整迁移摘要证据 |
| `GET /api/info` | 返回持久初始化时间，重启保持 |
| SIGTERM / SIGINT | 撤销就绪、停止接受请求，10 秒内排空并报告结果；不删除库 |

MySQL DDL 与随后摘要证据提交不构成完整事务。dirty、摘要改变、缺失证据或中断后的未知结果都会阻止继续迁移 / 就绪；没有自动 force、改历史或常规 down 入口。失败保留现场，人工核实原源码、实际结构与历史后再决定接续，不能用再次运行掩盖异常。

## 检查与边界

完整 `npm run test:platform` 在自有临时 MySQL 中导出原模板，核实文档，执行 Go 版本、gofmt、模块完整性、vet、race、实际构建、独立用途账号与 HTTP / 进程检查，并把本次模板测试和真实子进程覆盖率归回模板源文件。新旧平台检查继续执行；提交前仍由 `npm run precommit` 执行根 / web 审计、零告警和真实 Sonar。检查失败和工具错误保持非零，不沿用历史报告。

模板构建环境只含工具 / 缓存所需位置；本次临时管理配置仅交给已核实的内置模板集成测试建立自有随机库 / 账号，常驻服务和迁移子进程各自只拿用途身份。此验证入口不能用来运行任意用户仓库并向它发放管理权限。测试清理仅处理已确认由本次创建的数据库、账号、进程和容器。

模块校验不是 Go 漏洞数据库审计；当前本包没有交付该扫描器，不能报告为已通过。架构、完整安全、业务写入幂等、权限语义与例外仍须适用人工审阅；模板验证本身不产生生产运维授权。真实通过范围以本次日志与当前 head 的 PR CI 为准。
