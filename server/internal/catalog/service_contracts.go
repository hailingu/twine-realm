// 本文件校验服务自有离线契约包，禁止把自定义 schema 当作任意文件或网络读取权限。
package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"

	"github.com/hailingu/twine-realm/server/internal/project"
)

var contractID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]{0,79}$`)
var contractVersion = regexp.MustCompile(`^[0-9]{1,6}\.[0-9]{1,6}\.[0-9]{1,6}$`)
var protocolID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
var mysqlWire = regexp.MustCompile(`^mysql-wire/[1-9][0-9]{0,2}$`)

// ValidateServiceDocument 固定内容摘要，配置和多个接口版本分别编译，不假设单一业务 schema。
func (catalog *Catalog) ValidateServiceDocument(document project.ServiceContractDocument) (project.ServiceContractDocument, project.DefinitionRef, error) {
	if document.Interfaces == nil {
		document.Interfaces = []project.ServiceInterface{}
	}
	if document.Ports == nil {
		document.Ports = []project.ServicePort{}
	}
	if !contractID.MatchString(document.ID) || !contractVersion.MatchString(document.Version) || len(document.Interfaces) > 20 || len(document.Ports) > 20 {
		return document, project.DefinitionRef{}, invalidServiceContract()
	}
	data, err := json.Marshal(document)
	if err != nil || len(data) > 32768 {
		return document, project.DefinitionRef{}, project.Failure("INVALID_SERVICE_CONTRACT", "服务契约必须为不超过 32 KiB 的有界 JSON 包")
	}
	if err := validateInlineSchema(document.ConfigurationSchema); err != nil {
		return document, project.DefinitionRef{}, err
	}
	interfaces, err := validateInterfaces(document.Interfaces)
	if err != nil {
		return document, project.DefinitionRef{}, err
	}
	if err := validateServicePorts(document.Ports, interfaces); err != nil {
		return document, project.DefinitionRef{}, err
	}
	hash := sha256.Sum256(data)
	return document, project.DefinitionRef{ID: document.ID, Version: document.Version, Digest: "sha256:" + hex.EncodeToString(hash[:])}, nil
}

// validateInterfaces 校验每一份接口版本，重复标识不能制造含糊的端口解析。
func validateInterfaces(interfaces []project.ServiceInterface) (map[string]string, error) {
	result := map[string]string{}
	for _, item := range interfaces {
		key := item.ID + "@" + item.Version
		if !contractID.MatchString(item.ID) || !contractVersion.MatchString(item.Version) || !protocolID.MatchString(item.Protocol) || result[key] != "" {
			return nil, invalidServiceContract()
		}
		if err := validateInlineSchema(item.Schema); err != nil {
			return nil, err
		}
		result[key] = item.Protocol
	}
	return result, nil
}

// validateServicePorts 只认可包内接口或显式 MySQL 协议系列，不猜测任意 JSON 兼容。
func validateServicePorts(ports []project.ServicePort, interfaces map[string]string) error {
	seen := map[string]bool{}
	for _, port := range ports {
		if seen[port.ID] {
			return invalidServiceContract()
		}
		seen[port.ID] = true
		if err := validateServicePort(port, interfaces); err != nil {
			return err
		}
	}
	return nil
}

// validateServicePort 校验单个端口身份、方向与它在本包中引用的协议。
func validateServicePort(port project.ServicePort, interfaces map[string]string) error {
	if !contractID.MatchString(port.ID) || !protocolID.MatchString(port.Protocol) {
		return invalidServiceContract()
	}
	if port.Role != "consume" && port.Role != "provide" {
		return invalidServiceContract()
	}
	if port.Cardinality != "one" && port.Cardinality != "many" {
		return invalidServiceContract()
	}
	if port.Role == "provide" && port.Required {
		return project.Failure("INVALID_SERVICE_CONTRACT", "必需绑定仅适用于消费端口")
	}
	if port.Protocol == "mysql" && mysqlWire.MatchString(port.Contract) {
		return nil
	}
	if interfaces[port.Contract] != port.Protocol {
		return project.Failure("INVALID_SERVICE_CONTRACT", "端口引用的协议 / 接口版本不在此契约包中")
	}
	return nil
}

// validateInlineSchema 固定方言、规模和仅本地片段引用，实际编译仍由原 JSON Schema 库完成。
func validateInlineSchema(schema map[string]any) error {
	if schema == nil {
		return project.Failure("INVALID_SERVICE_SCHEMA", "每份 schema 必须是明确的 JSON 对象")
	}
	count := 0
	if err := checkSchemaTree(schema, 0, &count); err != nil {
		return err
	}
	if err := checkInlineReferences(schema); err != nil {
		return err
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return invalidServiceContract()
	}
	if _, err := compileConfiguration(data); err != nil {
		return project.Failure("INVALID_SERVICE_SCHEMA", "服务 schema 无效或包含不受支持的引用")
	}
	return nil
}

// checkSchemaTree 限制嵌套和引用边界，编译器不会按输入去读网络或宿主文件。
func checkSchemaTree(value any, depth int, count *int) error {
	*count = *count + 1
	if depth > 20 || *count > 2048 {
		return project.Failure("INVALID_SERVICE_SCHEMA", "服务 schema 超出深度或节点数量限制")
	}
	switch current := value.(type) {
	case map[string]any:
		for _, nested := range current {
			if err := checkSchemaTree(nested, depth+1, count); err != nil {
				return err
			}
		}
	case []any:
		for _, nested := range current {
			if err := checkSchemaTree(nested, depth+1, count); err != nil {
				return err
			}
		}
	}
	return nil
}

// invalidServiceContract 返回安全的结构说明，不回显自定义 schema、路径或示例数据。
func invalidServiceContract() error {
	return project.Failure("INVALID_SERVICE_CONTRACT", "服务契约标识、版本、接口或端口声明无效")
}
