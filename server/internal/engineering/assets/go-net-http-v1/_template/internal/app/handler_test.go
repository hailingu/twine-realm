// 本文件验证探针语义、响应边界与真实 HTTP 排空，不把数据库失败当进程死亡。
package app

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// backend 是端口替身，真实数据库与进程在 integration 用例中另验。
type backend struct {
	unavailable     bool
	identityFailure bool
	entered         chan struct{}
	release         chan struct{}
}

// Ready 尊重请求取消，错误文本只用于验证不会泄露到响应。
func (value backend) Ready(ctx context.Context) error {
	if ctx.Err() != nil || value.unavailable {
		return errors.New("private-database-detail")
	}
	return nil
}

// Identity 可在排空测试中明确挂住在途请求，而非用睡眠猜测时序。
func (value backend) Identity(context.Context) (time.Time, error) {
	if value.entered != nil {
		close(value.entered)
		<-value.release
	}
	if value.identityFailure {
		return time.Time{}, errors.New("private-database-detail")
	}
	return time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC), nil
}

// TestProbesAndResponses 检查存活 / 就绪分离、方法与安全错误，不依赖前端。
func TestProbesAndResponses(t *testing.T) {
	for _, scenario := range []struct {
		path    string
		method  string
		backend backend
		status  int
	}{
		{"/livez", "GET", backend{unavailable: true}, 200}, {"/readyz", "GET", backend{unavailable: true}, 503},
		{"/api/info", "GET", backend{unavailable: true}, 503}, {"/readyz", "GET", backend{}, 200},
		{"/api/info", "GET", backend{}, 200}, {"/api/info", "GET", backend{identityFailure: true}, 503},
		{"/", "GET", backend{}, 200}, {"/api/info", "POST", backend{}, 405}, {"/private", "GET", backend{}, 404},
	} {
		handler := New("测试服务", scenario.backend)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(scenario.method, scenario.path, nil))
		if recorder.Code != scenario.status || strings.Contains(recorder.Body.String(), "private-database-detail") {
			t.Fatalf("响应边界失效：%d %s", recorder.Code, recorder.Body.String())
		}
		if recorder.Header().Get("Cache-Control") != "no-store" || recorder.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("响应缺少安全边界")
		}
	}
	handler := New("测试服务", backend{})
	handler.BeginShutdown()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest("GET", "/readyz", nil))
	if recorder.Code != 503 {
		t.Fatal("排空期间仍宣称就绪")
	}
}

// TestGracefulShutdown 取消后在途响应仍完成，HTTP goroutine 被回收。
func TestGracefulShutdown(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	value := backend{entered: make(chan struct{}), release: make(chan struct{})}
	handler := New("排空服务", value)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, listener, handler) }()
	requestDone := make(chan error, 1)
	go func() {
		client := http.Client{Timeout: 5 * time.Second}
		response, err := client.Get("http://" + listener.Addr().String() + "/api/info")
		if err != nil {
			requestDone <- err
			return
		}
		_, readErr := io.Copy(io.Discard, response.Body)
		requestDone <- errors.Join(readErr, response.Body.Close())
	}()
	select {
	case <-value.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("在途请求未到达")
	}
	cancel()
	select {
	case err := <-done:
		t.Fatalf("未等待在途请求即退出：%v", err)
	default:
	}
	close(value.release)
	if err := <-requestDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP goroutine 未回收")
	}
}

// TestCanceledDependencyContext 已取消请求不能继续报告就绪。
func TestCanceledDependencyContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	recorder := httptest.NewRecorder()
	New("测试服务", backend{}).ServeHTTP(recorder, httptest.NewRequest("GET", "/readyz", nil).WithContext(ctx))
	if recorder.Code != 503 {
		t.Fatal("取消上下文仍继续依赖访问")
	}
}
