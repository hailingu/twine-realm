// 本文件只执行已经持久接受的固定目标，步骤前后都经过持久执行代次检查。
package mysqlruntime

import (
	"context"
	"errors"
	"sort"
	"time"
)

// progress 由持久队列拥有，返回false表示目标或执行代次已经失效。
type progress func(context.Context, Checkpoint) (bool, error)

// execution 只属于一次同步执行；context 经方法参数传递，不保存在结构中。
type execution struct {
	driver    *Driver
	lease     Lease
	image     imageSpec
	state     inspected
	sampledAt time.Time
	progress  progress
}

// Execute 在项目资源锁内逐步核实与执行；任何失败都保留已有资源和私有材料。
func (driver *Driver) Execute(ctx context.Context, lease Lease, timeout time.Duration, record progress) (fact *Observation, err error) {
	if err := driver.validateResource(lease.Target.Resource); err != nil {
		return nil, err
	}
	if lease.Target.ProjectID != lease.Target.Resource.ProjectID || lease.Operation.ProjectID != lease.Target.ProjectID || lease.Operation.Generation != lease.Target.Generation || !Number(lease.Execution) {
		return nil, failure("RUNTIME_PLAN_INVALID", "执行身份与固定目标不一致", false, false)
	}
	release, err := driver.projectLock(lease.Target.ProjectID)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, release()) }()
	work := &execution{driver: driver, lease: lease, progress: record}
	if err := work.stage(ctx, "preflight", work.preflight); err != nil {
		return nil, err
	}
	if lease.Target.Desired == "stopped" {
		return work.stop(ctx)
	}
	if lease.Target.Desired != "running" {
		return nil, failure("RUNTIME_PLAN_INVALID", "运行目标动作无效", false, false)
	}
	for _, step := range []struct {
		name   string
		action func(context.Context) (string, error)
	}{{"credentials", work.credentials}, {"network", work.network}, {"volume", work.volume}, {"container", work.container}} {
		if err := work.stage(ctx, step.name, step.action); err != nil {
			return work.observation(), err
		}
	}
	if err := work.mark(ctx, Checkpoint{Name: "readiness", State: "executing"}); err != nil {
		return work.observation(), err
	}
	fact, err = work.readiness(ctx, timeout)
	if err != nil {
		return fact, err
	}
	if err := work.mark(ctx, Checkpoint{Name: "readiness", State: "succeeded", ExternalID: fact.ServerUUID, VolumeConfirmed: true}); err != nil {
		return fact, err
	}
	return fact, nil
}

// mark 任何无法确认的检查点都阻止后续副作用，不猜测数据库提交结果。
func (work *execution) mark(ctx context.Context, point Checkpoint) error {
	valid, err := work.progress(ctx, point)
	if err != nil {
		return err
	}
	if !valid {
		return failure("EXECUTION_FENCED", "运行目标或执行代次已失效，原结果保留待核实", false, false)
	}
	return nil
}

// stage 在副作用前登记执行中，在事实核实后才登记成功。
func (work *execution) stage(ctx context.Context, name string, action func(context.Context) (string, error)) error {
	if err := work.mark(ctx, Checkpoint{Name: name, State: "executing"}); err != nil {
		return err
	}
	id, err := action(ctx)
	if err != nil {
		return err
	}
	return work.mark(ctx, Checkpoint{Name: name, State: "succeeded", ExternalID: id, VolumeConfirmed: work.state.volume != nil})
}

// preflight 核实全部已存在资源，旧卷缺失时不能创建空卷冒充恢复。
func (work *execution) preflight(ctx context.Context) (string, error) {
	image, err := work.driver.preflight(ctx, work.lease.Target.Resource)
	if err != nil {
		return "", err
	}
	work.image = image
	if err := work.refresh(ctx); err != nil {
		return "", err
	}
	if work.lease.VolumeConfirmed && work.state.volume == nil {
		return "", failure("RUNTIME_VOLUME_MISSING", "已登记的数据卷缺失，禁止创建空卷替代原数据库", false, false)
	}
	return work.lease.Target.Resource.DaemonID, nil
}

// refresh 对部分成功和响应丢失总是核实原身份，不改名创建替代对象。
func (work *execution) refresh(ctx context.Context) error {
	state, err := work.driver.inspect(ctx, work.lease.Target.Resource, work.image)
	if err != nil {
		return err
	}
	work.state = state
	work.sampledAt = time.Now().UTC()
	if state.container != nil && (state.volume == nil || state.network == nil) {
		return failure("RUNTIME_INSPECTION_CHANGED", "容器与网络或卷的事实不一致，需要再次核实", true, true)
	}
	return nil
}

