# 本地项目与源码版本切片（Issue #1 / #2）

本页记录实现边界；运行方式见[根 README](../../README.md)，验收事实以本次检查输出为准。[Issue #1](https://github.com/hailingu/twine-realm/issues/1) 与 [Issue #2](https://github.com/hailingu/twine-realm/issues/2) 对应现有 [Project #4](https://github.com/users/hailingu/projects/4)。不自动改变远端状态。

[Issue #6 标准组件目录与配置草稿](component-catalog-slice.md) 另行记录增量完整画布、MySQL 定义、混合布局和专用回执。下文原 v1 项目 / 服务及源码契约继续保持原义；500 容量现由服务与中间件共同使用。

[Issue #12 MySQL 运行](mysql-runtime-slice.md) 另行记录显式执行器与文件秘密、持久运行目标 / 步骤 / 事件、认证事实和数据保留恢复；不改变本页原回执含义。

## 当前范围

Go 标准 HTTP + `database/sql`，MySQL 9.7.4 LTS 平台元数据库，golang-migrate 版本迁移；React 19 / TypeScript / React Flow 12 投影。用户另已确认首个业务工程组合为 Go + 标准库 `net/http`；其模板待后续切片。不创建或改写导入仓库。

`server/internal/project` 拥有身份、输入和用例规则。它消费的 `Writer` 是**用例明确选择的原子命令端口**：每次 Create / Register / SaveLayout 明确要求聚合与回执共用一个 MySQL 事务；并非行级仓储方法隐式扩展事务。Git 检查在事务前，只读、无数据库锁；没有跨 Git / 元数据库的大事务。

`gitworkspace` 解析真实目录，只接受配置的允许根内工作区及 Git 元数据（含 linked worktree 的 common dir、对象库与 alternates）。只执行固定参数的只读 Git 命令，不继承外部 `GIT_*` 或全局 / 系统 Git 配置，关闭可选锁、fsmonitor 和替换对象。登记快照、明确固定版本与按需观察分别存储 / 返回，不自动跟踪、接管或纠正仓库。规范路径是本机定位与去重键，不是跨环境实体身份；ID 为独立 UUID。

## 机器契约

[JSON Schema v1](../../server/contract/v1/schema.json) 定义请求、响应、字段界限和空值；[OpenAPI](../../server/contract/v1/openapi.json) 引用相同定义。Go 入口以该 schema 校验，前端使用 Ajv 在构建时生成的独立校验器验证未知响应，浏览器不动态编译代码，兼容严格 CSP；`npm run contract --prefix web` 生成 TS 类型，`contract:check` 拒绝漂移。v1 只承诺本切片的服务仓库 / 布局，不把完整未来组件、端口、中间件 schema 或任意自定义服务接口压成一个通用 schema。

| 接口 | 行为 |
| --- | --- |
| `GET /api/v1/projects` | 项目列表 |
| `POST /api/v1/projects` | `{mutationId,name}` 创建项目，提交后返回回执 |
| `GET /api/v1/projects/{id}` | 读取一致项目快照 |
| `POST /api/v1/projects/{id}/services` | `{mutationId,baseRevision,repositoryPath,displayName?,position}` 登记仓库 |
| `PUT /api/v1/projects/{id}/layout` | `{mutationId,baseRevision,positions}` 保存完整布局集合 |
| `GET /api/v1/mutations/{id}` | 查原请求已提交回执；404 表示当前尚无已提交结果，不证明在途请求永不成功 |
| `GET /api/v1/projects/{id}/services/{serviceId}/source` | 已保存 `SourceVersion` 与独立 `GitObservation`；仓库观察失败仍返回已保存记录 |
| `PUT /api/v1/projects/{id}/services/{serviceId}/source` | `{mutationId,baseRevision,commit}` 明确固定完整 commit，返回专用 `SourceReceipt` |
| `GET /api/v1/source-mutations/{id}` | 核实源码固定原请求，不改变旧 `Receipt` 的发布形状 |
| `GET /healthz` | 初始化 / 迁移成功后才开放的最小就绪信息 |

项目 revision 是十进制字符串，避免 JS 精度损失；节点数最多 500，名称 1–80 字符，坐标为有限数且绝对值 ≤1,000,000，请求体 ≤128 KiB。服务节点保存服务 ID、仓库 ID、规范路径、显示名称、登记时 commit（空仓库 null）和坐标，不保存 React Flow 对象。

Issue #2 以增量端点扩展 v1，不给原 `Project`、`Repository`、`Receipt` 追加字段或枚举值。`SourceVersion` 绑定项目 / 服务 / 仓库 ID、规范路径、项目 revision 和可空 `pinnedCommit`；完整 SHA 为 40 或 64 个小写十六进制字符。迁移 `000002` 为原服务增加可空列，旧项目和回执保持原形状，旧数据不会自动把登记 HEAD 当成已固定源码。Git 观察含时间、当前 HEAD、分支（游离时 null）、修改状态（无法安全核实时 null）与固定对象当前可用性。

固定前验证对象类型确为 commit；tag、tree、blob、缩写、仓库外对象或缺失对象均拒绝。工作区包含过滤器或子模块时不执行脚本，修改状态明确为未知；普通仓库只读观察有 4 秒与 512 KiB 输出上限。原键回放先于 Git 访问；新意图验证后，在同一平台数据库事务保存固定值、推进项目 revision 并保存专用回执。对象随后被移除只影响可用性观察，不抹除记录，也不宣称已构建 / 部署。

## 提交与恢复

1. 每个新意图生成 mutation UUID。请求类型、项目和规范化载荷共同计算摘要，数据库唯一键约束该身份。
2. 重放先读持久回执；没有时只读检查 Git，再进入显式原子命令。事务内占用请求键、锁定项目 revision、写聚合并保存完整回执，全部成功才返回。
3. 同键同内容返回原回执；同键异内容 409。并发同键最多提交一次；同一项目 / 仓库关联唯一。布局必须恰好覆盖该项目所有服务。
4. revision 冲突 409，未写入任何部分；前端保留位置，用户显式载入最新快照并重放自己的位置。新增节点保持最新服务端位置。
5. 提交或网络结果未知时前端锁定原请求与草稿，查回执或重试原键，不换键复制操作。404 后仍保留原请求。已知拒绝允许纠正输入后提出新意图。
6. 确认快照、位置草稿与在途请求分离。发送中 / 未知时禁止改变草稿或切项目；卸载取消会话，旧结果不能写入下一项目。已确认数据可跨浏览器上下文与平台进程重启恢复。未提交草稿只在当前页面内存；强制离开有丢失边界。

暂不自动过期回执，不实施历史清理或数据库降级。当前项目列表读取全部本地项目；大规模分页、仓库重定位、删除、schema 升级兼容和后台事件流属于后续切片。

## 一期本机安全

按用户决定不开发 / 接入账号、登录、首次配对、会话、RBAC 或 GitHub 凭据。可组合的 HTTP 边界保留未来认证接入位置，不引入空权限引擎。

监听和平台 MySQL 只允许明确回环 IP；校验确切 Host，写接口要求允许的 Origin、JSON 与非秘密 `X-Twine-Request: 1`，拒绝跨站 Fetch 元数据，禁用全开放 CORS。静态目录仅构建产物；CSP、no-store、无第三方字体或脚本；业务源码不提供给浏览器。路径与输入在后端校验，错误不回显原始 Git / 驱动信息，秘密只经环境注入。

这是单个可信 OS 用户的本地工具。它不防御同用户恶意本机进程、已入侵的控制台源或本机目录并发替换，不宣称 loopback 等同账号认证。远程监听、多用户及不可信同源内容不在支持范围。

平台应用数据库用户仅获平台数据库范围内权限以运行迁移；应用与迁移当前共用一个本机注入身份，管理 root 仅供 Compose 初始化，不传给平台。未来分离迁移身份与长期秘密库另行实现；不会因此接入用户账号系统。

## 验证矩阵

| 状态转换 / 不变量 | 真实证据入口 |
| --- | --- |
| 创建、登记、拖动、保存、刷新 / 重开 / 进程重启 | `web/e2e/projects.spec.ts`，真实 Chromium + 平台 + MySQL |
| 普通 / 子目录 / 链接 / 空仓库、不可读、非 Git、越界、外置元数据 | `server/internal/gitworkspace/inspect_test.go` + 浏览器错误路径 |
| 不改源码 / AGENTS / Git 索引 / 配置 / 分支 | 真实仓库导入前后内容 / 权限摘要 + 浏览器夹具摘要 |
| 重复、同名不同路径、同键异意图、同键并发 / 版本竞争、失败原子性、回执重开 | `server/internal/httpapi/integration_test.go`，真实 MySQL |
| 提交成功响应丢失、未送达、原键核实 / 重试、冲突重放、草稿保留 | Playwright 故障注入 + Hook / 组件测试 |
| Host / Origin / JSON / 标记 / 额外字段 | HTTP + 发布 schema 集成检查 |
| 固定与 HEAD / 工作区互不覆盖、失联保留、源码原键恢复 | `server/internal/httpapi/source_integration_test.go`、`web/e2e/source.spec.ts` |
| v1 数据库升级后保留实体、坐标、版本与原始回执 | `server/internal/httpapi/migration_integration_test.go`，门禁独占临时 MySQL |
| 对象类型、完整 SHA、过滤器不执行、对象库边界、只读保证 | `server/internal/gitworkspace/source_test.go` |

各检查的实际运行结果在交付报告列明；用例存在不等于本次已通过。账号、AI、业务服务部署、编辑器和 SSE 不在此矩阵中冒称完成；项目 MySQL 运行的独立证据见运行切片。
