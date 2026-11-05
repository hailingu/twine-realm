// 本文件验证入口拒绝任意命令和不完整配置；真实运行由 integration 验证。
package main

import (
	"context"
	"io"
	"testing"
)

// TestCommandBoundary 没有参数、危险命令或缺少用途身份时不能进入执行。
func TestCommandBoundary(t *testing.T) {
	for _, args := range [][]string{nil, {"force"}, {"serve", "extra"}, {"serve"}, {"migrate"}, {"migration-status"}} {
		if err := run(context.Background(), args, func(string) string { return "" }, io.Discard); err == nil {
			t.Fatal("未拒绝非法命令或配置")
		}
	}
}
