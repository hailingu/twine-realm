// 本文件逐阶段执行原目标，迁移进程、数据库历史与业务入口三者必须相互印证。
package servicecompose

import (
	"context"
	"net"
	"strconv"
	"time"

	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// execution 将本次计划、原管理会话与持久检查点置于同一拥有者。
type execution struct {
	driver  *Driver
	lease   servicedeploy.Lease
	session *mysqlruntime.BindingSession
	report  func(context.Context, servicedeploy.Step) error
	fact    *servicedeploy.Observation
}

// Execute 不在平台重启时盲重跑业务迁移，所有恢复均先核实原容器与历史。
func (driver *Driver) Execute(ctx context.Context, lease servicedeploy.Lease, report func(context.Context, servicedeploy.Step) error) (*servicedeploy.Observation, error) {
	work := &execution{driver: driver, lease: lease, report: report}
	if err := work.stage(ctx, "preflight", func() error { return driver.build.VerifyCandidate(ctx, lease.Target.Plan.Candidate) }); err != nil {
		return nil, err
	}
	request, err := driver.bindingRequest(ctx, lease.Target.Plan)
	if err != nil {
		return nil, err
	}
	err = driver.runtime.WithBinding(ctx, request, func(session *mysqlruntime.BindingSession) error {
		work.session = session
		return work.run(ctx)
	})
	return work.fact, err
}

// run 每步前后写检查点，任一未知或围栏失败都停止推进后续外部副作用。
func (work *execution) run(ctx context.Context) error {
	plan := work.lease.Target.Plan
	actions := []struct {
		name string
		run  func() error
	}{
		{"database", func() error { return work.session.Prepare(ctx) }},
		{"material", func() error {
			data, err := composeMaterial(plan, work.session.Files())
			if err != nil {
				return err
			}
			if err := work.session.Material(ctx, data); err != nil {
				return err
			}
			return work.driver.ensureIngress(ctx, plan)
		}},
		{"migration", func() error { return work.migrate(ctx) }},
		{"permissions", func() error { return work.session.ApplicationPermissions(ctx) }},
		{"container", func() error { return work.application(ctx) }},
		{"readiness", func() error { return work.ready(ctx) }},
	}
	for _, action := range actions {
		if err := work.stage(ctx, action.name, action.run); err != nil {
			return err
		}
	}
	return nil
}

// stage 原执行许可未持久确认时，不调用外部动作，也不伪记完成。
func (work *execution) stage(ctx context.Context, name string, action func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := work.report(ctx, servicedeploy.Step{Name: name, State: "executing", UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		return err
	}
	if err := action(); err != nil {
		return err
	}
	return work.report(ctx, servicedeploy.Step{Name: name, State: "succeeded", UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)})
}

// application 原容器按完整配置核实后才能启动；端口占用不偷偷换端口或覆盖进程。
func (work *execution) application(ctx context.Context) error {
	plan := work.lease.Target.Plan
	value, err := work.driver.inspectContainer(ctx, plan, "application", work.session.Files())
	if err != nil {
		return err
	}
	if value != nil && value.State.Running {
		return nil
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(plan.Request.HostPort))
	if err != nil {
		return servicedeploy.Failure("DEPLOY_PORT_IN_USE", "已审阅的回环业务端口不可用，保留原目标；释放占用后按原计划恢复", false)
	}
	if err := listener.Close(); err != nil {
		return servicedeploy.Failure("DEPLOY_PORT_CHECK_UNKNOWN", "端口探测未确认关闭，保留原目标", true)
	}
	return work.session.Start(ctx, "application")
}

// ready 等待只读探针与实际容器及迁移证据，不创建任何业务记录。
func (work *execution) ready(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(work.lease.Target.Plan.Policy.ReadinessSeconds)*time.Second)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		fact, err := work.driver.inspectFacts(ctx, work.lease.Target.Plan, work.session)
		work.fact = fact
		if err != nil {
			return err
		}
		if fact != nil && fact.Ready {
			return nil
		}
		if fact != nil && !fact.Running {
			return servicedeploy.Failure("DEPLOY_APPLICATION_EXITED", "业务进程在就绪前已停止，原容器与数据保留", false)
		}
		select {
		case <-ctx.Done():
			return servicedeploy.Failure("DEPLOY_READINESS_TIMEOUT", "业务只读就绪探针超过期限，保留原服务与数据", false)
		case <-ticker.C:
		}
	}
}
