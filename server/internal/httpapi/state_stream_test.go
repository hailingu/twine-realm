// 本文件核实运输输入、限流及短写入期限；真实网络长连接另由集成测试覆盖。
package httpapi

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/projectstate"
)

// stateWriter 表达可控的HTTP运输失败，不模拟数据库或外部运行事实。
type stateWriter struct {
	bytes.Buffer
	deadlines []time.Time
	failure   string
}

// Header 只实现运输接口，不持有产品响应状态。
func (writer *stateWriter) Header() http.Header { return http.Header{} }

// WriteHeader 的状态码由真实HTTP测试核实，此替身仅验证流写入。
func (writer *stateWriter) WriteHeader(int) {}

// SetWriteDeadline 记录短期限与清零，并显式返回受控运输故障。
func (writer *stateWriter) SetWriteDeadline(at time.Time) error {
	writer.deadlines = append(writer.deadlines, at)
	if writer.failure == "deadline" {
		return errors.New("期限不可设置")
	}
	return nil
}

// Write 收集实际帧字节或拒绝本次写入。
func (writer *stateWriter) Write(data []byte) (int, error) {
	if writer.failure == "write" {
		return 0, errors.New("写入失败")
	}
	return writer.Buffer.Write(data)
}

// FlushError 将flush失败交还拥有者，不用空成功掩盖断开。
func (writer *stateWriter) FlushError() error {
	if writer.failure == "flush" {
		return errors.New("刷新失败")
	}
	return nil
}

// TestStateCursorTransport 不允许header/query混用时静默选择另一个游标。
func TestStateCursorTransport(t *testing.T) {
	for _, test := range []struct {
		path, header string
		valid        bool
	}{{"?after=0", "", true}, {"", "7", true}, {"?after=7", "7", true}, {"?after=1", "2", false}, {"?after=1&after=2", "", false}, {"?token=value", "1", false}, {"?after=0&token=value", "", false}, {"?after=00", "", false}, {"?after=0&broken=%ZZ", "", false}, {"", "", false}} {
		r := httptest.NewRequest("GET", "http://127.0.0.1/state"+test.path, nil)
		r.Header.Set("Last-Event-ID", test.header)
		_, err := stateCursor(r)
		if (err == nil) != test.valid {
			t.Fatal("游标传输判定不符", test.path)
		}
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1/state", nil)
	r.Header.Add("Last-Event-ID", "1")
	r.Header.Add("Last-Event-ID", "1")
	if _, err := stateCursor(r); err == nil {
		t.Fatal("重复header被接受")
	}
}

// TestStateWriteDeadlineCleared 等待心跳前必须清零短写期限，任何运输失败都向拥有者返回。
func TestStateWriteDeadlineCleared(t *testing.T) {
	writer := &stateWriter{}
	if err := writeStateFrame(writer, "caught_up", "", projectstate.CaughtUp{SchemaVersion: 1, ProjectID: "project", Cursor: "7"}); err != nil {
		t.Fatal(err)
	}
	if len(writer.deadlines) != 2 || writer.deadlines[0].IsZero() || !writer.deadlines[1].IsZero() || strings.Contains(writer.String(), "id:") || !strings.HasSuffix(writer.String(), "\n\n") {
		t.Fatal("控制帧占用了业务id或等待时仍留短期限")
	}
	for _, failure := range []string{"deadline", "write", "flush"} {
		if err := writeStateBytes(&stateWriter{failure: failure}, []byte(": heartbeat\n\n")); err == nil {
			t.Fatal("运输失败被吞掉", failure)
		}
	}
	if err := writeStateBytes(writer, bytes.Repeat([]byte("x"), projectstate.DefaultPolicy().MaxFrameBytes+1)); err == nil {
		t.Fatal("超大帧被发送")
	}
	if err := writeStateFrame(writer, "state", "1", make(chan int)); err == nil {
		t.Fatal("非法载荷编码被吞掉")
	}
}

// TestStateConnectionOwnership 项目与全局上限分别生效，释放后可再次领取。
func TestStateConnectionOwnership(t *testing.T) {
	connections := &stateConnections{projects: map[string]int{}}
	for _, id := range []string{"a", "b", "c", "d"} {
		for range 4 {
			if !connections.acquire(id) {
				t.Fatal("固定配额内未领取")
			}
		}
		if connections.acquire(id) {
			t.Fatal("同项目越过4条流")
		}
	}
	if connections.acquire("e") {
		t.Fatal("全局越过16条流")
	}
	for range 4 {
		connections.release("a")
	}
	if !connections.acquire("e") || connections.projects["a"] != 0 {
		t.Fatal("流释放后没有回收配额")
	}
}
