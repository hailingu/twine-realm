# 本地历史与离线草稿目录

项目具体工作统一在 [GitHub Issues](https://github.com/hailingu/twine-realm/issues) 记录并关联 [twine-realm Project #4](https://github.com/users/hailingu/projects/4)。本目录保留已有中文 Markdown 历史、离线草稿及明确链接的证据快照，不默认镜像每个远端任务。职责与状态规则见 [Projects 与 Issues 协作](../docs/development/project-tracking.md)，内容质量见[问题记录规范](../docs/development/issue-standard.md)。

## 与远端记录的边界

- 已有文件和编号不删除、不重排。迁移前先查重，在正文登记权威 Issue URL、核对日期与迁移说明，后续事实在原 Issue 维护；本地仅按需要保留标注日期的快照。
- 离线草稿不表示已经创建 / 更新 GitHub 或 Project；联网后先查重，再登记并核对关联，不保留两份可独立流转的状态。
- `ISSUE-0001` 与 GitHub `#1` 是不同命名空间。已创建的 [首个功能 #1](https://github.com/hailingu/twine-realm/issues/1) 不在本目录另建同名副本。
- `npm run issue:status` 仅检查本目录已有格式，不访问 GitHub，也不证明 Project 已同步；本地零条记录不代表远端无任务。

以下编号、文件头与命令规则仅用于确需保留的本地记录。

## 本地创建与编号

1. 先搜索现有记录，确认问题未被登记；一个问题一份文件，不创建无事实依据的占位缺陷。
2. 从 [_template.md](_template.md) 复制为 `ISSUE-0001-中文短名.md`，按已有最大编号递增，禁止重用编号。
3. 填写文件头，按缺陷、功能、工程债务或已知边界裁剪正文，补齐可验证的验收标准。
4. 创建后再次检查编号冲突；发现并发重复时重新分配本次新编号并修正链接，不能覆盖已有记录。

稳定编号是文件头的 `id`，文件名和编号在后续状态变化中保持不变。记录可按编号检索，不强制维护一份易过时的手工状态汇总。

## 元数据与生命周期

文件头字段：`id`、`title`、`type`、`status`、`priority`、`scope`、`createdAt`、`updatedAt`；可选 `owner`。标题形如 `P2(server): 节点交互问题摘要`，`priority` 与标题一致。

| 字段 | 可用值 |
| --- | --- |
| `type` | 缺陷、功能、工程债务、已知边界 |
| `status` | 待处理、进行中、待验证、已关闭、暂缓 |
| `priority` | P1、P2、P3 |
| `scope` | server、web、deploy、design、docs、tooling；跨域记录用 `+` 联写（如 `server+web`） |

本地历史 / 草稿快照沿用「待处理 → 进行中 → 待验证 → 已关闭」及「暂缓」枚举；已经关联远端时，不以本地状态代替 Issue 的实际 Open / Closed 与 Project 阶段。复发或新证据优先维护 / 重开原 Issue，保留历史处置。

更新本地快照时维护 `updatedAt`、来源链接、核对日期和处置记录。关闭按权威 Issue 验收并复核 Project；不把本地快照关闭写成远端已结束，亦不写成代码已提交、已推送或已部署。

编号仍从现有最大编号人工递增分配，没有自动编号器。元数据与状态由仓库根 `npm run issue:status` 自动校验：文件头必填字段、type/status/priority/scope 枚举、编号与文件名一致且唯一、日期格式及 `updatedAt` 不早于创建日期和最后处置记录；候选 `ISSUE-*.md` 文件名不合规时报错，处置日期支持正文、括号说明和日期标题，且只读取「处置记录」章节，忽略其他章节及代码示例。该命令同时输出状态统计与未关闭清单（`--json` 为结构化摘要），未关闭是正常工作流状态，仅元数据或文件名违规才非零退出。验收与证据质量仍采用结构化评审及链接核对。模板不是实际问题记录，不占用编号。本命令只统计本地文件；GitHub / Project 状态须另外读取，不据此推断远端总数或一致性。
