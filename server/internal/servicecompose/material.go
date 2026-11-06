// 本文件只证明首次尚未产生业务资源；存在、改名、外来或无法查询的资源都不能作为缺席。
package servicecompose

import (
	"context"

	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// noDeploymentResources 在原项目文件锁内检查标签与确定名称，允许MySQL独立恢复前必须全部缺席。
func (driver *Driver) noDeploymentResources(ctx context.Context, plan servicedeploy.Plan) (bool, error) {
	for _, role := range []struct{ role, name string }{{"application", plan.Resources.Application}, {"migration", plan.Resources.Migration}} {
		found, err := driver.containerLocated(ctx, plan, role.role, role.name)
		if err != nil || found {
			return false, err
		}
	}
	data, err := driver.run(ctx, plan, "network", "ls", dockerFilter, "label=io.twine-realm.deploy.installation="+plan.Environment.RuntimeInstallationID, dockerFilter, "label=io.twine-realm.deploy.project="+plan.ProjectID, dockerFilter, "label=io.twine-realm.deploy.service="+plan.ServiceID, dockerFilter, "label=io.twine-realm.deploy.role=ingress", "--format", "{{json .}}")
	if err != nil {
		return false, err
	}
	owned, err := listedNames(data, "network")
	if err != nil {
		return false, err
	}
	if len(owned) > 1 || len(owned) == 1 && owned[0] != plan.Resources.IngressNetwork {
		return false, ownershipConflict()
	}
	network, err := driver.inspectIngress(ctx, plan)
	return len(owned) == 0 && network == nil && err == nil, err
}
