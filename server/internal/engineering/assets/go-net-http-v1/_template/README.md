# Go + net/http 最小服务

这是 `go-net-http-mysql / 1.0.0` 的可替换初始化示例，不含 Todo、账号、AI 或平台控制能力。首先读取 [AGENTS](AGENTS.md) 及完整 [工程规范](docs/engineering/general.md)。Go 1.27.3 与实际依赖在 go.mod / go.sum 锁定；说明见 [验证文档](docs/engineering/verification.md)。

当前服务只返回持久初始化时间。根据业务需要修改服务与迁移，重新执行真实检查；模板来源不能证明修改后的实现符合要求。导出不会建立 Git 仓库或自动登记画布，不覆盖已有目录。

应用通过 `DB_HOST`、`DB_PORT`、`DB_NAME`、`DB_USER`、`DB_PASSWORD_FILE` 注入数据库信息。迁移命令单独使用 `MIGRATION_USER`、`MIGRATION_PASSWORD_FILE`。密码文件为绝对路径、仅所有者可读的普通文件，不写进仓库、命令参数、日志或 API。

```sh
go build -trimpath -o bin/service ./cmd/service
bin/service migrate
bin/service migration-status
bin/service serve
```

执行前按验证文档配置独立数据库、用途账号与密码文件。缺省监听 `127.0.0.1:8080`；容器内 `HTTP_ADDRESS=0.0.0.0:8080` 须显式注入，宿主发布与网络隔离由后续部署方案负责。不要把业务页面放到平台控制台同源路径。

`/livez` 只报告进程可响应；`/readyz` 要求数据库、干净迁移版本及摘要证据。`/api/info` 返回实际持久初始化时间。常驻进程不执行 DDL。SIGTERM 先撤销就绪、停止接受请求并在 10 秒内排空，不删除数据库。

迁移失败或 dirty 时保留现场。DDL 完成与摘要证据提交之间若中断，可能出现干净版本但证据缺失；再次运行也会阻塞，须人工核实实际表结构 / 原源码后再恢复。没有自动 force、清 dirty、重置版本或常规 down 命令。