// credentials 在任何创建卷动作前准备完整秘密与固定Compose；不向Docker发密码值。
func (work *execution) credentials(ctx context.Context) (string, error) {
	resource := work.lease.Target.Resource
	if err := work.driver.prepareSecrets(resource, work.state.volume != nil || work.lease.VolumeConfirmed); err != nil {
		return "", err
	}
	if err := work.driver.prepareCompose(ctx, resource); err != nil {
		return "", err
	}
	return resource.SecretRef, nil
}

// network 只创建无同名对象的固定私网，已有对象必须已通过严格归属核实。
func (work *execution) network(ctx context.Context) (string, error) {
	resource := work.lease.Target.Resource
	if work.state.network == nil {
		args := []string{contextOption, work.driver.Environment().Context, "network", "create", "--driver", "bridge", "--internal", "--ipv4=true", "--ipv6=false"}
		args = append(args, labelArguments(resource)...)
		args = append(args, resource.Network)
		if _, err := work.driver.execute(ctx, args...); err != nil {
			return "", err
		}
	}
	if err := work.refresh(ctx); err != nil {
		return "", err
	}
	if work.state.network == nil {
		return "", failure("RUNTIME_RESULT_UNKNOWN", "网络创建结果尚未核实", true, true)
	}
	return work.state.network.ID, nil
}

// volume 只有首次未出现过数据卷时才能创建，成功检查点先于任何数据库容器动作。
func (work *execution) volume(ctx context.Context) (string, error) {
	resource := work.lease.Target.Resource
	if work.state.volume == nil {
		if work.lease.VolumeConfirmed {
			return "", failure("RUNTIME_VOLUME_MISSING", "原卷缺失，不能生成替代数据库", false, false)
		}
		args := []string{contextOption, work.driver.Environment().Context, "volume", "create", "--driver", "local"}
		args = append(args, labelArguments(resource)...)
		args = append(args, resource.Volume)
		if _, err := work.driver.execute(ctx, args...); err != nil {
			return "", err
		}
	}
	if err := work.refresh(ctx); err != nil {
		return "", err
	}
	if work.state.volume == nil {
		return "", failure("RUNTIME_RESULT_UNKNOWN", "数据卷创建结果尚未核实", true, true)
	}
	return resource.Volume, nil
}

// container 从固定材料首次创建或明确运行目标恢复旧容器，不使用重新创建覆盖配置。
func (work *execution) container(ctx context.Context) (string, error) {
	resource := work.lease.Target.Resource
	if work.state.volume == nil || work.state.network == nil {
		return "", failure("RUNTIME_RESOURCE_MISSING", "先前核实的网络或数据卷已缺失", false, false)
	}
	if work.state.container == nil {
		if _, err := work.driver.composeCommand(ctx, resource, "up", "--detach", "--no-build", "--no-deps", "--no-recreate", "--pull", "never", "mysql"); err != nil {
			return "", err
		}
	} else if err := work.startExisting(ctx); err != nil {
		return "", err
	}
	if err := work.refresh(ctx); err != nil {
		return "", err
	}
	if work.state.container == nil {
		return "", failure("RUNTIME_RESULT_UNKNOWN", "容器创建结果尚未核实", true, true)
	}
	if !work.state.container.State.Running && work.state.container.State.Status != "restarting" {
		return "", failure("MYSQL_PROCESS_EXITED", "MySQL进程未持续运行，保留数据与诊断", false, false)
	}
	return work.state.container.ID, nil
}

// startExisting 只有新的明确运行或尚未启动过的原容器可启动，不将重试当作外部停止后的自动修复。
func (work *execution) startExisting(ctx context.Context) error {
	container := work.state.container
	if container.State.Running || container.State.Status == "restarting" {
		return nil
	}
	if work.lease.Operation.Attempts > 1 && container.State.Status != "created" {
		return failure("RUNTIME_DRIFT", "恢复时发现容器已停止，需新的明确运行请求", false, false)
	}
	_, err := work.driver.execute(ctx, contextOption, work.driver.Environment().Context, "start", container.ID)
	return err
}

// labelArguments 给固定小集合按字典序生成参数，每个标签都保持独立argv。
func labelArguments(resource Resource) []string {
	values := labels(resource)
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	args := []string{}
	for _, key := range keys {
		args = append(args, "--label", key+"="+values[key])
	}
	return args
}
