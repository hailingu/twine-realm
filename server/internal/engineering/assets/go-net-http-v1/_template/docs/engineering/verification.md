# 验证与运行约定 1.0.0

先使用 Go 1.27.3。文档可以完全离线读取；依赖缓存或下载、MySQL、容器镜像和 AI 的离线能力不是本包承诺。首次依赖获取按项目接受的网络权限执行。

```sh
go mod download
go mod verify
gofmt -l .
go vet ./...
go test -race ./...
go build -mod=readonly -trimpath -o bin/service ./cmd/service
```

`gofmt -l .` 须无输出；其余命令须成功且无代码告警。编译 / vet / 格式 / 实际测试之外的架构、完整安全和业务规则仍须人工审阅。Go 漏洞数据库检查未作为本包内置扫描器交付，不把模块校验说成漏洞审计；按实际服务 / 发布风险另运行适用安全检查。

运行前准备独立业务数据库，应用账号仅有该库 DML，迁移账号仅有该库必要 DDL / DML。不得给服务平台元数据库、root、跨库或 GRANT 权限。数据库名使用小写字母开头的 1–48 位字母 / 数字 / 下划线，账号限定 32 位。数据库默认 3306，宿主端口和网络由实际环境控制。

| 环境名 | 用途 |
| --- | --- |
| DB_HOST、DB_PORT、DB_NAME | 非敏感业务库位置，不是完整 DSN |
| DB_USER、DB_PASSWORD_FILE | 常驻应用账号与受限密码文件 |
| MIGRATION_USER、MIGRATION_PASSWORD_FILE | 仅迁移命令使用的独立账号与受限文件 |
| HTTP_ADDRESS | 默认 127.0.0.1:8080；容器须显式配置 0.0.0.0:8080 |
| SERVICE_NAME | JSON 返回的显示名，可选 |

密码文件为绝对路径、普通文件、0600 或更严格，不接受最终路径符号链接、空 / 超长或多行密码；文件目录由部署者隔离。密码不写进环境变量、CLI 参数或本仓库。常驻服务环境只注入应用身份，不注入迁移或管理身份。

先明确执行 `bin/service migrate`，成功后用应用身份执行 `bin/service migration-status`，返回 version、dirty、integrity；随后 `bin/service serve`。没有迁移时 serve 仍可启动以提供 livez，readyz 返回 503。干净版本和摘要均匹配时 readyz 200；GET /api/info 返回 service 与 initializedAt，重启后 initializedAt 不变。任一 dirty / 摘要改变 / 缺失证据都会阻止就绪；没有自动 force 或清库入口。

模板的 `integration` 测试由受信验证器提供自有临时数据库管理凭据和测试二进制，仅用于建立本次随机库 / 用途账号与安全清理。不能把这些测试参数作为构建任意用户代码的授权，也不能把管理凭据传给常驻示例进程。平台完整门禁会真实运行它；单独执行需要明确的隔离数据库和文档化测试环境，缺失时失败，不静默跳过。

检查应覆盖：规范删除 / 改义 / 不兼容、越界引用、已有目录不覆盖；配置与密码文件非法；未迁移、再次迁移、dirty、摘要异常与数据库失联；应用 DDL / 跨库失败；真实 HTTP、SIGTERM 和重启数据保持。执行结果绑定当前源码及规范摘要，失败 / 未运行不得记为通过。

包核实工具只能证明文件实际可达、版本 / 摘要匹配。当前没有 agents 自动发现、原文注入或读取记录系统，不以 CLI 检查成功声称 agents 已加载。
