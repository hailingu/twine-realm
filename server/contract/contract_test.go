// 本文件确保发布的共享契约能被服务端实际编译。
package contract

import "testing"

// TestContractCompilation 防止仅前端可用的表达式进入发布包。
func TestContractCompilation(t *testing.T) {
	t.Run("共享契约可编译", func(t *testing.T) {
		if _, err := Load(); err != nil {
			t.Fatal(err)
		}
	})
}
