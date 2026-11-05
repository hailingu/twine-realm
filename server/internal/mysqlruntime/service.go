// 本文件只接受已确认画布的固定运行目标；Docker 副作用由持久后台操作拥有。
package mysqlruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// Capability 如实表达是否显式配置文件适配器，不代表 Docker 此刻健康。
type Capability struct {
	SchemaVersion  int    `json:"schemaVersion"`
	Enabled        bool   `json:"enabled"`
	Adapter        string `json:"adapter"`
	Code           string `json:"code"`
	Message        string `json:"message"`
	Image          string `json:"image"`
	ComposeVersion string `json:"composeVersion"`
}

// Service 组合目录、同库受理和查询，不持有浏览器连接或账号身份。
type Service struct {
	commands    CommandStore
	queries     QueryStore
	canvas      CanvasReader
	definitions Definitions
	driver      *Driver
}

// NewService 显式装配端口；未配置适配器时仍可查询已存在的历史。
func NewService(commands CommandStore, queries QueryStore, canvas CanvasReader, definitions Definitions, driver *Driver) *Service {
	return &Service{commands: commands, queries: queries, canvas: canvas, definitions: definitions, driver: driver}
}

// Capability 只报告配置，不把静态配置标为运行成功。
func (service *Service) Capability() Capability {
	value := Capability{SchemaVersion: 1, Image: Image, Code: "RUNTIME_DISABLED", Message: "尚未显式配置本机运行适配器，项目与配置草稿仍可使用"}
	if service.driver != nil {
		value.Enabled, value.Adapter, value.Code = true, "file-secrets-v1", "CONFIGURED"
		value.Message = "已配置本机文件秘密适配器；执行时另核实 Docker、资源归属与认证就绪"
		value.ComposeVersion = service.driver.Environment().ComposeVersion
	}
	return value
}

// Accept 先返回原键结果，再检查当前目录；新目标只产生同库受理记录。
func (service *Service) Accept(ctx context.Context, id, componentID string, command Command) (Receipt, error) {
	if err := validateCommand(id, componentID, command); err != nil {
		return Receipt{}, err
	}
	hash, err := commandHash(id, componentID, command)
	if err != nil {
		return Receipt{}, err
	}
	if receipt, found, err := service.replay(ctx, command.MutationID, hash); found || err != nil {
		return receipt, err
	}
	if service.driver == nil {
		return Receipt{}, project.Failure("RUNTIME_DISABLED", "未配置本机运行适配器，未接受运行目标")
	}
	resource, err := service.candidate(ctx, id, componentID, command)
	if err != nil {
		return Receipt{}, err
	}
	return service.commands.AcceptRuntime(ctx, AcceptedInput{ProjectID: id, ComponentID: componentID, Command: command, Hash: hash, Resource: resource})
}

// candidate 对运行核实必需值；停止沿用原实例，不让后来草稿错误阻止停止。
func (service *Service) candidate(ctx context.Context, id, componentID string, command Command) (Resource, error) {
	if command.Desired == "stopped" {
		return service.originalResource(ctx, id, componentID)
	}
	canvas, err := service.canvas.Canvas(ctx, id)
	if err != nil {
		return Resource{}, err
	}
	if canvas.Project.Revision != command.BaseRevision {
		return Resource{}, &project.Error{Code: "REVISION_CONFLICT", Message: "画布声明已改变，请读取最新版本后重新确认运行目标", CurrentRevision: canvas.Project.Revision}
	}
	for _, node := range canvas.Canvas.Middlewares {
		if node.ID == componentID {
			return service.configuredResource(id, node)
		}
	}
	return Resource{}, project.Failure("COMPONENT_NOT_FOUND", "MySQL 组件不存在或不属于此项目")
}

// originalResource 沿用已固定实例，即使后来声明不合法也仍可明确停止。
func (service *Service) originalResource(ctx context.Context, id, componentID string) (Resource, error) {
	value, err := service.queries.RuntimeSnapshot(ctx, id)
	if err != nil {
		return Resource{}, err
	}
	if value.Target == nil {
		return Resource{}, project.Failure("RUNTIME_NOT_STARTED", "项目尚未接受 MySQL 运行目标")
	}
	if value.Target.Resource.ComponentID != componentID {
		return Resource{}, project.Failure("RUNTIME_COMPONENT_CONFLICT", "组件不是此项目已登记的运行实例")
	}
	return value.Target.Resource, nil
}

