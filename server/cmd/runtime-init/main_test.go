// 本文件检查初始化命令协议，真实初始化由完整平台检查执行。
package main

import (
	"context"
	"errors"
	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"io"
	"testing"
)

// brokenWriter 确认CLI输出失败不会被伪装为成功。
type brokenWriter struct{}

// Write 模拟输出连接中断，不包含秘密或路径。
func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("受控输出中断") }

// TestInitializationProtocol 非法输入在Docker访问前失败，响应写入保持非零。
func TestInitializationProtocol(t *testing.T) {
	for _, args := range [][]string{nil, {"/tmp/new"}, {"--directory", "relative"}, {"--directory", "/tmp/new", "extra"}} {
		if err := run(context.Background(), args, io.Discard); err == nil {
			t.Fatal("非法参数被接受")
		}
	}
	for _, raw := range []string{"", "null", "[]", "[\"relative\"]", "{}"} {
		if _, err := repositoryRoots(raw); err == nil {
			t.Fatal("无效仓库根被接受")
		}
	}
	if _, err := repositoryRoots(`["/tmp/repos"]`); err != nil {
		t.Fatal(err)
	}
	if err := writeReport(brokenWriter{}, "non-sensitive-installation", mysqlruntime.Environment{}); err == nil {
		t.Fatal("输出错误被忽略")
	}
}
