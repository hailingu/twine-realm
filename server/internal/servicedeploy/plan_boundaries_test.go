// 本文件验证缺失契约、额外依赖和损坏状态不能凭已有候选取得部署许可。
package servicedeploy

import (
	"errors"
	"strings"
	"testing"

	"github.com/hailingu/twine-realm/server/internal/catalog"
	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/servicebuild"
)

// TestDeploymentDeclarationAndCandidateBoundaries 缺失或越界输入必须返回具体阻塞，不默认忽略依赖。
func TestDeploymentDeclarationAndCandidateBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, code string
		change     func(*project.GraphSnapshot, *servicebuild.Snapshot, *Plan)
	}{
		{"无契约", "DEPLOY_CONTRACT_REQUIRED", func(g *project.GraphSnapshot, _ *servicebuild.Snapshot, _ *Plan) { g.Graph.ServiceContracts = nil }},
		{"重复契约", "DEPLOY_CONTRACT_REQUIRED", func(g *project.GraphSnapshot, _ *servicebuild.Snapshot, _ *Plan) {
			g.Graph.ServiceContracts = append(g.Graph.ServiceContracts, g.Graph.ServiceContracts[0])
		}},
		{"引用改变", "DEPLOY_CONTRACT_CHANGED", func(g *project.GraphSnapshot, _ *servicebuild.Snapshot, _ *Plan) {
			g.Graph.ServiceContracts[0].Reference.Version = "2.0.0"
		}},
		{"契约内容非法", "INVALID_SERVICE_CONTRACT", func(g *project.GraphSnapshot, _ *servicebuild.Snapshot, _ *Plan) {
			g.Graph.ServiceContracts[0].Document.ID = ""
		}},
		{"额外必需依赖", "DEPLOY_DEPENDENCY_UNSUPPORTED", func(g *project.GraphSnapshot, _ *servicebuild.Snapshot, _ *Plan) { additionalRequiredDependency(t, g) }},
		{"候选跨项目", "DEPLOY_CANDIDATE_INVALID", func(_ *project.GraphSnapshot, b *servicebuild.Snapshot, p *Plan) { b.Target.ProjectID = p.ServiceID }},
		{"未固定源码", "DEPLOY_SOURCE_CHANGED", func(_ *project.GraphSnapshot, b *servicebuild.Snapshot, _ *Plan) { b.CurrentSource.PinnedCommit = nil }},
		{"IPv6私网", "DEPLOY_ENVIRONMENT_CONFLICT", func(_ *project.GraphSnapshot, _ *servicebuild.Snapshot, p *Plan) {
			p.Database.NetworkCIDR = "2001:db8::/64"
		}},
		{"非法请求", "INVALID_REQUEST", func(_ *project.GraphSnapshot, _ *servicebuild.Snapshot, p *Plan) { p.Request.HostPort = 80 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := planFixture(t)
			graph, build := planInputs(t, p)
			test.change(&graph, &build, &p)
			_, err := MakePlan(graph, build, p.Request, p.Database, p.Environment)
			var failure *project.Error
			if !errors.As(err, &failure) || failure.Code != test.code {
				t.Fatal("部署边界未返回明确阻塞", test.code, err)
			}
		})
	}
}

// additionalRequiredDependency 通过真实目录重新计算契约引用，专门验证完整但不受支持的依赖组合。
func additionalRequiredDependency(t *testing.T, g *project.GraphSnapshot) {
	t.Helper()
	document := &g.Graph.ServiceContracts[0].Document
	port := document.Ports[0]
	port.ID = "extra"
	port.Required = true
	document.Ports = append(document.Ports, port)
	// 契约引用由测试调用方另行核实，不能用过期摘要误判为依赖检查通过。
	definitions, err := catalog.New()
	if err != nil {
		t.Fatal(err)
	}
	_, reference, err := definitions.ValidateServiceDocument(*document)
	if err != nil {
		t.Fatal(err)
	}
	g.Graph.ServiceContracts[0].Reference = reference
	g.Graph.Bindings[0].SourceDefinition = reference
}

// TestDeploymentInvalidObservationsAndCompletion 不接受未知状态、坏时间、无交叉证据的ready或无效终态诊断。
func TestDeploymentInvalidObservationsAndCompletion(t *testing.T) {
	valid := Observation{State: "running", MigrationIntegrity: "verified", ObservedAt: "2026-11-06T00:00:00Z"}
	for _, change := range []func(*Observation){
		func(v *Observation) { v.State = "healthy-ish" },
		func(v *Observation) { v.MigrationIntegrity = "guessed" },
		func(v *Observation) { v.ObservedAt = "invalid" },
		func(v *Observation) { v.Ready = true },
	} {
		value := valid
		change(&value)
		if ValidateObservation(value) == nil || ValidateTargetObservation(Target{}, value) == nil || ValidateCompletion(Completion{State: "failed", Code: "FAILED", Message: "安全诊断", Observation: &value}) == nil {
			t.Fatal("非法采集被存为终态证据")
		}
	}
	for _, value := range []Completion{
		{State: "executing", Code: "PENDING", Message: "仍在途"},
		{State: "failed", Code: "raw secret", Message: "无效诊断码"},
		{State: "failed", Code: "FAILED", Message: ""},
		{State: "failed", Code: "FAILED", Message: strings.Repeat("x", 1025)},
	} {
		if ValidateCompletion(value) == nil {
			t.Fatal("无效诊断或非终态被结算")
		}
	}
}
