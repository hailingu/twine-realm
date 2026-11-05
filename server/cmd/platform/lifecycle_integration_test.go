//go:build integration

// 本文件用真实回环监听验证默认无工作器退出、端口占用及服务异常时的监听所有权。
package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
)

// TestDisabledRuntimeHTTPShutdown 不启用工作器或启动时已经取消也必须收回HTTP监听和goroutine。
func TestDisabledRuntimeHTTPShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := listenAndServe(ctx, "127.0.0.1:0", http.NewServeMux(), nil, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
}

// TestPlatformPortConflictIsExplicit 端口占用时不开启后台执行，也不偷偷更换端口或连接已有服务。
func TestPlatformPortConflictIsExplicit(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := listenAndServe(context.Background(), listener.Addr().String(), http.NewServeMux(), nil, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("端口冲突仍宣称平台已启动")
	}
}

// panicListener 只在本测试的Accept边界制造异常，Close仍释放真实测试监听。
type panicListener struct {
	net.Listener
	closed bool
}

// Accept 模拟监听实现异常，不在新goroutine遗留不可观测panic。
func (*panicListener) Accept() (net.Conn, error) { panic("受控监听异常") }

// Close 将资源归还给真实监听，并记录所有者已处理退出。
func (listener *panicListener) Close() error {
	listener.closed = true
	return listener.Listener.Close()
}

// TestServePanicReturnsSafeError 异常被转换为安全错误，HTTP的监听所有权仍完整释放。
func TestServePanicReturnsSafeError(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := &panicListener{Listener: base}
	t.Cleanup(func() {
		if !listener.closed {
			if err := base.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	if err := serveHTTP(&http.Server{Handler: http.NewServeMux()}, listener); err == nil || !listener.closed {
		t.Fatal("服务异常未释放监听或被当成成功")
	}
}
