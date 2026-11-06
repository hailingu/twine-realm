// 本文件使用真实公开预览的固定结构证明契约与身份对齐，不冒称测试时执行了Docker。
package servicedeploy

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/hailingu/twine-realm/server/contract"
	"github.com/hailingu/twine-realm/server/internal/project"
	"github.com/hailingu/twine-realm/server/internal/servicebuild"
)

// planFixture 每个场景独立解码，保留真实生产者字段与规范身份。
func planFixture(t *testing.T) Plan {
	t.Helper()
	data, err := os.ReadFile("../../contract/v4/fixtures/preview.json")
	if err != nil {
		t.Fatal(err)
	}
	var value Plan
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

// planInputs 将公开固定输入还原成结构读屏障，未伪造任何外部运行结果。
func planInputs(t *testing.T, p Plan) (project.GraphSnapshot, servicebuild.Snapshot) {
	t.Helper()
	var document project.ServiceContractDocument
	data, err := os.ReadFile("../../contract/v2/fixtures/service-contract.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	c := p.Candidate
	s := c.Source
	target := servicebuild.Target{ID: c.TargetID, ProjectID: p.ProjectID, ServiceID: p.ServiceID, Generation: "1", CanvasRevision: s.ProjectRevision, InstallationID: p.Environment.BuildInstallationID, Source: s, Profile: c.Profile, Environment: c.Environment, Policy: c.Policy, InputDigest: c.InputDigest, CreatedAt: c.CreatedAt}
	op := servicebuild.Operation{ID: c.OperationID, TargetID: c.TargetID, ProjectID: p.ProjectID, ServiceID: p.ServiceID, Version: "30", State: "succeeded", Code: "CHECKED", Message: "结构证明", Candidate: &c}
	build := servicebuild.Snapshot{SchemaVersion: 1, ProjectID: p.ProjectID, ServiceID: p.ServiceID, Generation: "1", Target: &target, Operation: &op, Candidate: &c, CurrentSource: project.SourceVersion{ProjectID: p.ProjectID, ServiceID: p.ServiceID, ProjectRevision: p.Request.BaseRevision, RepositoryID: s.RepositoryID, RepositoryPath: s.RepositoryPath, PinnedCommit: &c.Source.PinnedCommit}}
	graph := project.GraphSnapshot{Project: project.Project{SchemaVersion: 1, ID: p.ProjectID, Revision: p.Request.BaseRevision, Nodes: []project.Node{{ID: p.ServiceID, DisplayName: p.ServiceName, Repository: project.Repository{ID: s.RepositoryID, Path: s.RepositoryPath, HeadCommit: &c.Source.PinnedCommit}}}}, Graph: project.GraphState{SchemaVersion: 2, ServiceContracts: []project.ServiceContract{{ServiceID: p.ServiceID, Reference: p.Contract, Document: document}}, Bindings: []project.DatabaseBinding{p.Binding}}}
	return graph, build
}

// TestDeploymentProducerContract 捕捉生产者真实使用native、带下划线用途账号和mysql-wire/10的契约差异。
func TestDeploymentProducerContract(t *testing.T) {
	p := planFixture(t)
	if err := ValidatePlan(p); err != nil {
		t.Fatal("真实公开预览自身摘要不一致", err)
	}
	graph, build := planInputs(t, p)
	value, err := MakePlan(graph, build, p.Request, p.Database, p.Environment)
	if err != nil || !reflect.DeepEqual(value, p) {
		t.Fatal("当前计划生成与已发布画布不一致", err)
	}
	assertProducerSchema(t, "DeployPlan", value)
	assertProducerSchema(t, "DeployCapability", NewService(nil, nil, nil).Capability())
	p.Environment.BindSourceMode = "native"
	p.RuntimeDigest, err = runtimeDigest(p)
	if err != nil {
		t.Fatal(err)
	}
	p.Digest, err = planDigest(p)
	if err != nil {
		t.Fatal(err)
	}
	assertProducerSchema(t, "DeployPlan", p)
}

// assertProducerSchema 让每个真实生产者对象穿过发布校验器，格式与机器字段另行裁决。
func assertProducerSchema(t *testing.T, name string, value any) {
	t.Helper()
	schemas, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := schemas[name].Validate(decoded); err != nil {
		t.Fatalf("真实生产者%s不满足机器契约：%v", name, err)
	}
}

// TestDeploymentRejectsChangedEvidence 候选、源码、连线或服务自有schema改变时预览不能沿用。
func TestDeploymentRejectsChangedEvidence(t *testing.T) {
	cases := []struct {
		name   string
		change func(*project.GraphSnapshot, *servicebuild.Snapshot, *Plan)
	}{
		{"revision", func(g *project.GraphSnapshot, _ *servicebuild.Snapshot, _ *Plan) { g.Project.Revision = "999" }},
		{"missing-candidate", func(_ *project.GraphSnapshot, b *servicebuild.Snapshot, _ *Plan) { b.Candidate = nil }},
		{"incomplete-checks", func(_ *project.GraphSnapshot, b *servicebuild.Snapshot, _ *Plan) { b.Candidate.Checks = nil }},
		{"changed-source", func(_ *project.GraphSnapshot, b *servicebuild.Snapshot, _ *Plan) {
			b.CurrentSource.RepositoryPath = "/different/service"
		}},
		{"unknown-protocol", func(g *project.GraphSnapshot, _ *servicebuild.Snapshot, _ *Plan) {
			g.Graph.Bindings[0].Contract = "mysql-wire/1"
		}},
		{"extra-binding", func(g *project.GraphSnapshot, _ *servicebuild.Snapshot, _ *Plan) {
			g.Graph.Bindings = append(g.Graph.Bindings, g.Graph.Bindings[0])
		}},
		{"required-configuration", func(g *project.GraphSnapshot, _ *servicebuild.Snapshot, _ *Plan) {
			g.Graph.ServiceContracts[0].Document.ConfigurationSchema = map[string]any{"type": "object", "required": []any{"businessSetting"}}
		}},
		{"custom-unmapped-configuration", func(_ *project.GraphSnapshot, _ *servicebuild.Snapshot, p *Plan) {
			p.Request.Configuration = map[string]any{"secret": "synthetic-private"}
		}},
		{"different-database", func(_ *project.GraphSnapshot, _ *servicebuild.Snapshot, p *Plan) {
			p.Database.ComponentID = p.ServiceID
		}},
		{"root-user", func(_ *project.GraphSnapshot, _ *servicebuild.Snapshot, p *Plan) { p.Environment.UID = 0 }},
		{"different-daemon", func(_ *project.GraphSnapshot, _ *servicebuild.Snapshot, p *Plan) { p.Environment.DaemonID = "foreign" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			p := planFixture(t)
			g, b := planInputs(t, p)
			test.change(&g, &b, &p)
			if _, err := MakePlan(g, b, p.Request, p.Database, p.Environment); err == nil {
				t.Fatal("改变的输入被沿用为部署许可")
			}
		})
	}
}

// TestDeploymentDigestAndCompletionBoundaries 布局并发版本与MySQL目标代次不重命名物理计划，端口与真实就绪确实影响结果。
func TestDeploymentDigestAndCompletionBoundaries(t *testing.T) {
	p := planFixture(t)
	original := p.RuntimeDigest
	p.Request.BaseRevision = "99"
	p.Request.BaseGeneration = "7"
	p.Database.Generation = "8"
	digest, err := runtimeDigest(p)
	if err != nil || digest != original {
		t.Fatal("纯并发版本改变了物理计划")
	}
	p.Request.HostPort++
	changed, err := runtimeDigest(p)
	if err != nil || changed == original {
		t.Fatal("端口改变没有改变运行身份")
	}
	if err := ValidatePlan(p); err == nil {
		t.Fatal("已改内容沿用旧摘要")
	}
	if err := ValidateCompletion(Completion{State: "succeeded", Code: "READY", Message: "结构测试"}); err == nil {
		t.Fatal("缺实际就绪证据仍成功")
	}
	if err := ValidateCompletion(Completion{State: "needs_attention", Code: "UNKNOWN", Message: "明确等待核实"}); err != nil {
		t.Fatal(err)
	}
	if ValidStep(Step{Name: "shell", State: "executing"}) || Terminal("executing") {
		t.Fatal("未知阶段或在途状态被当成终态")
	}
}
