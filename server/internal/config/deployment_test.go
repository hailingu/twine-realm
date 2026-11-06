// 本文件证明业务部署独立显式启用，不能用部分配置静默开放账号与容器副作用。
package config

import "testing"

// TestExplicitDeploymentConfiguration 两个原适配器与新组合必须完整匹配，默认仍无业务部署。
func TestExplicitDeploymentConfiguration(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		value Config
		valid bool
	}{
		{"disabled", Config{}, true},
		{"complete", Config{DeployAdapter: "go-mysql-compose-v1", RuntimeAdapter: "file-secrets-v1", RuntimeDirectory: "/private/runtime", BuildAdapter: "local-go-container-v1", BuildDirectory: "/private/build"}, true},
		{"unknown", Config{DeployAdapter: "automatic"}, false},
		{"missing-runtime", Config{DeployAdapter: "go-mysql-compose-v1", BuildAdapter: "local-go-container-v1", BuildDirectory: "/private/build"}, false},
		{"missing-build", Config{DeployAdapter: "go-mysql-compose-v1", RuntimeAdapter: "file-secrets-v1", RuntimeDirectory: "/private/runtime"}, false},
		{"missing-directory", Config{DeployAdapter: "go-mysql-compose-v1", RuntimeAdapter: "file-secrets-v1", BuildAdapter: "local-go-container-v1"}, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if err := validateAdapters(scenario.value); (err == nil) != scenario.valid {
				t.Fatal("业务部署显式边界不符合预期", err)
			}
		})
	}
}
