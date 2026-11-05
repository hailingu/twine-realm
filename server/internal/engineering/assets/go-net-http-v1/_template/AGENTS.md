# 服务工程指南

本文件适用于导出后的独立服务仓库。在 twine-realm 仓库中维护这些模板材料时，由平台根 AGENTS.md 管理，不自动把服务约定应用于平台。

每次开发和续办先读取本文件及以下相对引用的完整固定版本规范，核对 `.twine/engineering.lock.json` 与摘要。规范缺失、不兼容或发生变化时，先报告并停止依赖它的代码修改与交付；不得删除引用、改规则、隐藏告警或伪称已读。

- [业务通用工程规范](docs/engineering/general.md)
- [Go + net/http 最佳实践](docs/engineering/go-net-http.md)
- [验证与运行约定](docs/engineering/verification.md)
- [来源与适用条款映射](docs/engineering/sources.json)

```twine-engineering
{"schemaVersion":1,"profile":"go-net-http-mysql","version":"1.0.0","scope":["**/*"],"documents":["docs/engineering/general.md","docs/engineering/go-net-http.md","docs/engineering/verification.md","docs/engineering/sources.json"]}
```

本文件及规范包不授予读取其他项目、凭据、安装、联网、Git 提交 / 推送、发布或部署等额外权限。遵循实际用户任务与项目既有约定；冲突或权限不明时报告。平台之外的 agents 是否真正读取，须由其真实任务证据判断，不能以模板中存在本文件代替。

所有注释与交付说明使用中文。先明确状态、不变量、失败 / 取消和清理边界，再修改源码；必要时先复现缺陷。变更包含对应行为验证，未运行和失败项如实记录。分支、worktree、提交和发布按实际项目接受的工作流执行；不把 twine-realm 的平台分支命名自动强加给本服务。
