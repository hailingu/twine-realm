//go:build integration

// 本文件验证禁用、非法输入与缺失历史的真实HTTP/SQL边界，不启动部署或生成业务凭据。
package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/hailingu/twine-realm/server/internal/catalog"
	"github.com/hailingu/twine-realm/server/internal/gitworkspace"
	"github.com/hailingu/twine-realm/server/internal/httpapi"
	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// disabledDeploymentHandler 装配真实存储和禁用适配器，历史查询不能依赖Docker可用。
func disabledDeploymentHandler(t *testing.T, f *fixture) http.Handler {
	t.Helper()
	definitions, err := catalog.New()
	if err != nil {
		t.Fatal(err)
	}
	inspector, err := gitworkspace.New([]string{f.root})
	if err != nil {
		t.Fatal(err)
	}
	service := project.NewService(f.database, f.database, f.database, inspector, definitions)
	handler, err := httpapi.New(service, host, []string{"http://" + host}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)), httpapi.Features{Deploy: servicedeploy.NewService(f.database, f.database, nil)})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

// TestDeploymentDisabledAndMissingHTTP 未装配不会模拟成功，禁用后仍以真实持久历史作答。
func TestDeploymentDisabledAndMissingHTTP(t *testing.T) {
	f := openFixture(t)
	projectID, serviceID, operationID, mutationID := id(t), id(t), id(t), id(t)
	route := "/api/v4/projects/" + projectID + "/services/" + serviceID
	for _, endpoint := range []struct{ method, path string }{
		{"GET", "/api/v4/deployment/capabilities"}, {"POST", route + "/deployment/preview"}, {"POST", route + "/deployments"},
		{"GET", route + "/deployment"}, {"GET", route + "/deployments/" + operationID}, {"GET", "/api/v4/deployment-mutations/" + mutationID},
	} {
		buildFailure(t, call(t, f.handler, endpoint.method, endpoint.path, map[string]any{}), 503, "DEPLOY_DISABLED")
	}
	f.handler = disabledDeploymentHandler(t, f)
	capability := decode[servicedeploy.Capability](t, call(t, f.handler, "GET", "/api/v4/deployment/capabilities", nil), 200, "DeployCapability")
	if capability.Enabled || capability.Adapter != "disabled" {
		t.Fatal("未启用适配器却宣称可部署")
	}
	request := servicedeploy.PreviewRequest{BaseRevision: "1", BaseGeneration: "0", CandidateID: id(t), BindingID: id(t), HostPort: 8081, Configuration: map[string]any{}}
	buildFailure(t, call(t, f.handler, "POST", route+"/deployment/preview", request), 503, "DEPLOY_DISABLED")
	command := servicedeploy.Command{MutationID: mutationID, Preview: request, PlanDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	buildFailure(t, call(t, f.handler, "POST", route+"/deployments", command), 503, "DEPLOY_DISABLED")
	for _, path := range []string{route + "/deployment/preview", route + "/deployments"} {
		buildFailure(t, call(t, f.handler, "POST", path, map[string]any{"unexpected": true}), 400, "INVALID_REQUEST")
	}
	buildFailure(t, call(t, f.handler, "GET", route+"/deployment", nil), 404, "SERVICE_NOT_FOUND")
	buildFailure(t, call(t, f.handler, "GET", route+"/deployments/"+operationID, nil), 404, "DEPLOY_OPERATION_NOT_FOUND")
	buildFailure(t, call(t, f.handler, "GET", "/api/v4/deployment-mutations/"+mutationID, nil), 404, "MUTATION_NOT_FOUND")
	if _, _, err := f.database.DeploymentReceipt(context.Background(), mutationID); err == nil {
		t.Fatal("禁用请求仍产生了持久受理")
	}
}
