// 本文件验证一期启动配置不隐式开放外部网络或全盘仓库访问。
package config

import "testing"

// TestLocalConfiguration 覆盖合法配置与宽泛监听、来源及缺失注入。
func TestLocalConfiguration(t *testing.T) {
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
