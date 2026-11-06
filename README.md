# twine-realm

本地服务画布。当前实现 [Issue #1](https://github.com/hailingu/twine-realm/issues/1) 的本机项目、只读仓库登记与布局恢复，[Issue #2](https://github.com/hailingu/twine-realm/issues/2) 的明确固定 commit 与恢复，以及 [Issue #6](https://github.com/hailingu/twine-realm/issues/6) 的版本化 MySQL 目录、配置草稿和混合节点布局，以及 [Issue #8](https://github.com/hailingu/twine-realm/issues/8) 的服务接口声明、具名端口与 MySQL 依赖连线。 [Issue #12](https://github.com/hailingu/twine-realm/issues/12) 另提供显式本机 MySQL 运行、持久操作与数据保留恢复。

服务节点先保存为草稿。用户已选择首个业务工程组合为 Go + 标准库 `net/http`；[Issue #10](https://github.com/hailingu/twine-realm/issues/10) 提供离线工程包与最小服务模板，[Issue #14](https://github.com/hailingu/twine-realm/issues/14) 提供固定源码构建，[Issue #16](https://github.com/hailingu/twine-realm/issues/16) 提供单服务绑定 MySQL 的显式部署、迁移和业务就绪核实。[Issue #18](https://github.com/hailingu/twine-realm/issues/18) 提供一致状态信封、SSE 与断线草稿恢复。Todo 只是独立验收仓库。不包含平台账号 / 登录 / 配对 / RBAC、AI、GitHub 同步或代码编辑器。项目与仓库关联只保存在本机，导入、构建和部署不修改源码、`AGENTS.md`、索引、分支或 Git 配置。

## 运行

需要 Go 1.27.3、Node 26.13.0（`.nvmrc`）、npm 11、Git、Docker / Compose。使用任务内或系统工具链均可，不自动升级环境。

1. 根目录 `npm ci`，再执行 `npm ci --prefix web`。
2. 复制 `.env.example` 为 `.env`，设置权限 `chmod 600 .env`。分别以 `openssl rand -hex 24` 生成管理与应用密码，填写 `TWINE_DB_ROOT_PASSWORD`、`TWINE_DB_PASSWORD`；填写仓库允许范围，例如 `TWINE_REPOSITORY_ROOTS='["/Users/you/Git"]'`。不要使用文件系统根目录。
3. 执行 `docker compose --env-file .env -f deploy/compose.yaml up -d --wait`，只启动平台自用 MySQL；默认仅绑定 `127.0.0.1:3307`，命名卷保留元数据。
4. 在同一终端载入自己维护的 `.env`：

   ```bash
   set -a
   source .env
   set +a
   export TWINE_DATABASE_DSN="${TWINE_DB_USER}:${TWINE_DB_PASSWORD}@tcp(127.0.0.1:${TWINE_DB_PORT})/${TWINE_DB_NAME}"
   ```

5. 根目录执行 `npm run build --prefix web`、`go -C server build -o ../build/platform ./cmd/platform`、`./build/platform`。打开 `http://127.0.0.1:8080`。平台先完成数据库连接与版本化迁移，再开放 HTTP；端口被占用时明确失败。

输入允许目录内的 Git 工作区绝对路径。符号链接、子目录归一到同一工作区；相同项目不重复登记。同名目录但不同规范路径仍是不同仓库。空仓库允许登记，显示「暂无源提交」。坐标是布局，不代表运行状态。

修改位置后点击「保存布局」。失败时保留草稿；结果未知时点击「核实保存结果」或「重试原请求」，请求身份保持不变。版本冲突时可载入最新快照并保留自己的位置，确认后再次保存。未保存 / 未确认时刷新有浏览器离开保护；草稿未写入浏览器持久存储，强制关闭页面可能丢失未确认的本地输入，已提交数据可重新打开恢复。

点击服务列表中的「源码版本」查看登记快照、已固定 commit 与带时间的当前 HEAD / 分支 / 修改状态。点击「选择当前 HEAD」只填写候选，点击「确认固定 commit」才保存；也可输入仓库内已有完整小写 commit。后续提交、切分支和未提交修改不会自动更新固定值。先保存 / 还原布局再固定源码；请求失败保留候选，结果未知时核实原请求。仓库失联或对象丢失仍显示已保存记录；配置外部过滤器或子模块时修改状态明确为未知。尚未确认的候选只保留在当前页面，刷新不会自动提交它。

点击「＋ MySQL 组件」打开固定版本目录，填写名称和内存上限后明确加入。运行方案可暂不选择，节点显示配置未完整；保存完整配置仍不会自动运行。点击组件列表的「配置」修改并保存候选；服务与 MySQL 的位置使用同一次「保存布局」。定义版本、配置和节点身份可重开 / 重启恢复。

固定源码构建默认禁用。按[构建切片](docs/development/service-build-slice.md)准备固定 Go 镜像、在仓库及运行根之外执行 `build-init`，明确配置 `TWINE_BUILD_ADAPTER=local-go-container-v1` 和 `TWINE_BUILD_DIRECTORY` 后，可在服务源码详情检查并构建已保存的 commit。未知请求保留原键核实 / 重试，成功只产生本地不可变镜像候选；可以明确取消原构建，未自动部署。

项目 MySQL 运行默认禁用。按[运行切片](docs/development/mysql-runtime-slice.md)在仓库范围外初始化全新私有根，明确设置 `TWINE_RUNTIME_ADAPTER=file-secrets-v1` 与 `TWINE_RUNTIME_DIRECTORY` 后启动控制面。该适配器以 0700 / 0600 文件权限保护本机明文秘密，不是加密凭据库；不自动安装工具或拉取镜像。在 MySQL 详情点击「确认运行 MySQL」，才接受独立的持久目标；202 或容器运行不等于认证就绪。每项目固定一个实例、私网与持久卷，不发布数据库宿主端口；关闭页面或平台不会停止它。需要停止时在详情确认「确认停止 MySQL」，保留数据和秘密。配置升级、删除卷与项目聚合停止仍待后续。状态回显见 [SSE 切片](docs/development/project-state-slice.md)：断线保留最后观测及草稿，其他窗口变更先比较再明确载入；状态流不会替代保存回执。

业务部署也默认禁用。在原构建与运行配置之上显式设置 `TWINE_DEPLOY_ADAPTER=go-mysql-compose-v1`，复用原运行私有根。先为服务保存接口与一条 MySQL 依赖、启动原 MySQL、固定源码并完成构建；在服务详情填写业务端口，点击「审阅部署」，核对候选与数据库后再「确认部署到本机」。后台先建立用途独立的业务库和账号、执行固定迁移，再启动原应用并核实只读探针。成功后提供独立 `127.0.0.1` 业务入口；应用使用本服务入口网络，MySQL 与迁移仍只使用 internal 私网。未知受理保持原键核实；失败保留原任务、数据与秘密，不换库、换端口、重置密码或清 dirty。首版只支持原候选 / 原配置恢复，不自动替换运行中版本。支持范围与故障边界见[部署切片](docs/development/service-deployment-slice.md)。

`Ctrl-C` 正常停止平台；重新运行同一配置即可恢复。数据库停止使用 `docker compose --env-file .env -f deploy/compose.yaml stop`，不删除卷。`down -v` 会删除项目元数据，不属于普通停止。

开发前端可设置 `TWINE_DEV_ORIGIN=http://127.0.0.1:5173` 后启动后端，再 `npm run dev --prefix web`，API 走 Vite 本机代理。生产只提供构建产物，不托管业务仓库文件。

## 检查

执行 `npm run hooks:install` 启用本仓库提交入口；`npm run precommit` 验证完整零告警门禁而不提交。实际 Git hook 额外核对源码 / 配置与索引一致，禁止部分暂存错位和绕过检查。警告与依赖漏洞都必须先清零，见[规则与受控负例](docs/development/warning-free-commits.md)。

开发必须在独立任务分支和 worktree 中进行，经检查、推送与 PR 合入 `develop`，同步本地后仅清理本任务已合并且无未保存数据的分支 / worktree；不得直接在 `develop` 开发或提交。详见[交付流程](docs/development/workflow.md#版本控制与交付)。

- `npm run test:quality`：仓库脚本行为测试。
- `go -C server test ./...`：配置、领域与真实 Git 单测；`go -C server vet ./...`、`gofmt -l server`。
- `npm run typecheck --prefix web`、`npm run lint --prefix web`、`npm run test --prefix web`、`npm run build --prefix web`。
- 首次浏览器检查前 `cd web` 后执行 `npx playwright install chromium`。
- 根目录 `npm run test:platform`：创建独立临时平台库与全新运行安装，验证模块检查、真实 Chromium、Compose 业务 MySQL、认证 / 数据见证、丢失创建响应 / 控制面重启、SQL 迁移 / 回滚 / 围栏与 `-race`。只清理归属已核实的本次资源；归属或清理未知则保留资源与绑定文件、报告位置并失败。不使用开发数据库或删除既有构建，不接入登录系统。
- `npm run quality:gate`：上述完整平台检查加脚本覆盖率、issue 元数据和本次真实 Sonar 门禁。需要通过环境注入 `SONAR_HOST_URL` 与 `SONAR_TOKEN` / `TWINE_REALM_SONAR_TOKEN`；报告只采用本次生成的 Go / LCOV。
- `docker compose --env-file .env -f deploy/compose.yaml config --quiet`：仅校验配置，不启动资源。

在“接口与依赖声明”选择服务，显式填写自己的版本化契约或载入端口示例，再保存。具名消费端口可拖线到 MySQL 提供端口，也可用表单建立关系；检查库名和用途后确认才保存。当前连线仅为声明，尚不建库或运行服务。重连、移除与契约升级均需明确确认，失败保留候选和原关系。

详细契约、失败边界与实现范围见[本地与源码切片](docs/development/local-project-slice.md)、[组件目录切片](docs/development/component-catalog-slice.md)及[服务连线切片](docs/development/service-bindings-slice.md)和[MySQL 运行切片](docs/development/mysql-runtime-slice.md)，长期设计见[平台设计概览](docs/platform-design/README.md)。工作范围以 GitHub Issue 为记录，Project #4 管规划；本地实现与测试不自动关闭 Issue 或设 Done。
