// 本文件声明版本二的服务契约与数据库依赖；声明不代表运行连接已经生效。
package project

import "context"

// ServiceInterface 是服务自有的具名、多版本接口 schema，不与标准中间件配置混为一体。
type ServiceInterface struct {
	ID       string         `json:"id"`
	Version  string         `json:"version"`
	Protocol string         `json:"protocol"`
	Schema   map[string]any `json:"schema"`
}

// ServicePort 的语义身份与协议独立于画布 Handle 的屏幕位置。
type ServicePort struct {
	ID          string `json:"id"`
	Role        string `json:"role"`
	Protocol    string `json:"protocol"`
	Contract    string `json:"contract"`
	Cardinality string `json:"cardinality"`
	Required    bool   `json:"required"`
}

// ServiceContractDocument 固定服务自己的配置、多个接口版本和端口定义，不加载远程 schema。
type ServiceContractDocument struct {
	ID                  string             `json:"id"`
	Version             string             `json:"version"`
	ConfigurationSchema map[string]any     `json:"configurationSchema"`
	Interfaces          []ServiceInterface `json:"interfaces"`
	Ports               []ServicePort      `json:"ports"`
}

// ServiceContract 的完整身份含所属服务；同一服务内 id/version 不可原地改义。
type ServiceContract struct {
	ServiceID string                  `json:"serviceId"`
	Reference DefinitionRef           `json:"reference"`
	Document  ServiceContractDocument `json:"document"`
}

// PortEndpoint 引用稳定组件和语义端口，角色由对应固定定义裁决。
type PortEndpoint struct {
	ComponentID string `json:"componentId"`
	PortID      string `json:"portId"`
}

// CredentialReference 只预留运行时注入身份，当前不创建或保存真实秘密。
type CredentialReference struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// DatabaseBinding 固定服务消费端到 MySQL 提供端的依赖、数据库用途与不含秘密的引用。
type DatabaseBinding struct {
	ID               string              `json:"id"`
	Kind             string              `json:"kind"`
	Source           PortEndpoint        `json:"source"`
	Target           PortEndpoint        `json:"target"`
	SourceDefinition DefinitionRef       `json:"sourceDefinition"`
	TargetDefinition DefinitionRef       `json:"targetDefinition"`
	Protocol         string              `json:"protocol"`
	Contract         string              `json:"contract"`
	DatabaseID       string              `json:"databaseId"`
	DatabaseName     string              `json:"databaseName"`
	Purpose          string              `json:"purpose"`
	Credential       CredentialReference `json:"credential"`
}

// GraphState 是新增的 v2 完整图状态，原 v1 CanvasState 与回执形状保持不变。
type GraphState struct {
	SchemaVersion    int               `json:"schemaVersion"`
	Middlewares      []MiddlewareNode  `json:"middlewares"`
	ServiceContracts []ServiceContract `json:"serviceContracts"`
	Bindings         []DatabaseBinding `json:"bindings"`
}

// GraphSnapshot 在同一元数据库快照读取全部服务、标准组件、契约与依赖。
type GraphSnapshot struct {
	Project Project    `json:"project"`
	Graph   GraphState `json:"graph"`
}

// GraphOperation 的具体字段由判别联合机器契约约束，不提供任意 JSON patch。
type GraphOperation struct {
	Type         string                   `json:"type"`
	ServiceID    string                   `json:"serviceId,omitempty"`
	Document     *ServiceContractDocument `json:"document,omitempty"`
	BindingID    string                   `json:"bindingId,omitempty"`
	Source       *PortEndpoint            `json:"source,omitempty"`
	Target       *PortEndpoint            `json:"target,omitempty"`
	DatabaseName string                   `json:"databaseName,omitempty"`
	Purpose      string                   `json:"purpose,omitempty"`
}

// GraphEditCommand 在最终候选图上整体裁决，一次成功或一次拒绝。
type GraphEditCommand struct {
	MutationID   string           `json:"mutationId"`
	BaseRevision string           `json:"baseRevision"`
	Operations   []GraphOperation `json:"operations"`
}

// Graph 只读取持久声明，当前不读取源码、Docker 或业务数据库。
func (service *Service) Graph(ctx context.Context, id string) (GraphSnapshot, error) {
	if !ValidID(id) {
		return GraphSnapshot{}, Failure("INVALID_REQUEST", "项目身份无效")
	}
	value, err := service.reader.Graph(ctx, id)
	if err != nil {
		return value, err
	}
	if err := validateGraph(value.Project, value.Graph, service.definitions); err != nil {
		return GraphSnapshot{}, err
	}
	return value, nil
}

// EditGraph 先恢复原键，再构造和验证最终候选，元数据库版本锁防止并发覆盖。
func (service *Service) EditGraph(ctx context.Context, id string, command GraphEditCommand) (Receipt, error) {
	if err := validMutation(id, command.MutationID, command.BaseRevision); err != nil {
		return Receipt{}, err
	}
	if len(command.Operations) == 0 || len(command.Operations) > 50 {
		return Receipt{}, Failure("INVALID_GRAPH", "每次提交需要 1–50 个明确操作")
	}
	hash, err := requestHash("editGraph", id, command)
	if err != nil {
		return Receipt{}, err
	}
	if receipt, found, err := service.replay(ctx, command.MutationID, hash); found || err != nil {
		return receipt, err
	}
	current, err := service.reader.Graph(ctx, id)
	if err != nil {
		return Receipt{}, err
	}
	if current.Project.Revision != command.BaseRevision {
		return Receipt{}, &Error{Code: "REVISION_CONFLICT", Message: "项目已被其他窗口更新，请载入最新版本后重新确认连接", CurrentRevision: current.Project.Revision}
	}
	candidate, err := applyGraphOperations(current, command.Operations, service.definitions)
	if err != nil {
		return Receipt{}, err
	}
	if err := validateGraph(current.Project, candidate, service.definitions); err != nil {
		return Receipt{}, err
	}
	return service.writer.EditGraph(ctx, id, command, candidate, hash)
}
