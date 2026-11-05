//go:build integration

// 本文件穿过实际 schema、领域、迁移和 MySQL 验证版本化契约与最终候选图事务。
package httpapi_test

import (
	"encoding/json"
	"os"
	"reflect"
	"sync"
	"testing"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// graphFixture 保留原服务与 MySQL 草稿，不预先伪造任何业务端口。
func graphFixture(t *testing.T) (*fixture, project.GraphSnapshot) {
	t.Helper()
	f := openFixture(t)
	registered, _ := registerSource(t, f, true)
	command := middlewareCommand(t, f, registered.Project)
	added := decode[project.Receipt](t, call(t, f.handler, "POST", canvasURL(registered.Project)+"/middlewares", command), 201, "CanvasReceipt")
	value := decode[project.GraphSnapshot](t, call(t, f.handler, "GET", graphURL(added.Project), nil), 200, "GraphSnapshot")
	if len(value.Graph.ServiceContracts) != 0 || len(value.Graph.Bindings) != 0 {
		t.Fatal("既有服务被伪造默认端口或连接")
	}
	return f, value
}

// graphURL 返回显式 v2 入口，原 v1 画布契约不被改义。
func graphURL(current project.Project) string { return "/api/v2/projects/" + current.ID + "/graph" }

// contractDocument 使用与 UI 相同的多接口 / 多版本示例，不代表服务源码已经实现。
func contractDocument(t *testing.T) project.ServiceContractDocument {
	t.Helper()
	data, err := os.ReadFile("../../contract/v2/fixtures/service-contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var value project.ServiceContractDocument
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

// contractOperation 只为给定项目的真实服务声明用户明确选择的契约。
func contractOperation(value project.GraphSnapshot, document project.ServiceContractDocument) project.GraphOperation {
	return project.GraphOperation{Type: "service.setContract", ServiceID: value.Project.Nodes[0].ID, Document: &document}
}

// bindingOperation 使用语义端口构造候选，数据库及凭据真实值都不在命令里。
func bindingOperation(value project.GraphSnapshot, name string) project.GraphOperation {
	return project.GraphOperation{Type: "binding.create", Source: &project.PortEndpoint{ComponentID: value.Project.Nodes[0].ID, PortID: "database"}, Target: &project.PortEndpoint{ComponentID: value.Graph.Middlewares[0].ID, PortID: "mysql"}, DatabaseName: name, Purpose: "readWrite"}
}

// editGraph 验证真实公开响应，失败状态不得被解释成已保存图。
func editGraph(t *testing.T, f *fixture, current project.Project, operations ...project.GraphOperation) project.Receipt {
	t.Helper()
	command := project.GraphEditCommand{MutationID: id(t), BaseRevision: current.Revision, Operations: operations}
	return decode[project.Receipt](t, call(t, f.handler, "POST", graphURL(current)+"/edits", command), 200, "GraphReceipt")
}

// TestGraphContractsAndRecovery 同批先建边后声明契约仍按最终图校验，重启后保持所有身份与回执。
func TestGraphContractsAndRecovery(t *testing.T) {
	f, current := graphFixture(t)
	command := project.GraphEditCommand{MutationID: id(t), BaseRevision: current.Project.Revision, Operations: []project.GraphOperation{bindingOperation(current, "orders"), contractOperation(current, contractDocument(t))}}
	saved := decode[project.Receipt](t, call(t, f.handler, "POST", graphURL(current.Project)+"/edits", command), 200, "GraphReceipt")
	if saved.Graph == nil || len(saved.Graph.Bindings) != 1 || len(saved.Graph.ServiceContracts) != 1 || saved.Project.Revision != "3" {
		t.Fatal("最终图没有原子保存")
	}
	binding := saved.Graph.Bindings[0]
	if binding.Source.PortID != "database" || binding.Target.PortID != "mysql" || binding.SourceDefinition != saved.Graph.ServiceContracts[0].Reference || binding.TargetDefinition != current.Graph.Middlewares[0].Definition || binding.DatabaseID == "" || binding.Credential.ID == "" {
		t.Fatal("依赖缺少语义身份或固定版本")
	}
	if err := f.database.Close(); err != nil {
		t.Fatal(err)
	}
	f.reopen(t)
	opened := decode[project.GraphSnapshot](t, call(t, f.handler, "GET", graphURL(saved.Project), nil), 200, "GraphSnapshot")
	if !reflect.DeepEqual(opened.Project, saved.Project) || !reflect.DeepEqual(&opened.Graph, saved.Graph) {
		t.Fatal("重启后完整图和回执不同")
	}
	replay := decode[project.Receipt](t, call(t, f.handler, "POST", graphURL(current.Project)+"/edits", command), 200, "GraphReceipt")
	if !replay.Replayed || !reflect.DeepEqual(replay.Graph, saved.Graph) {
		t.Fatal("原键重放改变了稳定绑定身份")
	}
	queried := decode[project.Receipt](t, call(t, f.handler, "GET", "/api/v2/graph-mutations/"+command.MutationID, nil), 200, "GraphReceipt")
	if !queried.Replayed || !reflect.DeepEqual(queried.Graph, saved.Graph) {
		t.Fatal("专用回执不可恢复")
	}
	decode[any](t, call(t, f.handler, "GET", "/api/v1/mutations/"+command.MutationID, nil), 400, "ErrorResponse")
	legacy := decode[project.CanvasSnapshot](t, call(t, f.handler, "GET", canvasURL(saved.Project), nil), 200, "CanvasSnapshot")
	if !reflect.DeepEqual(legacy.Project, saved.Project) || len(legacy.Canvas.Middlewares) != 1 || legacy.Canvas.SchemaVersion != 1 {
		t.Fatal("v2 图污染了已发布 v1 投影")
	}
}

// TestGraphRejectsFinalInvalidity 拒绝方向、协议版本、角色、端口、重复与超基数，旧图不部分变化。
func TestGraphRejectsFinalInvalidity(t *testing.T) {
	for _, scenario := range []string{"无契约", "方向", "不存在端口", "角色", "协议版本", "重复", "超基数", "悬空"} {
		t.Run(scenario, func(t *testing.T) {
			f, current := graphFixture(t)
			document := contractDocument(t)
			binding := bindingOperation(current, "orders")
			operations := []project.GraphOperation{contractOperation(current, document), binding}
			switch scenario {
			case "无契约":
				operations = []project.GraphOperation{binding}
			case "方向":
				binding.Source, binding.Target = binding.Target, binding.Source
				operations[1] = binding
			case "不存在端口":
				binding.Source.PortID = "missing"
				operations[1] = binding
			case "角色":
				document.Ports[0].Role = "provide"
				document.Ports[0].Required = false
				operations[0] = contractOperation(current, document)
			case "协议版本":
				document.Ports[0].Contract = "mysql-wire/9"
				operations[0] = contractOperation(current, document)
			case "重复":
				operations = append(operations, binding)
			case "超基数":
				second := binding
				second.DatabaseName = "other_database"
				operations = append(operations, second)
			case "悬空":
				binding.Target.ComponentID = id(t)
				operations[1] = binding
			}
			command := project.GraphEditCommand{MutationID: id(t), BaseRevision: current.Project.Revision, Operations: operations}
			response := call(t, f.handler, "POST", graphURL(current.Project)+"/edits", command)
			if response.Code != 400 && response.Code != 409 {
				t.Fatalf("非法关系未明确拒绝: %d %s", response.Code, response.Body.String())
			}
			decode[any](t, response, response.Code, "ErrorResponse")
			after := decode[project.GraphSnapshot](t, call(t, f.handler, "GET", graphURL(current.Project), nil), 200, "GraphSnapshot")
			if !reflect.DeepEqual(after, current) {
				t.Fatal("最终非法图留下部分契约或绑定")
			}
			decode[any](t, call(t, f.handler, "GET", "/api/v2/graph-mutations/"+command.MutationID, nil), 404, "ErrorResponse")
		})
	}
}

// TestGraphReconnectAndImmutableVersions 重连保持边与数据身份，已发布的契约版本不能借重连改义。
func TestGraphReconnectAndImmutableVersions(t *testing.T) {
	f, current := graphFixture(t)
	document := contractDocument(t)
	initial := editGraph(t, f, current.Project, contractOperation(current, document), bindingOperation(current, "orders"))
	before := initial.Graph.Bindings[0]
	document.Version = "1.1.0"
	reconnect := bindingOperation(current, "orders")
	reconnect.Type, reconnect.BindingID = "binding.reconnect", before.ID
	updated := editGraph(t, f, initial.Project, contractOperation(current, document), reconnect)
	after := updated.Graph.Bindings[0]
	if before.ID != after.ID || before.DatabaseID != after.DatabaseID || before.Credential != after.Credential || after.SourceDefinition.Version != "1.1.0" {
		t.Fatal("明确重连改变稳定边或同一数据身份")
	}
	document.ConfigurationSchema["description"] = "同版本的其他内容"
	command := project.GraphEditCommand{MutationID: id(t), BaseRevision: updated.Project.Revision, Operations: []project.GraphOperation{contractOperation(current, document), reconnect}}
	response := call(t, f.handler, "POST", graphURL(current.Project)+"/edits", command)
	decode[any](t, response, 409, "ErrorResponse")
	opened := decode[project.GraphSnapshot](t, call(t, f.handler, "GET", graphURL(current.Project), nil), 200, "GraphSnapshot")
	if !reflect.DeepEqual(&opened.Graph, updated.Graph) || !reflect.DeepEqual(opened.Project, updated.Project) {
		t.Fatal("同版本冲突改写了旧图")
	}
	newBinding := bindingOperation(current, "other_database")
	replaced := editGraph(t, f, updated.Project, newBinding, project.GraphOperation{Type: "binding.remove", BindingID: after.ID})
	if len(replaced.Graph.Bindings) != 1 || replaced.Graph.Bindings[0].ID == after.ID {
		t.Fatal("最终图先增后删未按最终基数裁决")
	}
}

// TestGraphConcurrentReplay 相同请求仅提交一次，不同意图在同 revision 上竞争必须有一方冲突。
func TestGraphConcurrentReplay(t *testing.T) {
	f, current := graphFixture(t)
	command := project.GraphEditCommand{MutationID: id(t), BaseRevision: current.Project.Revision, Operations: []project.GraphOperation{contractOperation(current, contractDocument(t)), bindingOperation(current, "orders")}}
	var wait sync.WaitGroup
	codes := make(chan int, 2)
	for range 2 {
		wait.Go(func() { codes <- call(t, f.handler, "POST", graphURL(current.Project)+"/edits", command).Code })
	}
	wait.Wait()
	for range 2 {
		if code := <-codes; code != 200 {
			t.Fatalf("同键并发失败: %d", code)
		}
	}
	value := decode[project.GraphSnapshot](t, call(t, f.handler, "GET", graphURL(current.Project), nil), 200, "GraphSnapshot")
	if value.Project.Revision != "3" || len(value.Graph.Bindings) != 1 {
		t.Fatal("原键创建了重复关系")
	}
	for range 2 {
		request := project.GraphEditCommand{MutationID: id(t), BaseRevision: value.Project.Revision, Operations: []project.GraphOperation{{Type: "binding.remove", BindingID: value.Graph.Bindings[0].ID}}}
		wait.Go(func() { codes <- call(t, f.handler, "POST", graphURL(current.Project)+"/edits", request).Code })
	}
	wait.Wait()
	observed := map[int]int{}
	for range 2 {
		observed[<-codes]++
	}
	if observed[200] != 1 || observed[409] != 1 {
		t.Fatalf("同版本竞争未拒绝覆盖: %v", observed)
	}
}
