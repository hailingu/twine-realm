// 本文件验证实际命令路由与输出错误，不依赖终端交互。
package main

import (
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"testing"
)

// brokenOutput 用来证明写入失败不会被当成命令成功。
type brokenOutput struct{}

// Write 始终报告输出失联，供调用者验证错误传播。
func (brokenOutput) Write([]byte) (int, error) { return 0, errors.New("测试输出已关闭") }

// TestProfileCommands 实际导出 / 核实使用同一离线目录，其他命令明确失败。
func TestProfileCommands(t *testing.T) {
	var output bytes.Buffer
	if err := run([]string{"inspect"}, &output); err != nil || !bytes.Contains(output.Bytes(), []byte("go-net-http-mysql")) {
		t.Fatalf("无法读取工程方案：%v", err)
	}
	directory := filepath.Join(t.TempDir(), "service")
	if err := run([]string{"export", "--directory", directory}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"validate", "--directory", directory}, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{nil, {"inspect", "extra"}, {"shell"}, {"export"}, {"validate", "--other", "bad"}, {"validate", "--directory", directory, "extra"}, {"validate", "--directory", filepath.Join(t.TempDir(), "missing")}, {"export", "--directory", directory}} {
		if err := run(args, io.Discard); err == nil {
			t.Fatalf("接受未声明命令：%v", args)
		}
	}
	if err := run([]string{"inspect"}, brokenOutput{}); err == nil {
		t.Fatal("吞掉输出错误")
	}
}
