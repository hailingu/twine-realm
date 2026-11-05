// 本文件对最终候选图进行关系校验，必需但未绑定的消费端口仍可保留为草稿。
package project

import "regexp"

var databaseName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)
var credentialVersion = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)

// validateGraph 统一校验归属、固定定义、完整关系和基数，不以线条颜色推断兼容。
func validateGraph(current Project, graph GraphState, definitions Definitions) error {
	if graph.SchemaVersion != 2 || len(graph.Bindings) > 1000 || len(graph.ServiceContracts) > len(current.Nodes) {
		return Failure("INVALID_GRAPH", "图版本或容量无效")
	}
	contracts := map[string]ServiceContract{}
	for _, value := range graph.ServiceContracts {
		if !hasService(current, value.ServiceID) || contracts[value.ServiceID].ServiceID != "" {
			return Failure("INVALID_SERVICE_CONTRACT", "服务契约归属无效或重复")
		}
		_, reference, err := definitions.ValidateServiceDocument(value.Document)
		if err != nil {
			return err
		}
		if reference != value.Reference {
			return Failure("INVALID_SERVICE_CONTRACT", "服务契约内容与固定版本摘要不匹配")
		}
		contracts[value.ServiceID] = value
	}
	for _, node := range graph.Middlewares {
		if _, _, err := definitions.Normalize(node.Definition, node.Configuration); err != nil {
			return err
		}
	}
	return validateBindings(graph, contracts, definitions.List())
}

// validateBindings 对同一最终集合统计身份、库名、端点和连接基数。
func validateBindings(graph GraphState, contracts map[string]ServiceContract, catalog DefinitionCatalog) error {
	identities, databases, credentials := map[string]bool{}, map[string]bool{}, map[string]bool{}
	counts := map[string]int{}
	for _, binding := range graph.Bindings {
		if identities[binding.ID] || databases[binding.Target.ComponentID+"/"+binding.DatabaseName] || credentials[binding.Credential.ID] {
			return Failure("DUPLICATE_BINDING", "依赖身份、目标库或凭据引用重复；请修改原绑定或使用独立数据库名")
		}
		identities[binding.ID], databases[binding.Target.ComponentID+"/"+binding.DatabaseName], credentials[binding.Credential.ID] = true, true, true
		source, target, err := validateBinding(binding, graph, contracts, catalog)
		if err != nil {
			return err
		}
		for _, endpoint := range []struct{ key, cardinality string }{{binding.Source.ComponentID + "/" + binding.Source.PortID, source.Cardinality}, {binding.Target.ComponentID + "/" + binding.Target.PortID, target.Cardinality}} {
			counts[endpoint.key]++
			if endpoint.cardinality == "one" && counts[endpoint.key] > 1 {
				return Failure("PORT_CARDINALITY", "具名端口超过允许的连接数量")
			}
		}
	}
	return nil
}

// validateBinding 只开放消费服务到标准 MySQL 的明确兼容组合。
func validateBinding(binding DatabaseBinding, graph GraphState, contracts map[string]ServiceContract, catalog DefinitionCatalog) (ServicePort, PortDefinition, error) {
	var empty ServicePort
	var provider PortDefinition
	if !ValidID(binding.ID) || !ValidID(binding.DatabaseID) || !ValidID(binding.Credential.ID) || !credentialVersion.MatchString(binding.Credential.Version) || !databaseName.MatchString(binding.DatabaseName) || (binding.Purpose != "readWrite" && binding.Purpose != "readOnly") {
		return empty, provider, Failure("INVALID_BINDING", "数据库依赖身份、用途或名称无效")
	}
	if binding.Kind != "databaseDependency" || binding.Protocol != "mysql" || binding.Contract != "mysql-wire/10" {
		return empty, provider, Failure("INCOMPATIBLE_PORT", "仅支持已发布的 MySQL 数据库依赖协议")
	}
	contract, exists := contracts[binding.Source.ComponentID]
	if !exists {
		return empty, provider, Failure("SERVICE_CONTRACT_REQUIRED", "消费服务尚未登记有效端口契约，或不属于此项目")
	}
	source, exists := findServicePort(contract, binding.Source.PortID)
	if !exists {
		return empty, provider, Failure("PORT_NOT_FOUND", "服务消费端口不存在")
	}
	node, exists := middlewareNode(graph, binding.Target.ComponentID)
	if !exists {
		return empty, provider, Failure("INVALID_BINDING", "提供中间件不存在或不属于此项目")
	}
	provider, exists = findDefinitionPort(catalog, node.Definition, binding.Target.PortID)
	if !exists {
		return empty, provider, Failure("PORT_NOT_FOUND", "中间件提供端口不存在")
	}
	if err := validateEndpointCompatibility(binding, source, provider, contract.Reference, node.Definition); err != nil {
		return empty, provider, err
	}
	return source, provider, nil
}

// validateEndpointCompatibility 固定角色、协议、契约与两端定义版本，未知组合不会隐式转接。
func validateEndpointCompatibility(binding DatabaseBinding, source ServicePort, target PortDefinition, sourceDefinition, targetDefinition DefinitionRef) error {
	if source.Role != "consume" || target.Role != "provide" {
		return Failure("INVALID_PORT_ROLE", "数据库依赖必须从消费端口指向提供端口")
	}
	if source.Protocol != binding.Protocol || target.Protocol != binding.Protocol || source.Contract != binding.Contract || target.Contract != binding.Contract {
		return Failure("INCOMPATIBLE_PORT", "两端协议或接口版本不兼容")
	}
	if sourceDefinition != binding.SourceDefinition || targetDefinition != binding.TargetDefinition {
		return Failure("DEFINITION_CONFLICT", "两端定义已变化，请明确重连或在同一次编辑中更新关联")
	}
	return nil
}

// findServicePort 只查询本服务固定包内的语义 ID，不以端口显示次序解析。
func findServicePort(value ServiceContract, id string) (ServicePort, bool) {
	for _, port := range value.Document.Ports {
		if port.ID == id {
			return port, true
		}
	}
	return ServicePort{}, false
}

// findDefinitionPort 要求实例引用与目录完全一致，不采用最新版本兜底。
func findDefinitionPort(catalog DefinitionCatalog, reference DefinitionRef, id string) (PortDefinition, bool) {
	for _, definition := range catalog.Definitions {
		if definition.Reference != reference {
			continue
		}
		for _, port := range definition.Ports {
			if port.ID == id {
				return port, true
			}
		}
	}
	return PortDefinition{}, false
}
