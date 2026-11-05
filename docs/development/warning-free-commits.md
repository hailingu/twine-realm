# 零告警提交门禁

用户要求：前后端代码告警必须消除后才能提交。此规则约束源码、构建、类型、lint 和适用静态分析；依赖漏洞单独检查，同样必须清零。不能靠降低级别、忽略告警、删除检查或隐藏日志通过。

## 实际入口

根 `npm run precommit` 执行完整质量门禁，不执行提交。`npm run hooks:install` 安装仓库本地 `.githooks/pre-commit`，真实 Git 提交调用 `npm run precommit -- --staged`。已有全局 / 本地自定义 hooksPath 或默认 pre-commit 时安装器明确拒绝，不覆盖既有流程。

Git hook 要求全部受检源码 / 配置与索引一致，防止仅把无告警版本留在工作区、却提交另一版本；检查前后核对索引，过程中变化则重跑。门禁不替用户暂存，不自动提交。文档和 `.zcode` 不被当作源码强制暂存，但所有提交都经过完整门禁；纯文档编辑仍可先仅做结构化评审。禁止 `--no-verify`、临时撤销 hook、用另一套检查命令或缓存绿色结果绕过。

索引一致性检查保留真实 hook 的调用者 Git 环境，以核对实际待提交索引；完整门禁启动的子工具、临时仓库与扫描器必须隔离外层 `GIT_*` 定位/配置覆盖，按自己的 cwd 识别仓库。安装器也只操作明确传入的仓库。不能让嵌套夹具的 init/add/config 误改父仓库；此隔离不移除告警或绕过检查。

## 零容忍边界

| 检查 | 执行与失败条件 |
| --- | --- |
| Go | gofmt 零差异、build / vet / test 成功；`check-command.mjs` 拒绝标准编译器 warning 诊断，即使退出码 0；集成测试用 `-count=1 -race` |
| TypeScript | 严格 `tsc --noEmit` 无错误；全部维护的源码 / 测试 / tooling 进入类型与 ESLint 配置 |
| ESLint | `--max-warnings 0`；新增规则 warning 也失败，不能改成 quiet 或降低规则 |
| Vite / 打包器 | `tooling/warningLogger.ts` 将 warn 和 warnOnce 升为构建失败；配置加载早期 `(!)` 提示也由命令边界拒绝，不提高 chunk 阈值掩盖构建警告 |
| Sonar | 本次 analysisId 的 Quality Gate 通过，另查项目全部未解决代码问题为 0；历史问题不能靠新代码门槛绕过 |
| npm 审计 | 根和 web 均执行 `npm audit --audit-level=low`，包含开发依赖，low / moderate / high / critical 任意漏洞或审计失败均阻断 |

代码警告与扫描器运行提示有不同来源。Sonar 对未提交文件的 missing blame 属 SCM 信息，不是代码问题；不屏蔽扫描日志，也不将其混称为代码告警清零失败。代码问题采用其机器接口核验为 0；构建 / lint 告警保留原始诊断并失败。

## 可执行负例

- `scripts/check-command.node.mjs`：真实子进程输出 Go / TS / 链接器 / Node / npm / Vite 标准 warning，退出码为 0，检查仍拒绝。
- `web/src/testing/buildWarnings.test.ts`：真实 Vite 插件 `this.warn` 让构建失败；真实 ESLint 只报 warning 时仍返回非零。
- `scripts/precommit.node.mjs`：临时真实 Git 仓库验证部分暂存 / 检查期间索引变化拒绝、已有 hook 配置不被覆盖；真实 commit 经过 hook 和诊断检查后失败，不产生 HEAD。不会对用户仓库执行测试提交。
- 同一测试还在自有 linked worktree 的真实 hook 中经完整门禁入口启动嵌套 Git，核对调用仓库索引、配置和父分支未被检查过程改写；正例只提交测试自有仓库。编排边界同时验证所有子工具、平台检查和扫描器得到隔离环境，调用者环境保持不变。
- Sonar 适配器测试：新代码门槛通过但仍有历史未解决问题时拒绝。

这些负例被 `test:quality` 或前端 `test` 调用，并进入 `quality:gate` / precommit。故障样本只在临时目录产生并清理，不留在工程源码中。

## minimatch 安全修复及兼容验证

原 11 项 moderate 均由 [GHSA-g3hv-75wg-m249](https://github.com/advisories/GHSA-g3hv-75wg-m249) 传播，根因是 minimatch <11.0.1 的模式放大导致资源消耗。受影响链包含 minimatch、ESLint、@eslint/js、@eslint/config-array、@eslint-community/eslint-utils、typescript-eslint，以及 @typescript-eslint 的 parser、eslint-plugin、typescript-estree、type-utils、utils。它们不是 11 个互不相关漏洞。

核查时 ESLint 10.14.0 已是稳定版，仍请求 minimatch ^10.2.6；不能用同主版本升级取得此修复。因此 web 对 minimatch 做固定 **11.0.4** override，保留 ESLint / TS ESLint，不使用 npm 建议的跨代降级。此 override 跨上游声明主版本，不冒称普通补丁升级：已核对实际 CommonJS 具名导出、Node 26 支持，并用三个实际消费位置的路径匹配行为、完整 lint、类型 / 构建 / 测试和审计验证兼容。未来上游范围纳入修复版后，可在同一验证下移除 override。

可选 macOS 文件监视依赖 fsevents 2.3.3 的现有包与预构建入口已审查，`allowScripts` 只认可该固定版本，不开启全部脚本或忽略安装警告。该策略不关闭任何代码检查。最终告警 / 漏洞及运行结果以本次交付证据为准，不能由文档自行宣称通过。
