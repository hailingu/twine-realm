# Go + net/http 最佳实践 1.0.0

适用组合为 Go 1.27.3、标准库 net/http、本机 macOS / Linux 进程、MySQL 9.7.4。SQL 驱动固定 go-sql-driver/mysql v1.10.1，业务迁移固定 golang-migrate v4.20.1；完整解析由 go.mod / go.sum 和实际检查确认。没有额外 Web 框架，也不宣称其他版本已验证。

| 规则 | 工程要求及本模板行为 |
| --- | --- |
| GO-01 布局 | cmd/service 只装配；internal/config 解析外部配置，app 拥有 HTTP / 排空状态，database 拥有连接及只读访问，schema 拥有源码迁移摘要，migrations 只供明确迁移命令使用 |
| GO-02 依赖 | go.mod / go.sum 固定实际模块，构建使用 -mod=readonly 和 GOTOOLCHAIN=local，不隐式获取新工具链；不支持未接受的 replace / exclude 覆盖。新增依赖须按项目流程审阅，执行实际模块校验和适用安全检查 |
| GO-03 错误 | 显式返回 error，以 errors.Is / As 判别；在能决定恢复的层处理。数据库原始异常、DSN、文件内容不可进入响应或普通日志；客户端断开不重试写入 |
| GO-04 取消 | context 是 I/O 首参；goroutine 有退出路径。HTTP 每次只读依赖访问最多 2 秒，服务设置头、读、写、空闲与头大小边界；连接池、连接寿命与驱动 I/O 有界 |
| GO-05 HTTP | 标准 ServeMux 注册明确方法 / 路径，输入限制后再解析；不提供工作区静态文件、密码文件、pprof 或任意执行入口。缓存与 MIME / CSP 头明确，业务页面与平台控制台隔离 |
| GO-06 配置 | 默认回环监听，其他地址需显式配置；密码只读 DB_PASSWORD_FILE。迁移只读 MIGRATION_PASSWORD_FILE，身份分离，不使用 root。非敏感位置由 mysql.Config 结构化编码，不拼接 DSN |
| GO-07 SQL | 常驻应用只用该业务库 DML 权限，本示例仅 SELECT；迁移账号仅有该库必要 DDL / DML，无全局 / 跨库管理。查询有 context，遍历结束核实 Rows.Err，连接 / 事务有关闭或回滚 |
| GO-08 迁移 | SQL 内嵌于当前 commit，实际工具锁及数据库摘要锁串行。已应用版本核对完整 SQL SHA-256，dirty、旧内容改变或证据缺失阻塞，不自动 Force / 改历史。DDL 不按完整跨步骤回滚设计 |
| GO-09 探针 | livez 只验证进程；readyz 认证访问业务库并核实当前源码要求的干净版本和全部应用摘要，未迁移 / dirty / 未知均不就绪。数据库断开不据此循环重启进程 |
| GO-10 停机 | SIGTERM / SIGINT 先撤销就绪，再用 Server.Shutdown 排空，10 秒到界明确失败；不删除库、卷或源码。迁移取消等待已发 SQL 退出后查询历史，不把取消等同回滚 |
| GO-11 测试 | testing + httptest，gofmt、vet、race、行为测试和真实 SQL / 进程验收。不要仅断言 mock 次数，或以类型 / 编译通过代替外部边界验证 |

迁移 SQL 与摘要证据同属业务库，但 MySQL DDL 与随后证据提交并不原子。工具 Up 成功后证据提交前崩溃会留下需人工核实的状态；探针不放行，重试不盲目重跑或重建数据库。迁移账号在完成后不交常驻服务。

此模板的最小初始化身份可换成任意业务。新增写 API 时必须自行定义校验、事务、幂等、超时后核实与权限，不从只读示例推导写入安全。业务账户 / 身份需求独立设计，本包不接入平台账号系统。

来源版本、链接与适用条款见 [sources.json](sources.json)；上述参数是本方案的具体工程选择，不冒称上游通用强制值。
