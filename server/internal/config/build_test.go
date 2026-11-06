// 本文件验证隔离构建能力必须完整显式启用，不因部分配置静默降级。
package config

import "testing"

// TestExplicitBuildConfiguration 未配置时只保留画布；未知适配器与缺失根均阻止启动。
func TestExplicitBuildConfiguration(t *testing.T) {
	t.Setenv("TWINE_DEPLOY_ADAPTER", "")
	t.Setenv("TWINE_DATABASE_DSN", "受控配置")
	t.Setenv("TWINE_REPOSITORY_ROOTS", `["/tmp/repos"]`)
	t.Setenv("TWINE_ADDRESS", "127.0.0.1:8080")
	t.Setenv("TWINE_DEV_ORIGIN", "")
	for _, value := range []struct {
		adapter, directory string
		valid              bool
	}{{"", "", true}, {"local-go-container-v1", "/tmp/private-build", true}, {"local-go-container-v1", "", false}, {"", "/tmp/private-build", false}, {"automatic", "/tmp/private-build", false}} {
		t.Setenv("TWINE_BUILD_ADAPTER", value.adapter)
		t.Setenv("TWINE_BUILD_DIRECTORY", value.directory)
		configuration, err := Load()
		if (err == nil) != value.valid {
			t.Fatalf("构建配置结果不符：%v", err)
		}
		if err == nil && (configuration.BuildAdapter != value.adapter || configuration.BuildDirectory != value.directory) {
			t.Fatal("显式构建配置被改写")
		}
	}
}
