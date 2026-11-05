# 标准组件目录与 MySQL 配置草稿（Issue #6）

本切片在 [Issue #1 / #2](local-project-slice.md) 上加入标准组件目录与配置持久化。[Issue #6](https://github.com/hailingu/twine-realm/issues/6) 的实际验收以本次检查为准；MySQL 运行、业务工程方案与部署由后续任务交付；[Issue #8](service-bindings-slice.md) 已扩展服务自有契约与依赖声明，一期不接入用户账号权限。

## 单一目录与固定版本

权威材料为 [mysql-v1.json](../../server/internal/catalog/definitions/mysql-v1.json)：`twine.mysql / 1.0.0`、JSON Schema 2020-12、`mysql` 具名提供端口、MySQL wire/10 契约、固定镜像 digest、`mysql-compose/v1` 适配身份及 replace 生效模型。`requiredCapabilities` 固定对协议、持久卷、项目私网和文件凭据的要求；实际执行器仍须核实支持。当前没有启动入口，不代表执行器已实现。协议 10 的初始握手依据 [MySQL 官方协议说明](https://dev.mysql.com/doc/dev/mysql-server/9.7.2/page_protocol_connection_phase_packets_protocol_handshake_v10.html)，本平台兼容标识不推导所有客户端特性均兼容。

内容 SHA-256 按发布文件原始字节计算；实例固定 ID、版本和摘要。调整字段、缺省值、镜像、协议或生效语义须发布新版本，不能原地修改已有引用。Go 用随平台嵌入的文件校验；前端类型、独立 Ajv 校验器、缺省值与摘要由同一文件生成，`contract:check` 检测漂移。界面必须核实目录与已生成校验器匹配才开放写入。外部 `$ref` 不能联网或读宿主文件，没有任意 schema 注册入口。

组件机器契约在 `server/contract/v1/components.schema.json`，原入口以相对引用关联；Go 只注册内置分片，前端生成仅在内存组合相同文件。TS 采用普通数组类型以控制生成文件规模，数组数量仍由原 JSON Schema、独立运行时校验器和后端领域校验执行，超限负例覆盖此边界；不放宽机器契约。

配置只允许 `charset=utf8mb4`、`collation=utf8mb4_0900_ai_ci`、整数 `memoryMiB=256..8192`（缺省 512）及可选 `runtimeProfile=local-compose-v1`。服务端显式补缺省，不依赖 schema 的 default 注解改变输入；其注解语义见 [JSON Schema 官方说明](https://json-schema.org/understanding-json-schema/reference/annotations)。未选运行方案可保存，显示未就绪原因；已填非法值、未知字段与秘密字段均拒绝。配置完整仍显示“尚未运行”。

## 数据与恢复边界

新增迁移 `000003` 的 `middlewares` 表保存稳定组件 ID、所属项目、固定定义引用、非敏感规范化配置、名称和位置。`not_ready_reasons` 是该固定定义 / 配置下的解释快照，不是运行健康状态；读取时按同一版本复核。原项目 / 服务表、源码固定值和原始回执保持原义。

`CanvasSnapshot` 由原 `Project` 和互补的 `CanvasState` 组成，在同一个可重复读事务读取；标准组件不复制服务记录。所有类别共用项目 revision 与 500 组件容量。新画布布局命令按最终成员集合校验，原子保存服务与中间件全部位置；旧服务布局端点保持原契约且不改中间件位置。

创建 / 更新 / 统一布局分别返回 `CanvasReceipt`。组件变化、项目 revision 和专用回执同库事务提交；同键重放先查原结果，同键异内容或 revision 竞争返回冲突。失败不产生部分图或空回执；未知结果保留原请求与候选，先核实或原键重试。原 `Receipt` 与 `SourceReceipt` 不增加字段，新回执不从旧核实端点返回。

| 增量接口 | 行为 |
| --- | --- |
| `GET /api/v1/catalog` | 实际内置定义包、摘要与配置 schema |
| `GET /api/v1/projects/{id}/canvas` | 同库一致的服务与标准组件画布 |
| `POST /api/v1/projects/{id}/canvas/middlewares` | 加入明确版本的合法组件草稿 |
| `PUT /api/v1/projects/{id}/canvas/middlewares/{componentId}` | 更新完整配置，不偷换定义版本 |
| `PUT /api/v1/projects/{id}/canvas/layout` | `{mutationId,baseRevision,positions:[{componentId,x,y}]}` |
| `GET /api/v1/canvas-mutations/{id}` | 核实创建 / 更新 / 布局原请求 |

界面从同一确认快照投影两个类别；后续 Issue #8 改用互补的 v2 完整图读取，本文 v1 契约保持原义。保存失败与冲突保留配置候选，布局未保存时先保存 / 还原再写配置。冲突时显式读取完整最新画布，原位置和表单候选保留，不静默覆盖其他窗口的组件。未确认表单仅保留在当前页面，强制刷新可能丢失候选；已提交数据可重开及重启恢复。

## 验证与未实现范围

共享合法 / 非法配置样本在 `server/internal/catalog/definitions/configuration-cases.json`，Go 和前端实际执行同一份样本。真实 HTTP/MySQL 用例覆盖缺省、未就绪、配置更新、混合布局后序失败回滚、并发、原键与存储恢复；真实迁移分别覆盖原 v1 项目和 v2 固定源码 / 回执。Chromium 覆盖加入、修改、实际拖动、失败候选、响应丢失、并发和平台进程重启；源仓库指纹保持。

本切片不启动业务 MySQL、不建立连接或数据库账号、不执行迁移 / 构建 / 部署，不将固定镜像或目录条目当作运行事实。其他中间件按各自后续版本发布；自定义服务的多接口 / 端口声明已由 [Issue #8](service-bindings-slice.md) 提供，不从此 MySQL 定义推导全局唯一业务 schema。
