//go:build integration

// 本文件用真实元数据库验证组件配置、统一布局、失败原子性和原键回放。
package httpapi_test

import (
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// middlewareCommand 读取实际发布目录，不在集成测试中手写定义摘要。
func middlewareCommand(t *testing.T, f *fixture, current project.Project) project.MiddlewareCommand {
	t.Helper()
	catalog := decode[project.DefinitionCatalog](t, call(t, f.handler, "GET", "/api/v1/catalog", nil), 200, "DefinitionCatalog")
	return project.MiddlewareCommand{MutationID: id(t), BaseRevision: current.Revision, Definition: catalog.Definitions[0].Reference, DisplayName: "业务数据库", Configuration: map[string]any{}, Position: project.Position{X: 320, Y: 240}}
}

// canvasURL 定位同一项目的完整图，不以不同时间的接口结果拼接确认状态。
func canvasURL(current project.Project) string { return "/api/v1/projects/" + current.ID + "/canvas" }

// TestMiddlewareDraftPersistence 配置缺项是可保存草稿，更新和进程重开仍保留精确身份。
func TestMiddlewareDraftPersistence(t *testing.T) {
	f := openFixture(t)
	registered, _ := registerSource(t, f, true)
	url := canvasURL(registered.Project)
	command := middlewareCommand(t, f, registered.Project)
	saved := decode[project.Receipt](t, call(t, f.handler, "POST", url+"/middlewares", command), 201, "CanvasReceipt")
	node := saved.Canvas.Middlewares[0]
	if saved.Project.Revision != "2" || node.Configuration["charset"] != "utf8mb4" || node.Configuration["memoryMiB"] != float64(512) || len(node.NotReadyReasons) != 1 || node.Definition != command.Definition {
		t.Fatal("草稿未规范化或版本未固定")
	}
	if !reflect.DeepEqual(saved.Project.Nodes, registered.Project.Nodes) {
		t.Fatal("加入中间件改写了原服务")
	}
	command.MutationID, command.BaseRevision = id(t), saved.Project.Revision
	command.DisplayName = "订单数据库"
	command.Configuration = map[string]any{"memoryMiB": float64(1024), "runtimeProfile": "local-compose-v1"}
	updated := decode[project.Receipt](t, call(t, f.handler, "PUT", url+"/middlewares/"+node.ID, command), 200, "CanvasReceipt")
	if updated.Canvas.Middlewares[0].ID != node.ID || len(updated.Canvas.Middlewares[0].NotReadyReasons) != 0 || updated.Canvas.Middlewares[0].Configuration["memoryMiB"] != float64(1024) {
		t.Fatal("配置更新改变身份或缺项诊断错误")
	}
	if err := f.database.Close(); err != nil {
		t.Fatal(err)
	}
	f.reopen(t)
	opened := decode[project.CanvasSnapshot](t, call(t, f.handler, "GET", url, nil), 200, "CanvasSnapshot")
	if !reflect.DeepEqual(opened.Project, updated.Project) || !reflect.DeepEqual(&opened.Canvas, updated.Canvas) {
		t.Fatal("重开后配置、版本、身份或布局丢失")
	}
	old := decode[project.Receipt](t, call(t, f.handler, "GET", "/api/v1/mutations/"+registered.MutationID, nil), 200, "Receipt")
	if old.Canvas != nil || !reflect.DeepEqual(old.Project, registered.Project) {
		t.Fatal("新类型污染原 v1 回执")
	}
	replay := decode[project.Receipt](t, call(t, f.handler, "GET", "/api/v1/canvas-mutations/"+command.MutationID, nil), 200, "CanvasReceipt")
	if !replay.Replayed || !reflect.DeepEqual(replay.Canvas, updated.Canvas) {
		t.Fatal("组件回执未持久恢复")
	}
}

// TestInvalidMiddlewareDoesNotWrite 未知定义、非法配置或项目归属均不能部分推进版本。
func TestInvalidMiddlewareDoesNotWrite(t *testing.T) {
	f := openFixture(t)
	current := create(t, f)
	url := canvasURL(current)
	command := middlewareCommand(t, f, current)
	for _, configuration := range []map[string]any{{"password": "dummy-only"}, {"memoryMiB": 255}, {"runtimeProfile": "unknown"}, {"$ref": "file:///outside"}} {
		command.MutationID, command.Configuration = id(t), configuration
		decode[any](t, call(t, f.handler, "POST", url+"/middlewares", command), 400, "ErrorResponse")
		decode[any](t, call(t, f.handler, "GET", "/api/v1/canvas-mutations/"+command.MutationID, nil), 404, "ErrorResponse")
	}
	command.Configuration = map[string]any{}
	for _, field := range []string{"id", "version", "digest"} {
		invalid := command
		invalid.MutationID = id(t)
		switch field {
		case "id":
			invalid.Definition.ID = "unknown"
		case "version":
			invalid.Definition.Version = "2.0.0"
		case "digest":
			invalid.Definition.Digest = "sha256:" + strings.Repeat("0", 64)
		}
		decode[any](t, call(t, f.handler, "POST", url+"/middlewares", invalid), 400, "ErrorResponse")
	}
	opened := decode[project.CanvasSnapshot](t, call(t, f.handler, "GET", url, nil), 200, "CanvasSnapshot")
	if opened.Project.Revision != current.Revision || len(opened.Canvas.Middlewares) != 0 {
		t.Fatal("拒绝请求改变了图")
	}
	command.MutationID = id(t)
	decode[any](t, call(t, f.handler, "PUT", url+"/middlewares/"+id(t), command), 404, "ErrorResponse")
	decode[any](t, call(t, f.handler, "GET", "/api/v1/canvas-mutations/"+command.MutationID, nil), 404, "ErrorResponse")
	decode[any](t, call(t, f.handler, "GET", "/api/v1/canvas-mutations/bad", nil), 400, "ErrorResponse")
	decode[any](t, call(t, f.handler, "GET", "/api/v1/projects/bad/canvas", nil), 400, "ErrorResponse")
}

// TestMiddlewareReplayAndConcurrency 原键只创建一次，不同并发意图不能互相覆盖。
func TestMiddlewareReplayAndConcurrency(t *testing.T) {
	f := openFixture(t)
	current := create(t, f)
	url := canvasURL(current)
	command := middlewareCommand(t, f, current)
	var wait sync.WaitGroup
	codes := make(chan int, 2)
	for range 2 {
		wait.Go(func() { codes <- call(t, f.handler, "POST", url+"/middlewares", command).Code })
	}
	wait.Wait()
	for range 2 {
		if code := <-codes; code != 201 {
			t.Fatalf("原键并发失败: %d", code)
		}
	}
	command.DisplayName = "同键异内容"
	decode[any](t, call(t, f.handler, "POST", url+"/middlewares", command), 409, "ErrorResponse")
	command.BaseRevision = "1"
	for range 2 {
		request := command
		request.MutationID = id(t)
		wait.Go(func() { codes <- call(t, f.handler, "POST", url+"/middlewares", request).Code })
	}
	wait.Wait()
	observed := map[int]int{}
	for range 2 {
		observed[<-codes]++
	}
	if observed[201] != 1 || observed[409] != 1 {
		t.Fatalf("并发覆盖或重复生效: %v", observed)
	}
	value := decode[project.CanvasSnapshot](t, call(t, f.handler, "GET", url, nil), 200, "CanvasSnapshot")
	if value.Project.Revision != "2" || len(value.Canvas.Middlewares) != 2 {
		t.Fatal("并发版本或实例数量不正确")
	}
}

// TestMixedCanvasLayoutAtomicity 同一命令同时保存服务和中间件，后续非法成员使前序更新回滚。
func TestMixedCanvasLayoutAtomicity(t *testing.T) {
	f := openFixture(t)
	registered, _ := registerSource(t, f, true)
	url := canvasURL(registered.Project)
	command := middlewareCommand(t, f, registered.Project)
	added := decode[project.Receipt](t, call(t, f.handler, "POST", url+"/middlewares", command), 201, "CanvasReceipt")
	positions := []project.CanvasPosition{{ComponentID: added.Project.Nodes[0].ID, X: 100, Y: 200}, {ComponentID: added.Canvas.Middlewares[0].ID, X: 500, Y: 600}}
	layout := project.CanvasLayoutCommand{MutationID: id(t), BaseRevision: added.Project.Revision, Positions: positions}
	saved := decode[project.Receipt](t, call(t, f.handler, "PUT", url+"/layout", layout), 200, "CanvasReceipt")
	if saved.Project.Nodes[0].Position.X != 100 || saved.Canvas.Middlewares[0].Position.X != 500 {
		t.Fatal("混合布局没有全部保存")
	}
	layout.MutationID, layout.BaseRevision = id(t), saved.Project.Revision
	layout.Positions = []project.CanvasPosition{{ComponentID: added.Project.Nodes[0].ID, X: 999, Y: 999}, {ComponentID: id(t), X: 999, Y: 999}}
	decode[any](t, call(t, f.handler, "PUT", url+"/layout", layout), 400, "ErrorResponse")
	after := decode[project.CanvasSnapshot](t, call(t, f.handler, "GET", url, nil), 200, "CanvasSnapshot")
	if !reflect.DeepEqual(after.Project, saved.Project) || !reflect.DeepEqual(&after.Canvas, saved.Canvas) {
		t.Fatal("后序非法成员留下部分布局")
	}
	decode[any](t, call(t, f.handler, "GET", "/api/v1/canvas-mutations/"+layout.MutationID, nil), 404, "ErrorResponse")
	old := project.LayoutCommand{MutationID: id(t), BaseRevision: saved.Project.Revision, Positions: []project.NodePosition{{ServiceID: added.Project.Nodes[0].ID, X: 700, Y: 800}}}
	decode[project.Receipt](t, call(t, f.handler, "PUT", "/api/v1/projects/"+added.Project.ID+"/layout", old), 200, "Receipt")
	after = decode[project.CanvasSnapshot](t, call(t, f.handler, "GET", url, nil), 200, "CanvasSnapshot")
	if after.Project.Nodes[0].Position.X != 700 || after.Canvas.Middlewares[0].Position.X != 500 {
		t.Fatal("旧布局入口破坏新组件")
	}
}

// TestMiddlewareStorageRecovery 元数据库不可用不先改外部状态，原键恢复无需重新解释候选。
func TestMiddlewareStorageRecovery(t *testing.T) {
	f := openFixture(t)
	current := create(t, f)
	url := canvasURL(current)
	command := middlewareCommand(t, f, current)
	if err := f.database.Close(); err != nil {
		t.Fatal(err)
	}
	decode[any](t, call(t, f.handler, "POST", url+"/middlewares", command), 503, "ErrorResponse")
	f.reopen(t)
	decode[any](t, call(t, f.handler, "GET", "/api/v1/canvas-mutations/"+command.MutationID, nil), 404, "ErrorResponse")
	result := decode[project.Receipt](t, call(t, f.handler, "POST", url+"/middlewares", command), 201, "CanvasReceipt")
	if result.Project.Revision != "1" || len(result.Canvas.Middlewares) != 1 {
		t.Fatal("原键恢复产生重复实例")
	}
}
