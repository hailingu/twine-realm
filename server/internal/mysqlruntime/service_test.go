// 本文件验证受理前置、原键和能力边界，SQL原子性由集成测试独立验证。
package mysqlruntime

import (
	"context"
	"errors"
	"testing"

	"github.com/hailingu/twine-realm/server/internal/catalog"
	"github.com/hailingu/twine-realm/server/internal/project"
)

// servicePorts 记录是否越过受理屏障；所有返回均由具体时序测试明确给定。
type servicePorts struct {
	canvas   project.CanvasSnapshot
	snapshot Snapshot
	receipt  *Receipt
	hash     [32]byte
	accepted *AcceptedInput
}

// AcceptRuntime 只记录是否被调用，不代替持久化证明。
func (ports *servicePorts) AcceptRuntime(_ context.Context, input AcceptedInput) (Receipt, error) {
	ports.accepted = &input
	return Receipt{}, nil
}

// RuntimeReceipt 模拟未受理或已确认原键，不查询当前画布。
func (ports *servicePorts) RuntimeReceipt(context.Context, string) (Receipt, [32]byte, error) {
	if ports.receipt == nil {
		return Receipt{}, [32]byte{}, project.Failure("MUTATION_NOT_FOUND", "未受理")
	}
	return *ports.receipt, ports.hash, nil
}

// BindRuntime 在本受理测试中没有外部安装副作用。
func (*servicePorts) BindRuntime(context.Context, string, string) error { return nil }

// Canvas 返回测试明确给定的已保存声明版本。
func (ports *servicePorts) Canvas(context.Context, string) (project.CanvasSnapshot, error) {
	return ports.canvas, nil
}

// RuntimeSnapshot 返回测试明确给定的原固定实例。
func (ports *servicePorts) RuntimeSnapshot(context.Context, string) (Snapshot, error) {
	return ports.snapshot, nil
}

// RuntimeOperation 保留查询边界，失败路径不凭空生成操作。
func (*servicePorts) RuntimeOperation(context.Context, string, string) (Operation, error) {
	return Operation{}, project.Failure("RUNTIME_OPERATION_NOT_FOUND", "未找到")
}

// RuntimeEvents 查询端口返回空页，不承担此测试的持久化验证。
func (*servicePorts) RuntimeEvents(context.Context, string, string) (EventPage, error) {
	return EventPage{Events: []Event{}}, nil
}

// serviceFixture 使用发布目录，构造明确的已保存配置与原请求。
func serviceFixture(t *testing.T) (*Service, *servicePorts, Resource, Command) {
	t.Helper()
	driver, _ := openPrivateFixture(t)
	resource := configuredResource(t, driver)
	definitions, err := catalog.New()
	if err != nil {
		t.Fatal(err)
	}
	ports := &servicePorts{canvas: project.CanvasSnapshot{Project: project.Project{ID: resource.ProjectID, Revision: "1"}, Canvas: project.CanvasState{SchemaVersion: 1, Middlewares: []project.MiddlewareNode{{ID: resource.ComponentID, Definition: resource.Definition, Configuration: resource.Configuration}}}}}
	command := Command{MutationID: resource.ID, BaseRevision: "1", BaseGeneration: "0", Desired: "running"}
	return NewService(ports, ports, ports, definitions, driver), ports, resource, command
}

// expectServiceCode 不用安全文案的易变措辞判断领域失败。
func expectServiceCode(t *testing.T, err error, code string) {
	t.Helper()
	var failure *project.Error
	if !errors.As(err, &failure) || failure.Code != code {
		t.Fatalf("期望安全代码%s，实际%v", code, err)
	}
}

// TestRuntimeCandidateMustMatchCanvasRevision 资源候选必须来自同一声明版本，不能在随后加锁时偷换出处。
func TestRuntimeCandidateMustMatchCanvasRevision(t *testing.T) {
	service, ports, resource, command := serviceFixture(t)
	ports.canvas.Project.Revision = "2"
	_, err := service.Accept(context.Background(), resource.ProjectID, resource.ComponentID, command)
	expectServiceCode(t, err, "REVISION_CONFLICT")
	if ports.accepted != nil {
		t.Fatal("来自另一revision的候选进入了受理仓储")
	}
}

