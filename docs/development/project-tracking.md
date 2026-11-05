# GitHub Projects 与 Issues 协作

## 管理入口与适用边界

2026-11-04 用户确认使用仓库关联的 GitHub Project 管理开发工作。具体任务以 [hailingu/twine-realm 的 GitHub Issues](https://github.com/hailingu/twine-realm/issues) 为权威工作记录；[twine-realm Project #4](https://github.com/users/hailingu/projects/4) 负责规划、排序与可视化跟踪。仓库 [Projects 入口](https://github.com/hailingu/twine-realm/projects) 本次只关联这一个现有项目；不另建平行项目。

这里的 GitHub 是 twine-realm 自身的开发管理工具，不是产品运行时的 GitHub 同步或用户账号能力。当前首个功能任务仍要求本机使用，无需产品内登录、配对或账号权限接入。

本规范不授予无关的外部通信、推送、合并或发布权限。按用户已授权的任务范围维护 Issue 与 Project；权限、网络或工具受阻时保留已完成结果并报告，不把部分操作当作完整完成。

## 各处记录的责任

| 记录 | 权威内容 | 协作规则 |
| --- | --- | --- |
| GitHub Issue | 背景、范围、验收、讨论、阻塞原因、实施 / 验证证据、依赖和最终关闭理由 | 用仓库与 Issue 编号 / URL 定位；具体工作只维护这一份正文与结论 |
| Project item | 对原 Issue 的引用、Status、排期 Priority、排序及需要的计划字段 | 同一工作在本 Project 保留一个 Issue item，不复制正文，不为同一实现再建 PR 或草稿任务卡 |
| PR / commit | 实现差异、审阅与检查证据 | 关联原 Issue；PR 是否合并、Issue 是否结束、功能是否部署分别核实 |
| 本地 `issues/` | 已有历史、明确标注的离线草稿或带权威链接的证据快照 | 保留文件与编号；不默认给每个 GitHub Issue 建一份可独立流转的本地副本 |

Issue 的 Open / Closed 及关闭理由是工作是否结束的事实来源；Project Status 表达更细的规划 / 执行阶段。缺陷严重性写在 Issue，排期优先级写在 Project，不能只因都叫 P1 就互相覆盖。负责人以 Issue assignees 为准，不在正文和多个卡片里维护不同名单。

## 创建与关联

1. 先读原任务、项目视图和仓库所有状态的相关 Issue；按标题、目标、验收和关联关系去重，不能只检索 Open 标题。已有同项工作就复用原 Issue / item，不重复创建。
2. 确认仓库关联的目标 Project。没有目标或多个目标无法判定时先报告，不自建项目，也不只创建一个未关联的 Issue。
3. 一个可执行功能 / 缺陷对应一个 Issue，写清背景、范围、可验证验收与后续边界。允许先用 Project draft 澄清，但进入 Ready / In progress 前必须转为正确仓库的 Issue，并保留原 item；不另建 Issue 后留下同名草稿。
4. 创建 Issue 后查询是否已由自动工作流加入目标 Project；没有才显式添加。只用同一个 Issue URL / item ID 接续，不因响应超时而重新建任务。核实 Issue 内容、Project 关联、Status 和真实链接后才报告创建完成。
5. 实现前从 Issue 接受的范围确定切片与验证；设计总目标不自动扩大该任务。跨任务依赖在 Issue 中关联，Project 只呈现同一关系。

2026-11-04 创建 [Issue #1：将已有本地服务 Git 仓库加入画布并持久化](https://github.com/hailingu/twine-realm/issues/1)，对应 [Project item](https://github.com/users/hailingu/projects/4/views/1?pane=issue&itemId=293222657)。创建时为 Open / Backlog；现已有[本地切片实现](local-project-slice.md)，具体交付状态以 Issue、PR 和 Project 的实时读取为准，不把创建时快照继续当作当前事实。本地不再建立同名 `ISSUE-0001`；后续 Todo / MySQL 部署仍在该任务范围之外。

下一任务为 [Issue #2：固定已登记服务的 Git 提交并追溯源码版本](https://github.com/hailingu/twine-realm/issues/2)，已关联同一 Project，依赖 #1 合入 develop。它将明确区分登记快照、当前 Git 观察和用户固定的 commit，为后续构建提供追溯；当前仅为计划，尚未实现，不替用户选择业务技术栈，也不包含生成 / 运行 Todo。

## 状态与验收门槛

下列状态名来自当前 Project，未增加其他选项；状态显示不替代 Issue 的证据。

| Project Status | Issue 状态与进入条件 |
| --- | --- |
| Backlog | Open；已登记，尚未开始。范围仍需澄清时在 Issue 记录待定项 |
| Ready | Open；范围、依赖和验收足够明确，可以领取 |
| In progress | Open；已开始实施，持续记录实际进展、阻塞和相关实现 |
| In review | Open；已提交审阅或待验证，列出差异、实际检查、未运行项和剩余验收 |
| Done | 必须核对 Issue 的 Closed 与关闭理由。完成类须已有验收证据；取消 / 重复 / 不做须明确理由，不能因自动变为 Done 就宣称功能已实现 |

阻塞、延期和等待决策先在 Issue 记录原因与接续条件，Project 保持适当的非 Done 阶段；不临时发明不存在的 Blocked 字段，也不以关闭掩盖未完成任务。

## 当前已核对的自动化配置

2026-11-04 在 [Project Workflows](https://github.com/users/hailingu/projects/4/workflows) 只读核对，界面显示 **7 enabled**，并读取以下配置。本次没有修改开关，也没有通过关闭 / 重开真实任务测试这些事件；配置已启用不等于每次远端请求都已成功完成，执行后仍须核实结果。

| 工作流 | 界面配置与影响 | 操作时的约束 |
| --- | --- | --- |
| Auto-add to project | On；仓库 twine-realm，筛选 `is:issue is:open` | 创建后先查关联；不假定历史 Closed Issue、PR 或其他仓库内容也会加入 |
| Auto-add sub-issues to project | On；项目项的 sub-issues 加入项目 | 已有子项先查询，不能再复制一个同名卡；子项须有独立范围 |
| Item added to project | On；Status → Backlog | 新项默认待开始，不因被加入就声称已领取 |
| Item closed | On；Status → Done | 按 Issue 关闭理由判断完成或终止；核对关联和实际状态 |
| Auto-close issue | On；Status → Done 时关闭 Issue | **未验收不得把 Issue item 拖到 Done。** 这会触发真实关闭，不能用作临时整理 |
| Pull request linked to issue | On；Status → In progress | PR 关联后核对是否符合实际阶段，不把关联本身当作实现完成 |
| Pull request merged | On；Status → Done | 对 PR 项核对其状态；不能据此推断关联 Issue 的全部验收、发布或部署已完成 |

Item reopened 页面本次只显示设置 Ready 的模板，未见 On；不依赖重开自动复位。其他未核对或后来变更的配置也不作为同步保证。新增、关闭、重开和 PR 关联后都要检查原 Issue 与 Project，必要时补正原 item；不为修正状态新建任务。

## 更新、关闭与冲突修复

1. **实施进展**：范围、验收、阻塞与证据更新到原 Issue；只变更计划顺序 / 阶段时更新 Project。内容和阶段同时变化，先保留 Issue 的事实，再核对 Project 字段。
2. **完成并关闭**：逐项验收，记录已完成内容、提交 / PR、实际检查和未运行原因。代码完成类还须按[交付流程](workflow.md#版本控制与交付)通过门禁与 PR CI、合入 develop 并核实本地同步；未合并不按完整交付关闭。满足后关闭原 Issue，再检查 Project 是否 Done；清理未能安全完成时保留并如实记录。自动化未生效时只补正原卡片，不重复关闭、删卡或重建。
3. **取消或重复**：在 Issue 写明原因及权威后续链接，以相应的非完成理由关闭；自动 Done 不解释为已实现。必要归档仍保留 Issue 历史，归档或移出项目不等于修复。
4. **误操作或重新打开**：若误拖 Done 触发提前关闭，核实后重开原 Issue，记录原因，把原 item 恢复到 Ready / In progress / In review 中符合实际的阶段，并再次读取两处结果。只重开 Issue 而卡片仍为 Done 属于未处理完的冲突。
5. **Open 与 Done 并存**：以 Issue 是否真的验收完成为依据检查；未完成则保持 / 恢复 Open 并将卡片移出 Done，已完成才补充验收并关闭。Closed 与非 Done 并存则先核对关闭理由，再修正同一卡片；不能仅凭看板反向编造完成证据。
6. **版本控制与发布**：开发使用独立分支和 worktree，推送后以 PR 合入 develop，随后同步本地并安全清理本任务的已合并 origin / 本地分支与 worktree。PR 引用原 Issue；只有完整履行关闭条件的改动才使用完成关联，部分工作只引用和说明剩余范围。合并、自动关闭、工作区已改动、本地提交、已推送和已部署分别报告。当前默认分支为 main，不能依赖合入 develop 的关闭关键词自动关闭 Issue；合并后仍须读取并按验收结果处理原 Issue。

关闭和重开可能受远端自动化、分支和权限影响，每一步都读取实际结果；失败时报告哪个 Issue / Project item 已完成更新、哪个仍不一致及恢复入口。没有独立同步器或原子事务，不声称两处同时成功。

## 本地记录、检查与离线边界

既有 `ISSUE-xxxx` 文件保留原编号、内容与处置历史；需要迁移时先查重，建立一次权威 GitHub Issue，并在本地正文补充链接、快照日期与迁移说明，不删除历史、不继续双向独立更新。本地编号与 GitHub `#n` 是两个命名空间，不要求相等。

离线只能形成明确标注的本地草稿，不能报告 GitHub Issue / Project 已更新；恢复连接后重新查重、登记并核实。涉及敏感日志、绝对路径或凭据时先脱敏，不能因为项目私有就把不适合公开的数据写到公开仓库 Issue。

`npm run issue:status` 仍只校验本地 `issues/` 文件的已有元数据规则，**不读取 GitHub、不校验 Project，也不自动同步**。本地记录为零不代表没有远端任务。其合法性检查和 GitHub / Project 人工或工具查询分别报告，不能用本地校验替代远端状态核实。格式和验收要求见[问题记录规范](issue-standard.md)，保留目录说明见 [issues/README.md](../../issues/README.md)。
