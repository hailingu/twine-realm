// 本文件区分就绪、停止与未知；只读采集不自动重建或重新启动外部改变的资源。
package mysqlruntime

import (
	"context"
	"errors"
	"time"
)

// observation 把当前已核实Docker状态转为事实，认证就绪随后单独填写。
func (work *execution) observation() *Observation {
	if work.sampledAt.IsZero() {
		return nil
	}
	value := &Observation{State: "not_created", VolumeFound: work.state.volume != nil, ObservedAt: Timestamp(work.sampledAt)}
	if work.state.container != nil {
		value.ContainerID = work.state.container.ID
		value.Running = work.state.container.State.Running
		value.State = "stopped"
		if value.Running {
			value.State = "running"
		}
	}
	return value
}

// readiness 有界等待最终mysqld认证，目标变化时每轮检查持久执行边界。
func (work *execution) readiness(ctx context.Context, timeout time.Duration) (*Observation, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return work.observation(), failure("MYSQL_STARTUP_TIMEOUT", "MySQL认证就绪超过期限，保留数据并等待明确核实", false, false)
		}
		if err := work.mark(ctx, Checkpoint{Name: "readiness", State: "check"}); err != nil {
			return work.observation(), err
		}
		if err := work.refresh(ctx); err != nil {
			return work.observation(), err
		}
		fact := work.observation()
		if work.state.container == nil || !fact.Running {
			return fact, failure("MYSQL_PROCESS_EXITED", "MySQL进程在就绪前已停止，保留原数据", false, false)
		}
		ready, err := work.authenticate(ctx, fact)
		if err != nil || ready {
			return fact, err
		}
		select {
		case <-ctx.Done():
			return fact, failure("MYSQL_STARTUP_TIMEOUT", "MySQL认证就绪超过期限，保留数据并等待明确核实", false, false)
		case <-ticker.C:
		}
	}
}

// authenticate 将可等待的未就绪与固定数据库身份冲突分开，不让初始化临时进程冒充成功。
func (work *execution) authenticate(ctx context.Context, fact *Observation) (bool, error) {
	id, err := work.driver.probe(ctx, work.lease.Target.Resource, work.state.container.ID)
	if err != nil {
		var fault *Fault
		if errors.As(err, &fault) && fault.Code == "MYSQL_NOT_READY" {
			return false, nil
		}
		return false, err
	}
	fact.ServerUUID, fact.Ready, fact.ObservedAt = id, true, Timestamp(time.Now())
	if work.lease.ServerUUID != "" && id != work.lease.ServerUUID {
		fact.State, fact.Ready = "drifted", false
		return false, failure("RUNTIME_DATABASE_IDENTITY_CONFLICT", "原数据卷的数据库身份已改变，必须核实", false, false)
	}
	return true, nil
}

// stop 接受目标后才停止实际归属容器，不删除卷、网络、秘密或数据。
func (work *execution) stop(ctx context.Context) (*Observation, error) {
	if err := work.stage(ctx, "stop", work.stopContainer); err != nil {
		return work.observation(), err
	}
	if err := work.stage(ctx, "stopped", work.confirmStopped); err != nil {
		return work.observation(), err
	}
	return work.observation(), nil
}

// stopContainer 只停止本轮已核实的容器，不删除或替代任何数据资源。
func (work *execution) stopContainer(ctx context.Context) (string, error) {
	if work.state.container == nil {
		return work.lease.Target.Resource.Container, nil
	}
	id := work.state.container.ID
	if work.state.container.State.Running {
		if _, err := work.driver.execute(ctx, contextOption, work.driver.Environment().Context, "stop", "--time", "20", id); err != nil {
			return "", err
		}
	}
	return id, nil
}

// confirmStopped 重新读取实际状态后才承认停止，未知结果保留原目标。
func (work *execution) confirmStopped(ctx context.Context) (string, error) {
	if err := work.refresh(ctx); err != nil {
		return "", err
	}
	if work.state.container != nil && work.state.container.State.Running {
		return "", failure("RUNTIME_RESULT_UNKNOWN", "停止结果尚未核实，继续保留原目标与数据", true, true)
	}
	return work.lease.Target.Resource.Container, nil
}

// Inspect 只读核实当前资源；失败可携带部分事实，永远不生成秘密或修复漂移。
func (driver *Driver) Inspect(ctx context.Context, resource Resource) (*Observation, error) {
	image, err := driver.preflight(ctx, resource)
	if err != nil {
		return nil, err
	}
	state, err := driver.inspect(ctx, resource, image)
	if err != nil {
		return nil, err
	}
	work := execution{driver: driver, state: state, sampledAt: time.Now().UTC()}
	fact := work.observation()
	if fact.Running {
		id, err := driver.probe(ctx, resource, state.container.ID)
		if err != nil {
			return fact, err
		}
		fact.ServerUUID, fact.Ready, fact.ObservedAt = id, true, Timestamp(time.Now())
	}
	return fact, nil
}
