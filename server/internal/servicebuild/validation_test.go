// 本文件对持久边界拒绝不完整、过期身份和跨操作候选，不将结构测试当作实际工具成功。
package servicebuild

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/engineering"
	"github.com/hailingu/twine-realm/server/internal/project"
)

// metadataID 只产生独立结构验证身份，与任何真实Docker资源无关。
func metadataID(t *testing.T) string {
	t.Helper()
	value, err := project.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// validMetadata 为结构验证建立完整目标和候选；真实生成另由Docker/Git集成测试证明。
func validMetadata(t *testing.T) (Lease, Completion) {
	t.Helper()
	catalog, err := engineering.Load()
	if err != nil {
		t.Fatal(err)
	}
	source := Source{metadataID(t), "1", metadataID(t), metadataID(t), "/test/service", strings.Repeat("a", 40)}
	target := Target{ID: metadataID(t), ProjectID: source.ProjectID, ServiceID: source.ServiceID, Generation: "1", CanvasRevision: "1", InstallationID: metadataID(t), Source: source, Profile: catalog.Identity(), Environment: testBuildEnvironment(t), Policy: DefaultPolicy(), CreatedAt: Timestamp(time.Now())}
	target.InputDigest, err = InputFingerprint(source, target.Profile, target.Environment, target.Policy, target.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
	lease := Lease{Target: target, Operation: Operation{ID: metadataID(t)}}
	candidate := &Candidate{ID: metadataID(t), TargetID: target.ID, OperationID: lease.Operation.ID, InputDigest: target.InputDigest, SourceDigest: ContentDigest([]byte("source")), Source: source, Profile: target.Profile, Template: catalog.Profile().Template, Environment: target.Environment, Policy: target.Policy, Checks: []Check{}, Migrations: []Migration{{Path: "internal/schema/migrations/000001_initial.up.sql", Digest: ContentDigest([]byte("sql"))}}, CreatedAt: Timestamp(time.Now())}
	for _, name := range stepNames[:len(stepNames)-1] {
		candidate.Checks = append(candidate.Checks, Check{Name: name, State: "succeeded", ToolVersion: "verified-tool-version", OutputDigest: ContentDigest(nil), CompletedAt: Timestamp(time.Now())})
	}
	candidate.MigrationDigest, err = Fingerprint(candidate.Migrations)
	if err != nil {
		t.Fatal(err)
	}
	digest := ContentDigest([]byte("image"))
	candidate.Image = Image{ID: digest, ManifestDigest: ContentDigest([]byte("manifest")), ConfigDigest: digest, Tag: "test-only:local", BinaryDigest: ContentDigest([]byte("binary"))}
	return lease, Completion{State: "succeeded", Code: "BUILT", Message: "结构验证", Candidate: candidate}
}

// TestIncompleteBuildProofRejected 不同类型损坏均保持无可接受成功候选。
func TestIncompleteBuildProofRejected(t *testing.T) {
	lease, complete := validMetadata(t)
	if err := ValidateCompletion(lease, complete); err != nil {
		t.Fatal(err)
	}
	changes := []func(*Candidate){
		func(c *Candidate) { c.ID = "invalid" }, func(c *Candidate) { c.CreatedAt = "invalid" }, func(c *Candidate) { c.Template.Version = "unsupported" },
		func(c *Candidate) { c.TargetID = metadataID(t) }, func(c *Candidate) { c.OperationID = metadataID(t) }, func(c *Candidate) { c.InputDigest = ContentDigest(nil) },
		func(c *Candidate) { c.Source.RepositoryID = metadataID(t) }, func(c *Candidate) { c.Profile.Digest = ContentDigest(nil) }, func(c *Candidate) { c.Environment.DaemonID = "foreign" },
		func(c *Candidate) { c.Policy.MemoryMiB++ }, func(c *Candidate) { c.Image.ID = ContentDigest(nil) }, func(c *Candidate) { c.Image.ManifestDigest = "tag" },
		func(c *Candidate) { c.SourceDigest = "invalid" }, func(c *Candidate) { c.Checks = c.Checks[1:] }, func(c *Candidate) { c.Checks = append(c.Checks, c.Checks[0]) },
		func(c *Candidate) { c.Checks[0].State = "failed" }, func(c *Candidate) { c.Checks[0].OutputDigest = "invalid" }, func(c *Candidate) { c.Checks[0].ToolVersion = "" },
		func(c *Candidate) { c.Checks[0].CompletedAt = "invalid" }, func(c *Candidate) { c.Migrations = nil }, func(c *Candidate) { c.Migrations[0].Path = "../outside.sql" },
		func(c *Candidate) { c.Migrations = append(c.Migrations, c.Migrations[0]) }, func(c *Candidate) { c.MigrationDigest = ContentDigest(nil) },
		func(c *Candidate) { c.Checks[0].ToolVersion = strings.Repeat("x", 50*1024) },
	}
	for index, change := range changes {
		value := complete
		value.Candidate = cloneCandidate(t, complete.Candidate)
		change(value.Candidate)
		if err := ValidateCompletion(lease, value); err == nil {
			t.Fatalf("损坏证明被接受：%d", index)
		}
	}
}

// cloneCandidate 保持同一原输入，每个损坏场景只修改自己的材料。
func cloneCandidate(t *testing.T, value *Candidate) *Candidate {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result Candidate
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return &result
}

// TestBuildTerminalAndInputBoundaries 失败不能附带候选，布局不改变输入而固定commit确实改变。
func TestBuildTerminalAndInputBoundaries(t *testing.T) {
	lease, value := validMetadata(t)
	for _, state := range []string{"failed", "cancelled", "needs_attention", "executing"} {
		value.State = state
		if err := ValidateCompletion(lease, value); err == nil {
			t.Fatal("不合法终态携带候选被接受")
		}
	}
	value.State, value.Candidate = "failed", nil
	if err := ValidateCompletion(lease, value); err != nil {
		t.Fatal(err)
	}
	value.Code = "private text"
	if err := ValidateCompletion(lease, value); err == nil {
		t.Fatal("自由诊断被接受")
	}
	value.Code, value.Message = "FAILED", ""
	if err := ValidateCompletion(lease, value); err == nil {
		t.Fatal("空说明被接受")
	}

}

// TestBuildInputIdentity 固定输入变化与非法入口各有显式判定，不使用布局revision替代内容。
func TestBuildInputIdentity(t *testing.T) {
	lease, _ := validMetadata(t)
	source := lease.Target.Source
	source.ProjectRevision = "2"
	digest, err := InputFingerprint(source, lease.Target.Profile, lease.Target.Environment, lease.Target.Policy, lease.Target.InstallationID)
	if err != nil || digest != lease.Target.InputDigest {
		t.Fatal("布局revision使固定源码失效")
	}
	source.PinnedCommit = strings.Repeat("b", 40)
	changed, err := InputFingerprint(source, lease.Target.Profile, lease.Target.Environment, lease.Target.Policy, lease.Target.InstallationID)
	if err != nil || changed == digest {
		t.Fatal("commit变化未使输入摘要失效")
	}
	if _, err := Fingerprint(make(chan int)); err == nil {
		t.Fatal("不可序列化输入被接受")
	}
	if number("") || number("overflow18446744073709551616") || validIDs("wrong") || ValidStep(Step{Name: "shell", State: "executing"}) {
		t.Fatal("非法入口边界被放行")
	}
	if !ValidStep(Step{Name: "source", State: "succeeded"}) || !Terminal("needs_attention") {
		t.Fatal("合法恢复边界失效")
	}
	if _, err := (&Driver{}).Execute(context.Background(), Target{}, "invalid", nil, nil); err == nil {
		t.Fatal("非法目标进入执行器")
	}
}
