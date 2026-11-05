// 本文件只构造内存候选；暂时不完整的关系在全部操作之后整体裁决。
package project

import "encoding/json"

// applyGraphOperations 深复制原声明，失败不会污染读取快照或调用者的候选输入。
func applyGraphOperations(current GraphSnapshot, operations []GraphOperation, definitions Definitions) (GraphState, error) {
	data, err := json.Marshal(current.Graph)
	if err != nil {
		return GraphState{}, err
	}
	var candidate GraphState
	if err := json.Unmarshal(data, &candidate); err != nil {
		return candidate, err
	}
	resolve := map[string]bool{}
	for _, operation := range operations {
		if err := applyGraphOperation(current.Project, &candidate, operation, definitions, resolve); err != nil {
			return GraphState{}, err
		}
	}
	for index := range candidate.Bindings {
		binding := &candidate.Bindings[index]
		if !resolve[binding.ID] {
			continue
		}
		if !hasService(current.Project, binding.Source.ComponentID) {
			return GraphState{}, Failure("INVALID_BINDING", "数据库依赖必须从本项目服务指向本项目 MySQL")
		}
		source, ok := serviceContract(candidate, binding.Source.ComponentID)
		if !ok {
			return GraphState{}, Failure("SERVICE_CONTRACT_REQUIRED", "服务尚未声明接口 / 端口契约")
		}
		target, ok := middlewareNode(candidate, binding.Target.ComponentID)
		if !ok {
			return GraphState{}, Failure("INVALID_BINDING", "目标中间件不存在或不属于此项目")
		}
		binding.SourceDefinition, binding.TargetDefinition = source.Reference, target.Definition
	}
	return candidate, nil
}

// applyGraphOperation 只开放具名领域动作，不接受用户指定数据库表或任意字段路径。
func applyGraphOperation(current Project, graph *GraphState, operation GraphOperation, definitions Definitions, resolve map[string]bool) error {
	switch operation.Type {
	case "service.setContract":
		return setServiceContract(current, graph, operation, definitions)
	case "binding.create":
		binding, err := newBinding(operation)
		if err != nil {
			return err
		}
		graph.Bindings = append(graph.Bindings, binding)
		resolve[binding.ID] = true
		return nil
	case "binding.reconnect":
		return reconnectGraphBinding(graph, operation, resolve)
	case "binding.remove":
		for index, binding := range graph.Bindings {
			if binding.ID == operation.BindingID {
				graph.Bindings = append(graph.Bindings[:index], graph.Bindings[index+1:]...)
				return nil
			}
		}
	default:
		return Failure("INVALID_GRAPH", "不支持此图编辑操作")
	}
	return Failure("BINDING_NOT_FOUND", "依赖绑定不存在或不属于此项目")
}

// reconnectGraphBinding 只替换候选中的既有边，最终定义解析在全部操作后执行。
func reconnectGraphBinding(graph *GraphState, operation GraphOperation, resolve map[string]bool) error {
	for index, old := range graph.Bindings {
		if old.ID != operation.BindingID {
			continue
		}
		binding, err := reconnectedBinding(old, operation)
		if err != nil {
			return err
		}
		graph.Bindings[index] = binding
		resolve[binding.ID] = true
		return nil
	}
	return Failure("BINDING_NOT_FOUND", "依赖绑定不存在或不属于此项目")
}

// setServiceContract 只改变明确服务的活动引用，旧版本内容由存储层永久核对。
func setServiceContract(current Project, graph *GraphState, operation GraphOperation, definitions Definitions) error {
	if !hasService(current, operation.ServiceID) || operation.Document == nil {
		return Failure("INVALID_SERVICE_CONTRACT", "服务不存在、跨项目或缺少完整契约")
	}
	document, reference, err := definitions.ValidateServiceDocument(*operation.Document)
	if err != nil {
		return err
	}
	value := ServiceContract{ServiceID: operation.ServiceID, Reference: reference, Document: document}
	for index, old := range graph.ServiceContracts {
		if old.ServiceID == operation.ServiceID {
			graph.ServiceContracts[index] = value
			return nil
		}
	}
	graph.ServiceContracts = append(graph.ServiceContracts, value)
	return nil
}

// newBinding 分配稳定元数据身份，不创建数据库、账号或真实秘密。
func newBinding(operation GraphOperation) (DatabaseBinding, error) {
	if operation.Source == nil || operation.Target == nil {
		return DatabaseBinding{}, Failure("INVALID_BINDING", "请明确选择两端组件和语义端口")
	}
	id, err := NewID()
	if err != nil {
		return DatabaseBinding{}, err
	}
	databaseID, err := NewID()
	if err != nil {
		return DatabaseBinding{}, err
	}
	credentialID, err := NewID()
	if err != nil {
		return DatabaseBinding{}, err
	}
	return DatabaseBinding{ID: id, Kind: "databaseDependency", Source: *operation.Source, Target: *operation.Target, Protocol: "mysql", Contract: "mysql-wire/10", DatabaseID: databaseID, DatabaseName: operation.DatabaseName, Purpose: operation.Purpose, Credential: CredentialReference{ID: credentialID, Version: "1"}}, nil
}

// reconnectedBinding 保持边 ID；相同库保留数据身份，权限或归属变化使用新的凭据引用。
func reconnectedBinding(old DatabaseBinding, operation GraphOperation) (DatabaseBinding, error) {
	value, err := newBinding(operation)
	if err != nil {
		return value, err
	}
	value.ID = old.ID
	if old.Source.ComponentID == value.Source.ComponentID && old.Target.ComponentID == value.Target.ComponentID && old.DatabaseName == value.DatabaseName {
		value.DatabaseID = old.DatabaseID
		if old.Purpose == value.Purpose {
			value.Credential = old.Credential
		}
	}
	return value, nil
}

// hasService 按稳定身份和当前项目成员判断，不由名称或路径猜测归属。
func hasService(current Project, id string) bool {
	for _, node := range current.Nodes {
		if node.ID == id {
			return true
		}
	}
	return false
}

// serviceContract 查询本服务明确登记的活动契约，不伪造默认 database 端口。
func serviceContract(graph GraphState, id string) (ServiceContract, bool) {
	for _, value := range graph.ServiceContracts {
		if value.ServiceID == id {
			return value, true
		}
	}
	return ServiceContract{}, false
}

// middlewareNode 按实例身份定位，目录版本仍由实例上的不可变引用决定。
func middlewareNode(graph GraphState, id string) (MiddlewareNode, bool) {
	for _, value := range graph.Middlewares {
		if value.ID == id {
			return value, true
		}
	}
	return MiddlewareNode{}, false
}
