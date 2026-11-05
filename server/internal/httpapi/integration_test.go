//go:build integration

// 本文件穿过真实用例、Git、迁移和 MySQL 边界验证持久化与恢复不变量。
package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/hailingu/twine-realm/server/contract"
	"github.com/hailingu/twine-realm/server/internal/gitworkspace"
	"github.com/hailingu/twine-realm/server/internal/httpapi"
	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/store"
)

const host = "127.0.0.1:8080"

// fixture 持有只属于本次测试的数据库连接与仓库允许目录。
type fixture struct {
	handler  http.Handler
	database *store.Store
	root     string
	dsn      string
}

// openFixture 不允许依赖缺失时静默跳过集成检查。
func openFixture(t *testing.T) *fixture {
	t.Helper()
	dsn := os.Getenv("TWINE_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Fatal("集成检查必须注入独立测试数据库")
	}
	f := &fixture{root: t.TempDir(), dsn: dsn}
	f.reopen(t)
	t.Cleanup(func() {
		if err := f.database.Close(); err != nil {
			t.Error(err)
		}
	})
	return f
}

// reopen 重建真实连接和 HTTP 装配，恢复不依赖旧进程内存。
func (f *fixture) reopen(t *testing.T) {
	t.Helper()
	database, err := store.Open(context.Background(), f.dsn)
	if err != nil {
		t.Fatal(err)
	}
	f.database = database
	inspector, err := gitworkspace.New([]string{f.root})
	if err != nil {
		t.Fatal(err)
	}
	f.handler, err = httpapi.New(project.NewService(database, database, database, inspector), host, []string{"http://" + host}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
}

// call 发起符合浏览器来源约束的请求并返回可观测结果。
func call(t *testing.T, handler http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, "http://"+host+path, bytes.NewReader(data))
	request.Header.Set("Origin", "http://"+host)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Twine-Request", "1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// decode 验证发布机器契约，而不只依赖 Go 结构体反序列化成功。
func decode[T any](t *testing.T, response *httptest.ResponseRecorder, status int, name string) T {
	t.Helper()
	if response.Code != status {
		t.Fatalf("HTTP %d: %s", response.Code, response.Body.String())
	}
	schemas, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	var unknown any
	if err := json.Unmarshal(response.Body.Bytes(), &unknown); err != nil {
		t.Fatal(err)
	}
	if err := schemas[name].Validate(unknown); err != nil {
		t.Fatalf("响应契约: %v\n%s", err, response.Body.String())
	}
	var result T
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// id 为每个独立意图创建随机身份，重试直接复用原命令。
func id(t *testing.T) string {
	t.Helper()
	value, err := project.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// create 创建可供后续步骤打开的真实持久项目。
func create(t *testing.T, f *fixture) project.Project {
	t.Helper()
	command := project.CreateCommand{MutationID: id(t), Name: "集成项目"}
	return decode[project.Receipt](t, call(t, f.handler, "POST", "/api/v1/projects", command), 201, "Receipt").Project
}

// repository 准备同名但不同路径的空 Git 仓库。
func repository(t *testing.T, root, name string) string {
	t.Helper()
	path := filepath.Join(root, name, "service")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("git", "-C", path, "init", "-b", "develop")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Git 夹具: %v %s", err, output)
	}
	return path
}

// TestProjectPersistenceAndReceipts 验证稳定身份、空仓库、唯一关联与元数据库恢复。
func TestProjectPersistenceAndReceipts(t *testing.T) {
	t.Run("完整登记保存回执与重开", projectPersistenceScenario)
}

// TestConflictAtomicity 验证版本竞争、事务回滚和未受理回执。
func TestConflictAtomicity(t *testing.T) {
	t.Run("冲突和失败不产生部分写入", func(t *testing.T) {
		f := openFixture(t)
		current := create(t, f)
		url := "/api/v1/projects/" + current.ID
		bad := project.RegisterCommand{MutationID: id(t), BaseRevision: "0", RepositoryPath: filepath.Join(f.root, "missing"), Position: project.Position{}}
		decode[any](t, call(t, f.handler, "POST", url+"/services", bad), 400, "ErrorResponse")
		decode[any](t, call(t, f.handler, "GET", "/api/v1/mutations/"+bad.MutationID, nil), 404, "ErrorResponse")
		bad.RepositoryPath = repository(t, f.root, "valid")
		registered := decode[project.Receipt](t, call(t, f.handler, "POST", url+"/services", bad), 201, "Receipt")
		positions := []project.NodePosition{{ServiceID: registered.Project.Nodes[0].ID, X: 12, Y: 34}}
		var wait sync.WaitGroup
		codes := make(chan int, 2)
		for range 2 {
			request := project.LayoutCommand{MutationID: id(t), BaseRevision: registered.Project.Revision, Positions: positions}
			wait.Go(func() { codes <- call(t, f.handler, "PUT", url+"/layout", request).Code })
		}
		wait.Wait()
		close(codes)
		observed := map[int]int{}
		for code := range codes {
			observed[code]++
		}
		if observed[200] != 1 || observed[409] != 1 {
			t.Fatalf("并发保存覆盖或丢失冲突: %v", observed)
		}
		before := decode[project.Project](t, call(t, f.handler, "GET", url, nil), 200, "Project")
		invalid := project.LayoutCommand{MutationID: id(t), BaseRevision: before.Revision, Positions: []project.NodePosition{}}
		decode[any](t, call(t, f.handler, "PUT", url+"/layout", invalid), 400, "ErrorResponse")
		after := decode[project.Project](t, call(t, f.handler, "GET", url, nil), 200, "Project")
		if !reflect.DeepEqual(before, after) {
			t.Fatal("无效布局产生部分更新")
		}
	})
}

// TestConcurrentReplay 验证同键并发不会生成重复服务或推进两次版本。
func TestConcurrentReplay(t *testing.T) {
	t.Run("同时重试仅提交一次", func(t *testing.T) {
		f := openFixture(t)
		current := create(t, f)
		request := project.RegisterCommand{MutationID: id(t), BaseRevision: "0", RepositoryPath: repository(t, f.root, "race"), Position: project.Position{}}
		var wait sync.WaitGroup
		results := make(chan *httptest.ResponseRecorder, 2)
		for range 2 {
			wait.Go(func() { results <- call(t, f.handler, "POST", "/api/v1/projects/"+current.ID+"/services", request) })
		}
		wait.Wait()
		close(results)
		for response := range results {
			value := decode[project.Receipt](t, response, 201, "Receipt")
			if value.Project.Revision != "1" || len(value.Project.Nodes) != 1 {
				t.Fatal("同键并发重复提交")
			}
		}
	})
}

// TestStorageFailureRecovery 验证真实连接不可用时不丢草稿语义或原请求身份。
func TestStorageFailureRecovery(t *testing.T) {
	t.Run("存储失败可恢复且不伪报保存", func(t *testing.T) {
		f := openFixture(t)
		current := create(t, f)
		command := project.LayoutCommand{MutationID: id(t), BaseRevision: current.Revision, Positions: []project.NodePosition{}}
		path := "/api/v1/projects/" + current.ID + "/layout"
		if err := f.database.Close(); err != nil {
			t.Fatal(err)
		}
		decode[any](t, call(t, f.handler, "PUT", path, command), 503, "ErrorResponse")
		f.reopen(t)
		decode[any](t, call(t, f.handler, "GET", "/api/v1/mutations/"+command.MutationID, nil), 404, "ErrorResponse")
		value := decode[project.Receipt](t, call(t, f.handler, "PUT", path, command), 200, "Receipt")
		if value.Project.Revision != "1" {
			t.Fatal("未受理失败重试重复推进版本")
		}
	})
}

// TestLocalRequestBoundary 拒绝跨站、DNS 重绑定、表单和额外字段。
func TestLocalRequestBoundary(t *testing.T) {
	t.Run("本机请求安全与契约", func(t *testing.T) {
		f := openFixture(t)
		for _, test := range []struct {
			name, host, origin, media, marker string
			status                            int
		}{
			{"正常请求", host, "http://" + host, "application/json", "1", 201},
			{"重绑定", "evil.invalid", "http://" + host, "application/json", "1", 403},
			{"跨站", host, "https://evil.invalid", "application/json", "1", 403},
			{"表单", host, "http://" + host, "application/x-www-form-urlencoded", "1", 415},
			{"缺少标记", host, "http://" + host, "application/json", "", 403},
		} {
			t.Run(test.name, func(t *testing.T) {
				body := fmt.Sprintf(`{"mutationId":%q,"name":"安全检查"}`, id(t))
				request := httptest.NewRequest("POST", "http://"+host+"/api/v1/projects", bytes.NewBufferString(body))
				request.Host = test.host
				request.Header.Set("Origin", test.origin)
				request.Header.Set("Content-Type", test.media)
				request.Header.Set("X-Twine-Request", test.marker)
				response := httptest.NewRecorder()
				f.handler.ServeHTTP(response, request)
				if response.Code != test.status {
					t.Fatalf("HTTP %d", response.Code)
				}
			})
		}
		decode[any](t, call(t, f.handler, "POST", "/api/v1/projects", map[string]any{"mutationId": id(t), "name": "项目", "admin": true}), 400, "ErrorResponse")
	})
}

// TestMalformedRequests 用真实 HTTP 和数据库验证不完整输入不成为已提交事实。
func TestMalformedRequests(t *testing.T) {
	t.Run("未知项目与非法请求体", func(t *testing.T) {
		f := openFixture(t)
		decode[any](t, call(t, f.handler, "GET", "/api/v1/projects/"+id(t), nil), 404, "ErrorResponse")
		decode[any](t, call(t, f.handler, "GET", "/api/v1/projects/bad", nil), 400, "ErrorResponse")
		decode[any](t, call(t, f.handler, "GET", "/api/v1/mutations/bad", nil), 400, "ErrorResponse")
		decode[any](t, call(t, f.handler, "GET", "/api/v1/unknown", nil), 404, "ErrorResponse")
		for _, body := range []string{"{", `{} {}`, string(bytes.Repeat([]byte("x"), 128*1024+1))} {
			request := httptest.NewRequest("POST", "http://"+host+"/api/v1/projects", bytes.NewBufferString(body))
			request.Header.Set("Origin", "http://"+host)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Twine-Request", "1")
			response := httptest.NewRecorder()
			f.handler.ServeHTTP(response, request)
			decode[any](t, response, 400, "ErrorResponse")
		}
		request := httptest.NewRequest("GET", "http://"+host+"/api/v1/projects", nil)
		request.Header.Set("Sec-Fetch-Site", "cross-site")
		response := httptest.NewRecorder()
		f.handler.ServeHTTP(response, request)
		decode[any](t, response, 403, "ErrorResponse")
	})
}

// projectPersistenceScenario 执行中文登记与恢复场景。
func projectPersistenceScenario(t *testing.T) {
	f := openFixture(t)
	current := create(t, f)
	path := repository(t, f.root, "one")
	command := project.RegisterCommand{MutationID: id(t), BaseRevision: current.Revision, RepositoryPath: path, DisplayName: "首个服务", Position: project.Position{X: 70, Y: 90}}
	url := "/api/v1/projects/" + current.ID
	registered := decode[project.Receipt](t, call(t, f.handler, "POST", url+"/services", command), 201, "Receipt")
	if len(registered.Project.Nodes) != 1 || registered.Project.Nodes[0].Repository.HeadCommit != nil {
		t.Fatal("空仓库身份或源提交不正确")
	}
	replay := decode[project.Receipt](t, call(t, f.handler, "POST", url+"/services", command), 201, "Receipt")
	if !replay.Replayed || !reflect.DeepEqual(replay.Project, registered.Project) {
		t.Fatal("相同请求未重放原结果")
	}
	command.DisplayName = "同键不同内容"
	decode[any](t, call(t, f.handler, "POST", url+"/services", command), 409, "ErrorResponse")
	command.MutationID, command.BaseRevision = id(t), registered.Project.Revision
	decode[any](t, call(t, f.handler, "POST", url+"/services", command), 409, "ErrorResponse")
	command.MutationID, command.RepositoryPath = id(t), repository(t, f.root, "two")
	second := decode[project.Receipt](t, call(t, f.handler, "POST", url+"/services", command), 201, "Receipt")
	if len(second.Project.Nodes) != 2 || second.Project.Nodes[0].Repository.ID == second.Project.Nodes[1].Repository.ID {
		t.Fatal("同名不同路径仓库被错误合并")
	}
	positions := []project.NodePosition{}
	for _, node := range second.Project.Nodes {
		positions = append(positions, project.NodePosition{ServiceID: node.ID, X: 123, Y: 456})
	}
	layout := project.LayoutCommand{MutationID: id(t), BaseRevision: second.Project.Revision, Positions: positions}
	saved := decode[project.Receipt](t, call(t, f.handler, "PUT", url+"/layout", layout), 200, "Receipt")
	if err := f.database.Close(); err != nil {
		t.Fatal(err)
	}
	f.reopen(t)
	opened := decode[project.Project](t, call(t, f.handler, "GET", url, nil), 200, "Project")
	if !reflect.DeepEqual(saved.Project, opened) {
		t.Fatal("重开后身份、路径、名称或坐标漂移")
	}
	receipt := decode[project.Receipt](t, call(t, f.handler, "GET", "/api/v1/mutations/"+layout.MutationID, nil), 200, "Receipt")
	if !receipt.Replayed || !reflect.DeepEqual(receipt.Project, opened) {
		t.Fatal("重开后丢失持久回执")
	}
}
