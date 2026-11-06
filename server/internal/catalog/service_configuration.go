// 本文件复用服务自己的离线schema裁决运行配置，不将标准组件schema套给业务服务。
package catalog

import (
	"encoding/json"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// ValidateServiceConfiguration 先验证完整契约，再校验显式配置；不隐式补齐未映射的业务参数。
func (catalog *Catalog) ValidateServiceConfiguration(document project.ServiceContractDocument, configuration map[string]any) error {
	if _, _, err := catalog.ValidateServiceDocument(document); err != nil {
		return err
	}
	if configuration == nil {
		return project.Failure("INVALID_CONFIGURATION", "业务配置必须为明确对象")
	}
	data, err := json.Marshal(document.ConfigurationSchema)
	if err != nil {
		return invalidServiceContract()
	}
	schema, err := compileConfiguration(data)
	if err != nil {
		return err
	}
	if err := schema.Validate(configuration); err != nil {
		return project.Failure("INVALID_CONFIGURATION", "业务配置不满足此服务的固定schema，请核实必填值和支持范围")
	}
	return nil
}
