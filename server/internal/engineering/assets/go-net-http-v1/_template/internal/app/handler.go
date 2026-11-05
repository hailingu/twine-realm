// Package app 用标准库 HTTP 暴露最小只读示例与独立存活 / 就绪探针。
package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

// Backend 只提供当前请求需要的只读行为，不向 HTTP 层暴露 SQL 或秘密。
type Backend interface {
	Ready(context.Context) error
	Identity(context.Context) (time.Time, error)
}

// Handler 拥有探针的排空状态；服务名只以 JSON 数据输出。
type Handler struct {
	backend  Backend
	name     string
	mux      *http.ServeMux
	draining atomic.Bool
}

// New 注册显式方法与固定路径，不挂载工作区、日志、密码文件或调试路由。
func New(name string, backend Backend) *Handler {
	handler := &Handler{backend: backend, name: name, mux: http.NewServeMux()}
	handler.mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
	})
	handler.mux.HandleFunc("GET /readyz", handler.ready)
	handler.mux.HandleFunc("GET /api/info", handler.info)
	handler.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if _, err := w.Write([]byte("<!doctype html><html lang=zh><meta charset=utf-8><title>最小服务</title><h1>最小服务</h1><p><a href=/api/info>查看初始化示例</a></p></html>")); err != nil {
			slog.Debug("HTTP 页面传输未完成")
		}
	})
	return handler
}

// ServeHTTP 给所有响应提供最小安全头，业务 HTML 不与平台控制台同源。
func (handler *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; frame-ancestors 'none'")
	handler.mux.ServeHTTP(w, r)
}

// BeginShutdown 先撤销就绪，再由 Server.Shutdown 停止接收并排空。
func (handler *Handler) BeginShutdown() { handler.draining.Store(true) }

// available 将每次只读访问限制在两秒，并把未就绪与进程死亡分开。
func (handler *Handler) available(ctx context.Context) bool {
	return !handler.draining.Load() && handler.backend.Ready(ctx) == nil
}

// ready 不回显数据库原始诊断或密码，不因数据库故障改变存活状态。
func (handler *Handler) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if !handler.available(ctx) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "NOT_READY"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// info 只返回实际持久初始化时间；示例不绑定具体业务。
func (handler *Handler) info(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if !handler.available(ctx) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "NOT_READY"})
		return
	}
	initialized, err := handler.backend.Identity(ctx)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "DATA_UNAVAILABLE"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"service": handler.name, "initializedAt": initialized.UTC().Format(time.RFC3339Nano)})
}

// writeJSON 只编码固定可序列化类型；连接关闭的写入错误由 HTTP 连接生命周期处理。
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Debug("HTTP JSON 传输未完成")
		return
	}
}