// TestRuntimeOriginalReceiptSurvivesEnvironmentChange 已受理原请求在能力关闭、草稿变化后仍只重放原回执。
func TestRuntimeOriginalReceiptSurvivesEnvironmentChange(t *testing.T) {
	service, ports, resource, command := serviceFixture(t)
	hash, err := commandHash(resource.ProjectID, resource.ComponentID, command)
	if err != nil {
		t.Fatal(err)
	}
	ports.hash = hash
	ports.receipt = &Receipt{MutationID: command.MutationID, OperationID: resource.ID}
	service.driver = nil
	ports.canvas = project.CanvasSnapshot{}
	value, err := service.Accept(context.Background(), resource.ProjectID, resource.ComponentID, command)
	if err != nil || !value.Replayed || value.OperationID != resource.ID || ports.accepted != nil {
		t.Fatal("原键被当成新目标或受当前配置阻塞")
	}
	command.Desired = "stopped"
	_, err = service.Accept(context.Background(), resource.ProjectID, resource.ComponentID, command)
	expectServiceCode(t, err, "IDEMPOTENCY_CONFLICT")
	if service.Capability().Enabled {
		t.Fatal("未配置执行器仍报告启用")
	}
}

// TestRuntimeRejectsMissingProfileAndUnknownComponent 目录未就绪、其他组件与未显式配置均不能新受理。
func TestRuntimeRejectsMissingProfileAndUnknownComponent(t *testing.T) {
	service, ports, resource, command := serviceFixture(t)
	if !service.Capability().Enabled {
		t.Fatal("显式适配器未报告配置能力")
	}
	ports.canvas.Canvas.Middlewares[0].Configuration = map[string]any{}
	_, err := service.Accept(context.Background(), resource.ProjectID, resource.ComponentID, command)
	expectServiceCode(t, err, "RUNTIME_NOT_READY")
	ports.canvas.Canvas.Middlewares = nil
	_, err = service.Accept(context.Background(), resource.ProjectID, resource.ComponentID, command)
	expectServiceCode(t, err, "COMPONENT_NOT_FOUND")
	service.driver = nil
	_, err = service.Accept(context.Background(), resource.ProjectID, resource.ComponentID, command)
	expectServiceCode(t, err, "RUNTIME_DISABLED")
	if ports.accepted != nil {
		t.Fatal("非法前置已进入受理")
	}
}

// TestStopUsesOriginalResource 不要求后来草稿合法才允许停止原固定实例。
func TestStopUsesOriginalResource(t *testing.T) {
	service, ports, resource, command := serviceFixture(t)
	command.Desired = "stopped"
	_, err := service.Accept(context.Background(), resource.ProjectID, resource.ComponentID, command)
	expectServiceCode(t, err, "RUNTIME_NOT_STARTED")
	ports.snapshot.Target = &Target{Resource: resource}
	ports.canvas = project.CanvasSnapshot{}
	if _, err := service.Accept(context.Background(), resource.ProjectID, resource.ComponentID, command); err != nil || ports.accepted == nil || ports.accepted.Resource.ID != resource.ID {
		t.Fatal("停止没有使用原实例")
	}
	ports.snapshot.Target.Resource.ComponentID = resource.ProjectID + "changed"
	_, err = service.Accept(context.Background(), resource.ProjectID, resource.ComponentID, command)
	expectServiceCode(t, err, "RUNTIME_COMPONENT_CONFLICT")
}

// TestRuntimeVersionAndQueryBoundary 独立版本拒绝溢出、前导零和无效归属，查询不能跨边界猜测结果。
func TestRuntimeVersionAndQueryBoundary(t *testing.T) {
	service, _, resource, command := serviceFixture(t)
	for _, value := range []string{"-1", "01", "18446744073709551616", ""} {
		command.BaseGeneration = value
		_, err := service.Accept(context.Background(), resource.ProjectID, resource.ComponentID, command)
		expectServiceCode(t, err, "INVALID_REQUEST")
	}
	if _, err := service.Receipt(context.Background(), "bad"); err == nil {
		t.Fatal("非法回执身份被查询")
	}
	if _, err := service.Snapshot(context.Background(), "bad"); err == nil {
		t.Fatal("非法项目被查询")
	}
	if _, err := service.Operation(context.Background(), resource.ProjectID, "bad"); err == nil {
		t.Fatal("非法操作被查询")
	}
	if _, err := service.Events(context.Background(), resource.ProjectID, "01"); err == nil {
		t.Fatal("非法事件游标被查询")
	}
}
