// 本文件验证阶段取消会中断阻塞管理通道，并禁止在未知会话里继续写入。
package mysqlruntime

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

// blockedSQLTransport 不制造SQL结果，只阻塞到拥有者中断；sync.Once归还关闭所有权。
type blockedSQLTransport struct {
	reader       *io.PipeReader
	writer       *io.PipeWriter
	once         sync.Once
	interruptErr error
	writes       int
}

// Read 等待当前协议响应，不替取消调用者提前生成成功。
func (value *blockedSQLTransport) Read(data []byte) (int, error) { return value.reader.Read(data) }

// Write 记录是否发过请求，结果由受控取消保持未知。
func (value *blockedSQLTransport) Write(data []byte) (int, error) {
	value.writes++
	return len(data), nil
}

// Interrupt 可以与阻塞读取并发；只关闭管道，不改动串行会话字段。
func (value *blockedSQLTransport) Interrupt() {
	value.once.Do(func() { value.interruptErr = value.writer.CloseWithError(context.Canceled) })
}

// Close 等待原中断归还后再释放读取端，保留真正关闭失败。
func (value *blockedSQLTransport) Close() error {
	value.Interrupt()
	return errors.Join(value.interruptErr, value.reader.Close())
}

// TestSQLQueryPhaseCancellation 阶段期限不必等待整个部署期限，后续写入不能换成成功。
func TestSQLQueryPhaseCancellation(t *testing.T) {
	for _, before := range []bool{false, true} {
		t.Run(map[bool]string{false: "阻塞中取消", true: "发送前取消"}[before], func(t *testing.T) {
			assertSQLPhaseCancellation(t, before)
		})
	}
}

// assertSQLPhaseCancellation 每种取消时机独立拥有管道，超时与后续禁写必须同时成立。
func assertSQLPhaseCancellation(t *testing.T, before bool) {
	t.Helper()
	reader, writer := io.Pipe()
	transport := &blockedSQLTransport{reader: reader, writer: writer}
	session, err := newSQLSession(transport)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if before {
		cancel()
	}
	started := time.Now()
	_, err = session.query(ctx, "SELECT 1")
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "DATABASE_RESULT_UNKNOWN" || !fault.Unknown || time.Since(started) > time.Second {
		t.Fatal("阶段取消未中断原通道或丢失未知结果", err)
	}
	writes := 1
	if before {
		writes = 0
	}
	if _, err = session.query(context.Background(), "CREATE DATABASE controlled"); err == nil || transport.writes != writes {
		t.Fatal("取消后再次发出写入")
	}
	if err = session.Close(); err == nil {
		t.Fatal("关闭隐藏取消的原结果")
	}
}
