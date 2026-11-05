//go:build integration

// 本文件在实际 SQL 写入中制造失败，并核实跨项目端点与历史版本边界。
package httpapi_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// TestGraphRejectsForeignEndpoints 外项目组件真实存在时也不得跨项目连线或声明契约。
func TestGraphRejectsForeignEndpoints(t *testing.T) {
	f, current := graphFixture(t)
	_, other := graphFixture(t)
	saved := editGraph(t, f, current.Project, contractOperation(current, contractDocument(t)), bindingOperation(current, "orders"))
	for _, endpoint := range []string{"target", "source", "contract"} {
		operation := bindingOperation(current, "orders")
		operation.Type, operation.BindingID = "binding.reconnect", saved.Graph.Bindings[0].ID
		switch endpoint {
		case "target":
			operation.Target.ComponentID = other.Graph.Middlewares[0].ID
		case "source":
			operation.Source.ComponentID = other.Project.Nodes[0].ID
		case "contract":
			operation = contractOperation(other, contractDocument(t))
		}
		command := project.GraphEditCommand{MutationID: id(t), BaseRevision: saved.Project.Revision, Operations: []project.GraphOperation{operation}}
		decode[any](t, call(t, f.handler, "POST", graphURL(current.Project)+"/edits", command), 400, "ErrorResponse")
		opened := decode[project.GraphSnapshot](t, call(t, f.handler, "GET", graphURL(current.Project), nil), 200, "GraphSnapshot")
		if !reflect.DeepEqual(saved.Graph, &opened.Graph) || saved.Project.Revision != opened.Project.Revision {
			t.Fatal("外项目端点拒绝后丢失原关系")
		}
	}
}

// TestGraphHistoricalVersionCannotChange 切换活动版本后原版本仍不可改义。
func TestGraphHistoricalVersionCannotChange(t *testing.T) {
	f, current := graphFixture(t)
	document := contractDocument(t)
	saved := editGraph(t, f, current.Project, contractOperation(current, document))
	document.Version = "1.1.0"
	saved = editGraph(t, f, saved.Project, contractOperation(current, document))
	document.Version = "1.0.0"
	document.ConfigurationSchema["description"] = "不能改写历史内容"
	command := project.GraphEditCommand{MutationID: id(t), BaseRevision: saved.Project.Revision, Operations: []project.GraphOperation{contractOperation(current, document)}}
	decode[any](t, call(t, f.handler, "POST", graphURL(current.Project)+"/edits", command), 409, "ErrorResponse")
	opened := decode[project.GraphSnapshot](t, call(t, f.handler, "GET", graphURL(current.Project), nil), 200, "GraphSnapshot")
	if !reflect.DeepEqual(&opened.Graph, saved.Graph) {
		t.Fatal("已退出活动状态的历史版本被改义")
	}
}

// TestGraphSQLRollbackAndOriginalKeyRecovery 在契约已写、绑定尚未完成时使 SQL 失败，所有写入与回执须回滚。
func TestGraphSQLRollbackAndOriginalKeyRecovery(t *testing.T) {
	f, current := graphFixture(t)
	database, err := sql.Open("mysql", f.dsn)
	if err != nil {
		t.Fatal(err)
	}
	constraint := "graph_test_" + strings.ReplaceAll(id(t), "-", "")
	installed := false
	t.Cleanup(func() {
		if installed {
			if _, err := database.Exec("ALTER TABLE database_bindings DROP CHECK " + constraint); err != nil {
				t.Error(err)
			}
		}
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	statement := "ALTER TABLE database_bindings ADD CONSTRAINT " + constraint + " CHECK (project_id <> '" + current.Project.ID + "')"
	if _, err := database.Exec(statement); err != nil {
		t.Fatal(err)
	}
	installed = true
	command := project.GraphEditCommand{MutationID: id(t), BaseRevision: current.Project.Revision, Operations: []project.GraphOperation{contractOperation(current, contractDocument(t)), bindingOperation(current, "orders")}}
	decode[any](t, call(t, f.handler, "POST", graphURL(current.Project)+"/edits", command), 503, "ErrorResponse")
	opened := decode[project.GraphSnapshot](t, call(t, f.handler, "GET", graphURL(current.Project), nil), 200, "GraphSnapshot")
	if !reflect.DeepEqual(opened, current) {
		t.Fatal("SQL 失败留下部分图声明或修订")
	}
	var versions int
	if err := database.QueryRow("SELECT COUNT(*) FROM service_contract_versions WHERE project_id=?", current.Project.ID).Scan(&versions); err != nil || versions != 0 {
		t.Fatalf("SQL 回滚未清除未发布契约版本：%v", err)
	}
	decode[any](t, call(t, f.handler, "GET", "/api/v2/graph-mutations/"+command.MutationID, nil), 404, "ErrorResponse")
	if _, err := database.Exec("ALTER TABLE database_bindings DROP CHECK " + constraint); err != nil {
		t.Fatal(err)
	}
	installed = false
	saved := decode[project.Receipt](t, call(t, f.handler, "POST", graphURL(current.Project)+"/edits", command), 200, "GraphReceipt")
	if len(saved.Graph.Bindings) != 1 || saved.Project.Revision != "3" {
		t.Fatal("SQL 恢复后原键未正确完成唯一关系")
	}
}
