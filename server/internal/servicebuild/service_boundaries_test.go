// 本文件验证公开用例的失联、非法请求和历史候选语义；真实持久化另由SQL与浏览器验收。
package servicebuild

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// unavailableCommands 模拟存储故障，不能以空值或禁用执行器掩盖原请求未知。
type unavailableCommands struct{}

// BuildReceipt 保持存储失败，不捏造未受理。
func (unavailableCommands) BuildReceipt(context.Context, string) (Receipt, [32]byte, error) {
	return Receipt{}, [32]byte{}, errors.New("受控存储不可用")
}

// AcceptBuild 不允许进入无持久所有者的受理。
func (unavailableCommands) AcceptBuild(context.Context, AcceptedInput) (Receipt, error) {
	return Receipt{}, errors.New("受控存储不可用")
}

// CancelBuild 不允许把存储未知当成已取消。
func (unavailableCommands) CancelBuild(context.Context, string, string, string, CancelCommand, [32]byte) (Receipt, error) {
	return Receipt{}, errors.New("受控存储不可用")
}

// sourceUnavailable 为候选可用性提供只读失联边界，不扫描宿主仓库。
type sourceUnavailable struct{ unavailable bool }

// VerifyCommit 只表达固定对象当前是否可用，不修改关联。
func (value sourceUnavailable) VerifyCommit(context.Context, string, string) error {
	if value.unavailable {
		return project.Failure("SOURCE_UNAVAILABLE", "固定源码失联")
	}
	return nil
}

// TestPublicBuildRequestBoundaries 非法身份和存储故障不能降级为新受理或空成功。
func TestPublicBuildRequestBoundaries(t *testing.T) {
	service, err := NewService(unavailableCommands{}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if service.Capability().Enabled {
		t.Fatal("缺省开启了执行器")
	}
	ctx := context.Background()
	lease, _ := validMetadata(t)
	command := Command{MutationID: metadataID(t), BaseRevision: "1", BaseGeneration: "0", Commit: lease.Target.Source.PinnedCommit, Profile: lease.Target.Profile}
	if _, err := service.Accept(ctx, "invalid", lease.Target.ServiceID, command); err == nil {
		t.Fatal("非法项目被受理")
	}
	if _, err := service.Accept(ctx, lease.Target.ProjectID, lease.Target.ServiceID, command); err == nil {
		t.Fatal("存储失败被伪报未受理")
	}
	if _, err := service.Cancel(ctx, "invalid", lease.Target.ServiceID, lease.Operation.ID, CancelCommand{}); err == nil {
		t.Fatal("非法取消被受理")
	}
	if _, err := service.Cancel(ctx, lease.Target.ProjectID, lease.Target.ServiceID, lease.Operation.ID, CancelCommand{MutationID: metadataID(t), BaseVersion: "1"}); err == nil {
		t.Fatal("未知取消被伪报成功")
	}
	if _, err := service.Receipt(ctx, "invalid"); err == nil {
		t.Fatal("非法原键被查询")
	}
	if _, err := service.Receipt(ctx, metadataID(t)); err == nil {
		t.Fatal("存储失败回执被伪造")
	}
	if _, err := service.Operation(ctx, "invalid", lease.Target.ServiceID, lease.Operation.ID); err == nil {
		t.Fatal("非法历史操作被查询")
	}
	if _, err := service.Snapshot(ctx, "invalid", lease.Target.ServiceID); err == nil {
		t.Fatal("非法投影被查询")
	}
}

// candidateFixture 将同一固定输入关联当前私有安装，仅用于可用性分支核实。
func candidateFixture(t *testing.T, driver *Driver) (*Service, Snapshot) {
	t.Helper()
	lease, completion := validMetadata(t)
	lease.Target.InstallationID = driver.InstallationID()
	digest, err := InputFingerprint(lease.Target.Source, lease.Target.Profile, lease.Target.Environment, lease.Target.Policy, driver.InstallationID())
	if err != nil {
		t.Fatal(err)
	}
	completion.Candidate.InputDigest, lease.Target.InputDigest = digest, digest
	source := lease.Target.Source
	commit := source.PinnedCommit
	value := Snapshot{SchemaVersion: 1, ProjectID: source.ProjectID, ServiceID: source.ServiceID, Generation: "1", Target: &lease.Target, Operation: &Operation{ID: completion.Candidate.OperationID, State: "succeeded"}, Candidate: completion.Candidate, CurrentSource: project.SourceVersion{ProjectID: source.ProjectID, ProjectRevision: "1", ServiceID: source.ServiceID, RepositoryID: source.RepositoryID, RepositoryPath: source.RepositoryPath, PinnedCommit: &commit}}
	service, err := NewService(nil, nil, nil, sourceUnavailable{}, driver)
	if err != nil {
		t.Fatal(err)
	}
	return service, value
}

// TestHistoricalCandidateAvailability 原固定输入仍保留，源码、工具或镜像未知不能显示当前可用。
func TestHistoricalCandidateAvailability(t *testing.T) {
	driver, _, _ := sandboxFixture(t)
	service, value := candidateFixture(t, driver)
	if !service.Capability().Enabled {
		t.Fatal("显式配置未报告")
	}
	ctx := context.Background()
	service.driver = nil
	if service.candidateCode(ctx, value) != "BUILD_DISABLED" {
		t.Fatal("禁用时冒充可用")
	}
	service.driver = driver
	copy := value
	copy.Operation = nil
	if service.candidateCode(ctx, copy) != "HISTORICAL_CANDIDATE" {
		t.Fatal("历史候选被当成当前操作")
	}
	copy = value
	copy.CurrentSource.PinnedCommit = nil
	if service.candidateCode(ctx, copy) != "HISTORICAL_CANDIDATE" {
		t.Fatal("未固定源码被当成当前输入")
	}
	copy = value
	copy.CurrentSource.RepositoryID = metadataID(t)
	if service.candidateCode(ctx, copy) != "INPUT_CHANGED" {
		t.Fatal("仓库关联变化未使候选失效")
	}
	service.verifier = sourceUnavailable{true}
	if service.candidateCode(ctx, value) != "BUILD_SOURCE_UNAVAILABLE" {
		t.Fatal("失联源码被当成可用")
	}
	service.verifier = sourceUnavailable{}
	driver.execute = fakeEnvironmentCommand
	if service.candidateCode(ctx, value) != "BUILD_IMAGE_UNCONFIRMED" {
		t.Fatal("未知镜像被当成可用")
	}
	driver.execute = nilEnvironmentCommand
	if service.candidateCode(ctx, value) != "BUILD_ENVIRONMENT_UNCONFIRMED" {
		t.Fatal("未知工具环境被当成可用")
	}
}

// nilEnvironmentCommand 只模拟工具读取失败，不允许产生任何资源。
func nilEnvironmentCommand(context.Context, time.Duration, bool, ...string) ([]byte, error) {
	return nil, errors.New("受控工具不可用")
}
