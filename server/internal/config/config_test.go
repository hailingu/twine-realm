// 本文件验证一期启动配置不隐式开放外部网络或全盘仓库访问。
package config

import "testing"

// TestLocalConfiguration 覆盖合法配置与宽泛监听、来源及缺失注入。
func TestLocalConfiguration(t *testing.T) {
	t.Setenv("TWINE_DEPLOY_ADAPTER", "")
	t.Run("本机配置边界", func(t *testing.T) {
		for _, test := range []struct {
			name, address, origin, roots, dsn string
			valid                             bool
		}{
			{"本机默认", "", "", `["/tmp/repos"]`, "环境秘密", true},
			{"开发代理", "127.0.0.1:8080", "http://127.0.0.1:5173", `["/tmp/repos"]`, "环境秘密", true},
			{"公开监听", "0.0.0.0:8080", "", `["/tmp/repos"]`, "环境秘密", false},
			{"域名重绑定", "localhost:8080", "", `["/tmp/repos"]`, "环境秘密", false},
			{"外部来源", "", "https://evil.invalid", `["/tmp/repos"]`, "环境秘密", false},
			{"缺少目录", "", "", `[]`, "环境秘密", false},
			{"缺少数据库", "", "", `["/tmp/repos"]`, "", false},
		} {
			t.Run(test.name, func(t *testing.T) {
				t.Setenv("TWINE_ADDRESS", test.address)
				t.Setenv("TWINE_DEV_ORIGIN", test.origin)
				t.Setenv("TWINE_REPOSITORY_ROOTS", test.roots)
				t.Setenv("TWINE_DATABASE_DSN", test.dsn)
				_, err := Load()
				if (err == nil) != test.valid {
					t.Fatalf("配置结果 %v", err)
				}
			})
		}
	})
}

// TestExplicitRuntimeConfiguration 运行能力必须由完整组合显式启用，缺省和部分配置不发生静默降级。
func TestExplicitRuntimeConfiguration(t *testing.T) {
	t.Setenv("TWINE_DEPLOY_ADAPTER", "")
	t.Setenv("TWINE_ADDRESS", "127.0.0.1:8080")
	t.Setenv("TWINE_DATABASE_DSN", "受控注入")
	t.Setenv("TWINE_REPOSITORY_ROOTS", `["/tmp/repos"]`)
	t.Setenv("TWINE_DEV_ORIGIN", "")
	for _, scenario := range []struct {
		adapter, directory string
		valid              bool
	}{
		{"", "", true}, {"file-secrets-v1", "/tmp/private-runtime", true}, {"file-secrets-v1", "", false}, {"", "/tmp/private-runtime", false}, {"unknown-adapter", "/tmp/private-runtime", false},
	} {
		t.Run(scenario.adapter+scenario.directory, func(t *testing.T) {
			t.Setenv("TWINE_RUNTIME_ADAPTER", scenario.adapter)
			t.Setenv("TWINE_RUNTIME_DIRECTORY", scenario.directory)
			value, err := Load()
			if (err == nil) != scenario.valid {
				t.Fatal("显式运行组合校验不符合边界")
			}
			if err == nil && (value.RuntimeAdapter != scenario.adapter || value.RuntimeDirectory != scenario.directory) {
				t.Fatal("运行配置被隐式替换")
			}
		})
	}
}