// configuredResource 只从匹配revision的已保存节点解析完整受支持配置和摘要。
func (service *Service) configuredResource(id string, node project.MiddlewareNode) (Resource, error) {
	configuration, reasons, err := service.definitions.Normalize(node.Definition, node.Configuration)
	if err != nil {
		return Resource{}, err
	}
	if len(reasons) != 0 || configuration["runtimeProfile"] != "local-compose-v1" {
		return Resource{}, project.Failure("RUNTIME_NOT_READY", "先保存完整的本机运行配置，再接受目标")
	}
	hash, err := Fingerprint(struct {
		Definition    project.DefinitionRef
		Configuration map[string]any
	}{node.Definition, configuration})
	if err != nil {
		return Resource{}, err
	}
	return Resource{ProjectID: id, ComponentID: node.ID, InstallationID: service.driver.InstallationID(), DaemonID: service.driver.Environment().DaemonID, Definition: node.Definition, Configuration: configuration, ConfigurationHash: hash, Image: Image}, nil
}

// WithID 固定资源命名，名称来自完整随机身份而不是可修改显示名。
func (resource Resource) WithID(id string) Resource {
	resource.ID, resource.SecretRef = id, id
	hash := sha256.Sum256([]byte(resource.InstallationID + "/" + resource.ProjectID + "/" + id))
	prefix := "tr-" + hex.EncodeToString(hash[:16])
	resource.Network, resource.Volume, resource.Container = prefix+"-net", prefix+"-data", prefix+"-mysql"
	return resource
}

// commandHash 纳入路由归属与完整意图，同键不能跨项目或改变目标。
func commandHash(id, componentID string, command Command) ([32]byte, error) {
	data, err := json.Marshal(struct {
		Kind, ProjectID, ComponentID string
		Command                      Command
	}{"mysqlRuntime", id, componentID, command})
	return sha256.Sum256(data), err
}

// validateCommand 在查询前限制身份、版本与本切片动作集合。
func validateCommand(id, componentID string, command Command) error {
	if !project.ValidID(id) || !project.ValidID(componentID) || !project.ValidID(command.MutationID) || !Number(command.BaseRevision) || !Number(command.BaseGeneration) {
		return project.Failure("INVALID_REQUEST", "运行请求身份或版本无效")
	}
	if command.Desired != "running" && command.Desired != "stopped" {
		return project.Failure("INVALID_REQUEST", "只支持明确运行或停止目标")
	}
	if command.RetryOf != "" && !project.ValidID(command.RetryOf) {
		return project.Failure("INVALID_REQUEST", "原操作身份无效")
	}
	return nil
}

// replay 不因配置改变、Docker故障或重启重复受理已确认的原请求。
func (service *Service) replay(ctx context.Context, id string, hash [32]byte) (Receipt, bool, error) {
	value, stored, err := service.commands.RuntimeReceipt(ctx, id)
	var failure *project.Error
	if errors.As(err, &failure) && failure.Code == "MUTATION_NOT_FOUND" {
		return Receipt{}, false, nil
	}
	if err != nil {
		return Receipt{}, false, err
	}
	if stored != hash {
		return Receipt{}, true, project.Failure("IDEMPOTENCY_CONFLICT", "相同运行请求身份已用于不同内容")
	}
	value.Replayed = true
	return value, true, nil
}

// Receipt 核实原请求，不触发新的 Docker 命令。
func (service *Service) Receipt(ctx context.Context, id string) (Receipt, error) {
	if !project.ValidID(id) {
		return Receipt{}, project.Failure("INVALID_REQUEST", "运行请求身份无效")
	}
	value, _, err := service.commands.RuntimeReceipt(ctx, id)
	value.Replayed = err == nil
	return value, err
}

// Snapshot 读取同库快照，不由浏览器猜测成功或新鲜度。
func (service *Service) Snapshot(ctx context.Context, id string) (Snapshot, error) {
	if !project.ValidID(id) {
		return Snapshot{}, project.Failure("INVALID_REQUEST", "项目身份无效")
	}
	return service.queries.RuntimeSnapshot(ctx, id)
}

// Operation 保留原目标的诊断，即使它已被新目标替代。
func (service *Service) Operation(ctx context.Context, id, operationID string) (Operation, error) {
	if !project.ValidID(id) || !project.ValidID(operationID) {
		return Operation{}, project.Failure("INVALID_REQUEST", "项目或运行操作身份无效")
	}
	return service.queries.RuntimeOperation(ctx, id, operationID)
}

// Events 查询有界持久历史，游标只在同一项目解释。
func (service *Service) Events(ctx context.Context, id, sequence string) (EventPage, error) {
	if !project.ValidID(id) || !Number(sequence) {
		return EventPage{}, project.Failure("INVALID_REQUEST", "项目或事件游标无效")
	}
	return service.queries.RuntimeEvents(ctx, id, sequence)
}
