// 本文件统一拥有HTTP与持久工作器生命周期，退出控制面不会停止已运行的业务容器。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// listenAndServe 只有本机监听成功才启动后台工作器，所有goroutine都在返回前收回。
func listenAndServe(ctx context.Context, address string, handler http.Handler, worker backgroundWorker, logger *slog.Logger) (err error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("本机监听失败，请检查端口是否占用")
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- serveHTTP(server, listener) }()
	var workerStopped chan error
	if worker != nil {
		workerStopped = make(chan error, 1)
		go func() { workerStopped <- worker.Run(ctx) }()
	}
	logger.Info("平台已就绪", "address", address)
	serverFinished, workerFinished := false, false
	select {
	case result := <-stopped:
		serverFinished = true
		if !errors.Is(result, http.ErrServerClosed) {
			err = fmt.Errorf("本机服务异常停止")
		}
	case result := <-workerStopped:
		workerFinished = true
		if result != nil {
			err = result
		} else if ctx.Err() == nil {
			err = fmt.Errorf("运行工作器意外退出，需核实持久操作")
		}
	case <-ctx.Done():
	}
	cancel()
	shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	if shutdownErr := server.Shutdown(shutdown); shutdownErr != nil {
		err = errors.Join(err, fmt.Errorf("本机HTTP未能正常退出"), server.Close())
	}
	if !serverFinished {
		if result := <-stopped; !errors.Is(result, http.ErrServerClosed) {
			err = errors.Join(err, fmt.Errorf("本机服务退出失败"))
		}
	}
	if worker != nil && !workerFinished {
		err = errors.Join(err, <-workerStopped)
	}
	return err
}

// serveHTTP 将服务goroutine中的panic转换为安全错误，保证拥有者能够收回。
func serveHTTP(server *http.Server, listener net.Listener) (err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("HTTP服务内部异常")
		}
	}()
	return server.Serve(listener)
}
