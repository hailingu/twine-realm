// 本文件覆盖v6真实路由边界与机器契约负例，不模拟外部资源验收。
package httpapi

import (
	"context"
	"errors"
	"github.com/hailingu/twine-realm/server/contract"
	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/projectrun"
	"github.com/hailingu/twine-realm/server/internal/projectstate"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// lifecycleHTTPStore 只提供原请求重试边界，意外调用直接令测试失败。
type lifecycleHTTPStore struct {
	projectrun.Repository
	called bool
}

// RetryProjectRun 模拟数据库拒绝未知外部命令重试。
func (s *lifecycleHTTPStore) RetryProjectRun(context.Context, string, string) (projectrun.Receipt, error) {
	s.called = true
	return projectrun.Receipt{}, project.Failure("PROJECT_RUN_UNKNOWN", "原动作未知")
}

// lifecycleHTTPAdapter 仅用于启用受理边界，测试不得执行物理动作。
type lifecycleHTTPAdapter struct{ projectrun.Adapter }

// lifecycleTestAPI 编译真实契约并注册生产路由。
func lifecycleTestAPI(t *testing.T, service *projectrun.Service) (*API, *http.ServeMux) {
	t.Helper()
	schemas, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	api := &API{run: service, schemas: schemas, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	mux := http.NewServeMux()
	api.lifecycleRoutes(mux)
	return api, mux
}

// TestLifecycleDisabledRoutes 禁用能力返回503而不是无意义的输入错误。
func TestLifecycleDisabledRoutes(t *testing.T) {
	_, mux := lifecycleTestAPI(t, nil)
	for _, path := range []string{"/state", "/lifecycle", "/lifecycle/receipts/key"} {
		r := httptest.NewRequest("GET", "/api/v6/projects/id"+path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 503 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
}

// TestLifecycleInvalidCommand 无效v6请求不能调用存储或执行器。
func TestLifecycleInvalidCommand(t *testing.T) {
	_, mux := lifecycleTestAPI(t, projectrun.NewService(nil, nil, nil))
	for _, body := range []string{"{", `{}`, `{"mutationId":"x","baseRevision":"00","baseGeneration":"0","desired":"running"}`, `{"desired":"deleted"}`} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/v6/projects/id/lifecycle", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatalf("错误状态%d", w.Code)
		}
	}
}

// TestLifecycleRetryRoute 真正的POST重试路由只接受原键且保留未知冲突。
func TestLifecycleRetryRoute(t *testing.T) {
	store := &lifecycleHTTPStore{}
	_, mux := lifecycleTestAPI(t, projectrun.NewService(store, nil, &lifecycleHTTPAdapter{}))
	id := "11111111-1111-4111-8111-111111111111"
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/v6/projects/"+id+"/lifecycle/receipts/"+id+"/retry", nil))
	if w.Code != 409 || !store.called {
		t.Fatalf("重试路由状态%d 调用%v", w.Code, store.called)
	}
}

// lifecycleReadStore 将可读历史与明确存储错误交给真实HTTP处理器。
type lifecycleReadStore struct {
	projectrun.Repository
	failure error
}

// ProjectRunSnapshot 返回历史快照，不授予外部执行许可。
func (s lifecycleReadStore) ProjectRunSnapshot(context.Context, string) (projectrun.Snapshot, error) {
	return projectrun.Snapshot{SchemaVersion: 1, ProjectID: "project", Generation: "0"}, s.failure
}

// ProjectRunReceipt 返回同一项目的历史回执或明确读取失败。
func (s lifecycleReadStore) ProjectRunReceipt(context.Context, string) (projectrun.Receipt, [32]byte, error) {
	return projectrun.Receipt{OperationID: "original", Target: projectrun.Target{ProjectID: "project"}}, [32]byte{}, s.failure
}

// TestLifecycleHistoryHTTP 真实GET路由支持禁用执行器下的历史读取并保留存储错误码。
func TestLifecycleHistoryHTTP(t *testing.T) {
	for _, failure := range []error{nil, project.Failure("MUTATION_NOT_FOUND", "不存在")} {
		_, mux := lifecycleTestAPI(t, projectrun.NewService(lifecycleReadStore{failure: failure}, nil, nil))
		expected := 200
		if failure != nil {
			expected = 404
		}
		for _, path := range []string{"/lifecycle", "/lifecycle/receipts/key"} {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v6/projects/project"+path, nil))
			if w.Code != expected {
				t.Fatalf("历史读取状态%d 期望%d", w.Code, expected)
			}
		}
	}
}

// lifecycleStateReader 提供单次完整快照的测试边界，HTTP不拼接其他读取。
type lifecycleStateReader struct{ failure error }

// ProjectStateV6 返回明确数据库错误或空的完整传输对象。
func (s lifecycleStateReader) ProjectStateV6(context.Context, string) (projectstate.SnapshotV6, error) {
	return projectstate.SnapshotV6{SchemaVersion: 1}, s.failure
}

// TestLifecycleStateHTTP 验证完整读取的失败和成功分别走正式HTTP状态。
func TestLifecycleStateHTTP(t *testing.T) {
	for _, failure := range []error{nil, errors.New("数据库错误")} {
		api, mux := lifecycleTestAPI(t, nil)
		api.runState = lifecycleStateReader{failure: failure}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v6/projects/project/state", nil))
		expected := 200
		if failure != nil {
			expected = 503
		}
		if w.Code != expected {
			t.Fatalf("完整读取状态%d 期望%d", w.Code, expected)
		}
	}
}

// TestLifecycleAdmissionErrorStatus 固定锁忙是冲突，未知核实是服务不可用，不能混为存储失败。
func TestLifecycleAdmissionErrorStatus(t *testing.T) {
	api, _ := lifecycleTestAPI(t, nil)
	for _, tc := range []struct {
		code   string
		status int
	}{{"RUNTIME_PROJECT_BUSY", 409}, {"PROJECT_RUN_ADMISSION_UNKNOWN", 503}} {
		w := httptest.NewRecorder()
		api.failure(w, project.Failure(tc.code, "受控诊断"))
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) {
			t.Fatalf("诊断%s状态%d", tc.code, w.Code)
		}
	}
}
