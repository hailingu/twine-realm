//go:build integration

// 本文件以真实回环HTTP和元数据库验证SSE重放、限流、心跳与中途窗口失效。
package httpapi_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/projectstate"
)

// stateNetwork 使用与平台相同的普通15秒写期限，测试流必须显式管理自身期限。
func stateNetwork(t *testing.T, f *fixture) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { r.Host = host; f.handler.ServeHTTP(w, r) }))
	server.Config.WriteTimeout = 15 * time.Second
	server.Start()
	t.Cleanup(server.Close)
	return server
}

// openStateNetwork 每个响应由本测试关闭，连接不会流入用户预览或其他项目。
func openStateNetwork(t *testing.T, server *httptest.Server, id, cursor string) *http.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	t.Cleanup(cancel)
	r, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v5/projects/"+id+"/state/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Last-Event-ID", cursor)
	r.Header.Set("Accept", "text/event-stream")
	response, err := server.Client().Do(r)
	if err != nil {
		t.Fatal("真实状态流未连接", err)
	}
	t.Cleanup(func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	})
	return response
}

// wireStateFrame 只为验证读取服务端已经有界编码的帧，不用作产品解析器。
func wireStateFrame(t *testing.T, reader *bufio.Reader) (kind, id, data string) {
	t.Helper()
	var raw strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal("状态帧未完整到达", err)
		}
		raw.WriteString(line)
		if raw.Len() > 4096 {
			t.Fatal("真实状态帧超过上限")
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == "" {
			return kind, id, data
		}
		if strings.HasPrefix(line, "event: ") {
			kind = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "id: ") {
			id = strings.TrimPrefix(line, "id: ")
		}
		if strings.HasPrefix(line, "data: ") {
			data = strings.TrimPrefix(line, "data: ")
		}
	}
}

// assertWireState 真实帧与公开事件schema及连续id共同核实。
func assertWireState(t *testing.T, reader *bufio.Reader, projectID, sequence string) {
	t.Helper()
	kind, id, data := wireStateFrame(t, reader)
	var event projectstate.Event
	if kind != "state" || id != sequence || json.Unmarshal([]byte(data), &event) != nil || event.Sequence != sequence {
		t.Fatal("真实业务事件帧不符")
	}
	assertStateEvents(t, []projectstate.Event{event}, projectID)
}

// assertWireCaughtUp 控制帧没有业务id，游标指向实际最后一条事件。
func assertWireCaughtUp(t *testing.T, reader *bufio.Reader, projectID, cursor string) {
	t.Helper()
	kind, id, data := wireStateFrame(t, reader)
	var value projectstate.CaughtUp
	if kind != "caught_up" || id != "" || json.Unmarshal([]byte(data), &value) != nil || value.ProjectID != projectID || value.Cursor != cursor {
		t.Fatal("追平控制冒充业务事件或错误游标")
	}
}

// TestProjectStateStreamReplayAndOwnership 断连按Last-Event-ID重放，同项目限流与取消释放都经过实际HTTP。
func TestProjectStateStreamReplayAndOwnership(t *testing.T) {
	f := openFixture(t)
	current := create(t, f)
	server := stateNetwork(t, f)
	response := openStateNetwork(t, server, current.ID, "0")
	if response.StatusCode != 200 || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatal("真实流没有正确运输契约")
	}
	reader := bufio.NewReader(response.Body)
	assertWireState(t, reader, current.ID, "1")
	assertWireCaughtUp(t, reader, current.ID, "1")
	command := project.LayoutCommand{MutationID: id(t), BaseRevision: "0", Positions: []project.NodePosition{}}
	decode[project.Receipt](t, call(t, f.handler, "PUT", "/api/v1/projects/"+current.ID+"/layout", command), 200, "Receipt")
	assertWireState(t, reader, current.ID, "2")
	assertWireCaughtUp(t, reader, current.ID, "2")
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	replay := openStateNetwork(t, server, current.ID, "1")
	assertWireState(t, bufio.NewReader(replay.Body), current.ID, "2")
	assertStateQuotaReleased(t, server, current.ID, replay)
}

// assertStateQuotaReleased 只核实真实连接的项目配额及取消释放，不改变业务目标。
func assertStateQuotaReleased(t *testing.T, server *httptest.Server, projectID string, replay *http.Response) {
	t.Helper()
	opened := []*http.Response{replay}
	for range 3 {
		value := openStateNetwork(t, server, projectID, "2")
		if value.StatusCode != 200 {
			t.Fatal("配额内连接未受理")
		}
		opened = append(opened, value)
	}
	limited := openStateNetwork(t, server, projectID, "2")
	if limited.StatusCode != 429 || limited.Header.Get("Retry-After") != "2" {
		t.Fatal("真实连接没有限流与重试期限")
	}
	for _, value := range opened {
		if err := value.Body.Close(); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		value := openStateNetwork(t, server, projectID, "2")
		if value.StatusCode == 200 {
			return
		}
		if err := value.Body.Close(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("取消后连接配额未释放")
}

// TestProjectStateStreamSurvivesHTTPWriteTimeout 两次15秒心跳越过普通写期限后仍能收到新提交。
func TestProjectStateStreamSurvivesHTTPWriteTimeout(t *testing.T) {
	f := openFixture(t)
	current := create(t, f)
	server := stateNetwork(t, f)
	response := openStateNetwork(t, server, current.ID, "1")
	if response.StatusCode != 200 {
		t.Fatal("状态流未受理")
	}
	reader := bufio.NewReader(response.Body)
	assertWireCaughtUp(t, reader, current.ID, "1")
	for range 2 {
		kind, id, data := wireStateFrame(t, reader)
		if kind != "" || id != "" || data != "" {
			t.Fatal("心跳递增业务序号")
		}
	}
	command := project.LayoutCommand{MutationID: id(t), BaseRevision: "0", Positions: []project.NodePosition{}}
	if _, err := f.database.SaveLayout(context.Background(), current.ID, command, [32]byte{}); err != nil {
		t.Fatal(err)
	}
	assertWireState(t, reader, current.ID, "2")
	assertWireCaughtUp(t, reader, current.ID, "2")
}

// TestProjectStateStreamExpiredAfterHeaders 窗口在流建立后失效时发送独立resync并关闭。
func TestProjectStateStreamExpiredAfterHeaders(t *testing.T) {
	f := openFixture(t)
	current := create(t, f)
	server := stateNetwork(t, f)
	response := openStateNetwork(t, server, current.ID, "1")
	reader := bufio.NewReader(response.Body)
	assertWireCaughtUp(t, reader, current.ID, "1")
	command := project.LayoutCommand{MutationID: id(t), BaseRevision: "0", Positions: []project.NodePosition{}}
	if _, err := f.database.SaveLayout(context.Background(), current.ID, command, [32]byte{}); err != nil {
		t.Fatal(err)
	}
	if _, err := stateSQL(t, f).Exec("UPDATE project_state_events SET created_at=? WHERE project_id=?", time.Now().UTC().Add(-25*time.Hour), current.ID); err != nil {
		t.Fatal(err)
	}
	kind, id, data := wireStateFrame(t, reader)
	var value projectstate.Resync
	if kind != "resync" || id != "" || json.Unmarshal([]byte(data), &value) != nil || value.Code != "STATE_CURSOR_EXPIRED" {
		t.Fatal("中途失效未要求完整恢复")
	}
	if rest, err := io.ReadAll(reader); err != nil || len(rest) != 0 {
		t.Fatal("失效流未正常结束")
	}
}
