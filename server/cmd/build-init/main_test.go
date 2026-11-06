// 本文件验证显式初始化入口拒绝含糊路径和缺失范围，不触及用户已有目录。
package main

import (
	"context"
	"io"
	"testing"
)

// TestBuildInitializationArguments 在Docker前拒绝无效参数和缺失仓库范围。
func TestBuildInitializationArguments(t *testing.T) {
	t.Setenv("TWINE_REPOSITORY_ROOTS", "")
	for _, args := range [][]string{nil, {"--directory", "relative"}, {"--directory", "/tmp/example", "extra"}, {"--unknown"}, {"--directory", "/tmp/example"}} {
		if err := run(context.Background(), args, io.Discard); err == nil {
			t.Fatal("含糊初始化被放行")
		}
	}
}
