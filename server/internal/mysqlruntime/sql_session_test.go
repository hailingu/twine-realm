// 本文件验证管理协议边界与安全错误；真实Docker进程和MySQL集成另行验收。
package mysqlruntime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// sqlFixtureTransport 只模拟已分帧的客户端字节，不假装产生真实SQL结果。
type sqlFixtureTransport struct {
	output bytes.Buffer
	mode   string
	writes int
	closed bool
}

// Interrupt 的普通同步夹具无阻塞I/O；取消阻塞另由管道夹具验证。
func (value *sqlFixtureTransport) Interrupt() {}

// Read 提供当前受控结果，下一请求前不产生额外成功数据。
func (value *sqlFixtureTransport) Read(data []byte) (int, error) { return value.output.Read(data) }

// Write 从本次请求提取随机边界，以覆盖错序、截断、告警和连续请求。
func (value *sqlFixtureTransport) Write(data []byte) (int, error) {
	value.writes++
	lines := strings.Split(string(data), "\n")
	start := strings.TrimSuffix(strings.TrimPrefix(lines[0], "SELECT '"), "';")
	end := strings.TrimSuffix(strings.TrimPrefix(lines[len(lines)-2], "SELECT '"), "';")
	rows := []string{start, `{"value":1}`, "0", end}
	switch value.mode {
	case "wrong-start":
		rows[0] = "foreign"
	case "truncated":
		rows = rows[:len(rows)-1]
	case "warning":
		rows[2] = "1"
	case "bad-warning":
		rows[2] = "not-a-count"
	case "oversized":
		rows[1] = strings.Repeat("x", 128*1024+1)
	case "missing-warning":
		rows = []string{start, end}
	case "too-many-rows":
		rows = append([]string{start}, append(make([]string, 1025), "0", end)...)
	case "short-write":
		return len(data) - 1, nil
	case "write-failure":
		return 0, errors.New("synthetic-private-write")
	}
	if _, err := value.output.WriteString(strings.Join(rows, "\n") + "\n"); err != nil {
		return 0, err
	}
	return len(data), nil
}

// Close 表达原始故障可能含秘密，生产协议必须转换而不是直接返回它。
func (value *sqlFixtureTransport) Close() error {
	value.closed = true
	if value.mode == "close-failure" {
		return errors.New("synthetic-private-value")
	}
	return nil
}

// TestSQLSessionFraming 连续请求保持各自边界，关闭始终回收原传输。
func TestSQLSessionFraming(t *testing.T) {
	transport := &sqlFixtureTransport{}
	session, err := newSQLSession(transport)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		rows, err := session.query(context.Background(), "SELECT JSON_OBJECT('value',1)")
		if err != nil || len(rows) != 1 || rows[0] != `{"value":1}` {
			t.Fatal("当前请求结果边界丢失", err)
		}
	}
	if err := session.Close(); err != nil || !transport.closed {
		t.Fatal("会话未回收", err)
	}
}

// TestSQLSessionFaultsStopWrites 错序、截断、超限和告警均保留原失败，后续不再发SQL。
func TestSQLSessionFaultsStopWrites(t *testing.T) {
	for _, mode := range []string{"wrong-start", "truncated", "warning", "bad-warning", "oversized", "missing-warning", "too-many-rows", "short-write", "write-failure"} {
		t.Run(mode, func(t *testing.T) {
			assertSQLFaultStopsWrites(t, mode)
		})
	}
}

// assertSQLFaultStopsWrites 同时核实安全诊断、停止后续写入和原传输回收。
func assertSQLFaultStopsWrites(t *testing.T, mode string) {
	t.Helper()
	transport := &sqlFixtureTransport{mode: mode}
	session, err := newSQLSession(transport)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.query(context.Background(), "SELECT 1"); err == nil || strings.Contains(err.Error(), "synthetic-private") {
		t.Fatal("未确认响应被接受")
	}
	if _, err := session.query(context.Background(), "CREATE DATABASE controlled"); err == nil || transport.writes != 1 {
		t.Fatal("失败后继续发送写入")
	}
	if session.Close() == nil || !transport.closed {
		t.Fatal("关闭丢失原失败或未回收")
	}
}

// TestSQLSessionRequestQuotaHasNoSuccessorWrite 会话达到固定请求上限后进入失败，不能继续发送潜在DDL。
func TestSQLSessionRequestQuotaHasNoSuccessorWrite(t *testing.T) {
	transport := &sqlFixtureTransport{}
	session, err := newSQLSession(transport)
	if err != nil {
		t.Fatal(err)
	}
	for range 1000 {
		if _, err := session.query(context.Background(), "SELECT 1"); err != nil {
			t.Fatal("配额内请求未完成", err)
		}
	}
	for range 2 {
		if _, err := session.query(context.Background(), "CREATE DATABASE controlled"); err == nil || transport.writes != 1000 {
			t.Fatal("配额耗尽后仍执行SQL")
		}
	}
	if err := session.Close(); err == nil || !transport.closed {
		t.Fatal("配额失败未保留或传输未回收")
	}
}

// TestSQLSessionInputAndPrivateErrors 无效管理请求不发送，关闭故障不回显敏感传输原文。
func TestSQLSessionInputAndPrivateErrors(t *testing.T) {
	for _, sql := range []string{"", "SELECT 1; SELECT 2", "SELECT\x00", strings.Repeat("x", 8193)} {
		transport := &sqlFixtureTransport{}
		session, err := newSQLSession(transport)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := session.query(context.Background(), sql); err == nil || transport.writes != 0 {
			t.Fatal("非法请求被发送")
		}
		if session.Close() == nil {
			t.Fatal("关闭隐藏此前失败")
		}
	}
	session, err := newSQLSession(&sqlFixtureTransport{mode: "close-failure"})
	if err != nil {
		t.Fatal(err)
	}
	err = session.Close()
	if err == nil || strings.Contains(err.Error(), "synthetic-private-value") || errors.Is(err, io.EOF) {
		t.Fatal("原始传输错误进入诊断")
	}
}
