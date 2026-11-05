// 本文件负责 HTTP 进程生命周期；停止服务不会删除数据库或调用迁移。
package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Serve 等待显式取消，拥有并回收唯一的 HTTP goroutine。
func Serve(ctx context.Context, listener net.Listener, handler *Handler) error {
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16 * 1024}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("HTTP 服务异常停止")
		}
		return nil
	case <-ctx.Done():
		handler.BeginShutdown()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			return errors.Join(fmt.Errorf("HTTP 服务未能在期限内排空"), server.Close(), <-done)
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("HTTP 服务停止未能核实")
		}
		return nil
	}
}
