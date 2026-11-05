// 本文件定义标准组件草稿及完整画布读取，不把目录条目冒称运行实例。
package project

import (
	"context"
	"encoding/json"
	"strings"
)

// DefinitionRef 固定不可变目录版本与内容，不按最新版本自动解析。
type DefinitionRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

// PortDefinition 是具名协议端口，屏幕坐标和本地化名称不参与身份。
type PortDefinition struct {
	ID          string `json:"id"`
	Role        string `json:"role"`
	Protocol    string `json:"protocol"`
	Contract    string `json:"contract"`
	Cardinality string `json:"cardinality"`
}

// NodeDefinition 由受控离线目录发布配置与端口，不执行任意插件脚本。
type NodeDefinition struct {
	Reference             DefinitionRef    `json:"reference"`
	Kind                  string           `json:"kind"`
	Title                 string           `json:"title"`
	ConfigurationSchema   json.RawMessage  `json:"configurationSchema"`
	Ports                 []PortDefinition `json:"ports"`
	Image                 string           `json:"image"`
	Adapter               string           `json:"adapter"`
	RequiredCapabilities  []string         `json:"requiredCapabilities"`
	Effect                string           `json:"effect"`
	RequiredForDeployment []string         `json:"requiredForDeployment"`
}

// DefinitionCatalog 保留离线包摘要；实际运行支持由运行适配器另行核实。
type DefinitionCatalog struct {
	SchemaVersion int              `json:"schemaVersion"`
	Digest        string           `json:"digest"`
	Definitions   []NodeDefinition `json:"definitions"`
}

// MiddlewareNode 保存非敏感草稿，当前没有运行状态或秘密值。
type MiddlewareNode struct {
	ID              string         `json:"id"`
	Kind            string         `json:"kind"`
	DisplayName     string         `json:"displayName"`
	Definition      DefinitionRef  `json:"definition"`
	Configuration   map[string]any `json:"configuration"`
	Position        Position       `json:"position"`
	NotReadyReasons []string       `json:"notReadyReasons"`
}

// CanvasState 是与原服务集合互补的标准组件声明，不复制原服务实体。
type CanvasState struct {
	SchemaVersion int              `json:"schemaVersion"`
	Middlewares   []MiddlewareNode `json:"middlewares"`
}

// CanvasSnapshot 在同一数据库读取边界返回原服务和标准组件，共用项目 revision。
type CanvasSnapshot struct {
	Project Project     `json:"project"`
	Canvas  CanvasState `json:"canvas"`
}

// MiddlewareCommand 只允许目录定义的非敏感配置，未知字段由对应版本 schema 拒绝。
type MiddlewareCommand struct {
	MutationID    string         `json:"mutationId"`
	BaseRevision  string         `json:"baseRevision"`
	Definition    DefinitionRef  `json:"definition"`
	DisplayName   string         `json:"displayName"`
	Configuration map[string]any `json:"configuration"`
	Position      Position       `json:"position"`
}

// CanvasPosition 统一定位服务与中间件，不把业务类别写进布局身份。
type CanvasPosition struct {
	ComponentID string  `json:"componentId"`
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
}

// CanvasLayoutCommand 一次保存所有类别的位置，失败时没有部分布局更新。
type CanvasLayoutCommand struct {
	MutationID   string           `json:"mutationId"`
	BaseRevision string           `json:"baseRevision"`
	Positions    []CanvasPosition `json:"positions"`
}

// Definitions 是用例所需的受控目录能力，不接收用户提供的 schema 引用。
type Definitions interface {
	List() DefinitionCatalog
	Normalize(DefinitionRef, map[string]any) (map[string]any, []string, error)
	ValidateServiceDocument(ServiceContractDocument) (ServiceContractDocument, DefinitionRef, error)
}

// Catalog 返回当前发布的离线目录，供界面核对版本和实际支持边界。
func (service *Service) Catalog() DefinitionCatalog { return service.definitions.List() }

// Canvas 返回一致的完整画布，而不是在前端拼接两个不同时刻的读取。
func (service *Service) Canvas(ctx context.Context, id string) (CanvasSnapshot, error) {
	if !ValidID(id) {
		return CanvasSnapshot{}, Failure("INVALID_REQUEST", "项目身份无效")
	}
	value, err := service.reader.Canvas(ctx, id)
	if err != nil {
		return value, err
	}
	for index := range value.Canvas.Middlewares {
		node := &value.Canvas.Middlewares[index]
		_, reasons, err := service.definitions.Normalize(node.Definition, node.Configuration)
		if err != nil {
			return CanvasSnapshot{}, err
		}
		node.NotReadyReasons = reasons
	}
	return value, nil
}

// PutMiddleware 在回放原键后应用受控缺省值；创建和修改均不隐含运行或升级。
func (service *Service) PutMiddleware(ctx context.Context, id, componentID string, command MiddlewareCommand) (Receipt, error) {
	command.DisplayName = strings.TrimSpace(command.DisplayName)
	if err := validMutation(id, command.MutationID, command.BaseRevision); err != nil {
		return Receipt{}, err
	}
	if componentID != "" && !ValidID(componentID) {
		return Receipt{}, Failure("INVALID_REQUEST", "组件身份无效")
	}
	if err := validName(command.DisplayName); err != nil {
		return Receipt{}, err
	}
	if !ValidPosition(command.Position) {
		return Receipt{}, Failure("INVALID_POSITION", "节点位置超出允许范围")
	}
	kind := "addMiddleware"
	if componentID != "" {
		kind = "updateMiddleware"
	}
	hash, err := requestHash(kind, id, struct {
		ID      string
		Command MiddlewareCommand
	}{componentID, command})
	if err != nil {
		return Receipt{}, err
	}
	if receipt, found, err := service.replay(ctx, command.MutationID, hash); found || err != nil {
		return receipt, err
	}
	configuration, reasons, err := service.definitions.Normalize(command.Definition, command.Configuration)
	if err != nil {
		return Receipt{}, err
	}
	command.Configuration = configuration
	return service.writer.PutMiddleware(ctx, id, componentID, command, reasons, hash)
}

// SaveCanvasLayout 校验统一位置集合，再交由同库原子写入保存所有类别。
func (service *Service) SaveCanvasLayout(ctx context.Context, id string, command CanvasLayoutCommand) (Receipt, error) {
	if err := validMutation(id, command.MutationID, command.BaseRevision); err != nil {
		return Receipt{}, err
	}
	if len(command.Positions) > 500 {
		return Receipt{}, Failure("INVALID_LAYOUT", "画布最多支持 500 个组件")
	}
	seen := map[string]bool{}
	for _, position := range command.Positions {
		if !ValidID(position.ComponentID) || seen[position.ComponentID] || !ValidPosition(Position{X: position.X, Y: position.Y}) {
			return Receipt{}, Failure("INVALID_LAYOUT", "位置身份重复、无效或坐标越界")
		}
		seen[position.ComponentID] = true
	}
	hash, err := requestHash("saveCanvasLayout", id, command)
	if err != nil {
		return Receipt{}, err
	}
	if receipt, found, err := service.replay(ctx, command.MutationID, hash); found || err != nil {
		return receipt, err
	}
	return service.writer.SaveCanvasLayout(ctx, id, command, hash)
}
