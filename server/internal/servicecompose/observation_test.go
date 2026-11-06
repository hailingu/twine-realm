// 本文件通过真实本机HTTP验证就绪响应的传输和内容边界，不用状态码200单独证明就绪。
package servicecompose

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestReadinessProbeResponseBoundary 只接受固定单字段响应，拒绝重定向、错误、截断和无界正文。
func TestReadinessProbeResponseBoundary(t *testing.T) {
	var followed atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		followed.Add(1)
		if _, err := io.WriteString(w, `{"status":"ready"}`); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(destination.Close)
	for _, test := range []struct {
		name, body string
		status     int
		ready      bool
	}{
		{"就绪", `{"status":"ready"}`, 200, true}, {"不可用", `{"status":"ready"}`, 503, false},
		{"重定向", `{"status":"ready"}`, 302, false}, {"非法JSON", `{`, 200, false},
		{"额外字段", `{"status":"ready","other":"value"}`, 200, false}, {"字段类型错误", `{"status":42}`, 200, false},
		{"正文过大", strings.Repeat("x", 1025), 200, false}, {"空结果", `null`, 200, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", destination.URL)
				w.WriteHeader(test.status)
				if _, err := io.WriteString(w, test.body); err != nil {
					t.Error(err)
				}
			}))
			t.Cleanup(server.Close)
			if probe(context.Background(), server.URL+"/readyz", "ready") != test.ready {
				t.Fatal("响应边界未被严格核实")
			}
		})
	}
	if followed.Load() != 0 {
		t.Fatal("只读探针跟随了重定向")
	}
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { truncatedProbeResponse(t, w, r) }))
	t.Cleanup(broken.Close)
	if probe(context.Background(), broken.URL, "ready") {
		t.Fatal("截断正文被当成就绪")
	}
}

// truncatedProbeResponse 制造实际协议截断，只写固定测试字节，不将网络错误隐藏。
func truncatedProbeResponse(t *testing.T, w http.ResponseWriter, _ *http.Request) {
	t.Helper()
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		t.Error("测试HTTP通道不支持受控截断")
		return
	}
	connection, buffered, err := hijacker.Hijack()
	if err != nil {
		t.Error("测试截断通道未建立", err)
		return
	}
	_, writeErr := buffered.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 99\r\nConnection: close\r\n\r\n{")
	flushErr := buffered.Flush()
	closeErr := connection.Close()
	if err := errors.Join(writeErr, flushErr, closeErr); err != nil {
		t.Error("测试截断通道写入或回收失败", err)
	}
}
