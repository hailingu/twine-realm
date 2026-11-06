// 本文件拥有持久部署执行与被动采集，HTTP连接和浏览器退出均不停止业务资源。
package servicedeploy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Worker 同步拥有有界执行；持久围栏和项目文件锁负责跨进程正确性。
type Worker struct {
	repository Repository
	adapter    Adapter
	logger     *slog.Logger
}

// NewWorker 显式启用执行器，不在构造阶段启动goroutine。
func NewWorker(repository Repository, adapter Adapter, logger *slog.Logger) (*Worker, error) {
	if repository == nil || adapter == nil || logger == nil {
		return nil, fmt.Errorf("业务部署工作器必须显式装配")
	}
	return &Worker{repository, adapter, logger}, nil
}

// Run 依次领取和采集，退出前等待外部命令与监测归还所有权。
func (worker *Worker) Run(ctx context.Context) (err error) {
	defer func() {
		if recover() != nil {
			err = Failure("DEPLOY_WORKER_PANIC", "部署工作器异常退出，保留原操作等待恢复核实", true)
		}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	next := time.Time{}
	for ctx.Err() == nil {
		if err := worker.runOne(ctx); err != nil && ctx.Err() == nil {
			worker.logger.Warn("业务部署持久结果尚未确认", "code", "DEPLOY_STORAGE_UNAVAILABLE")
		}
		if ctx.Err() == nil && !time.Now().Before(next) {
			if err := worker.observe(ctx); err != nil && ctx.Err() == nil {
				worker.logger.Warn("业务状态采集尚未确认", "code", "DEPLOY_OBSERVATION_UNAVAILABLE")
			}
			next = time.Now().Add(5 * time.Second)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
	return nil
}

// runOne 在领取提交成功之后执行，普通停监测取消不推翻已确认成功。
func (worker *Worker) runOne(ctx context.Context) error {
	now := time.Now().UTC()
	lease, err := worker.repository.ClaimDeployment(ctx, now, now.Add(time.Duration(DefaultPolicy().LeaseSeconds)*time.Second))
	if err != nil || lease == nil {
		return err
	}
	fact, executeErr := worker.execute(ctx, *lease)
	settling, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	if ctx.Err() != nil {
		return worker.repository.YieldDeployment(settling, *lease)
	}
	result := Completion{State: "succeeded", Code: "READY", Message: "固定候选、业务迁移、最小权限与实际HTTP就绪已核实", Observation: fact}
	if executeErr != nil {
		code, message, unknown := Diagnostic(executeErr)
		result.State, result.Code, result.Message = "failed", code, message
		if unknown {
			result.State = "needs_attention"
		}
	}
	return worker.repository.FinishDeployment(settling, *lease, result)
}

// execute 在正常返回和异常时都收回监测，原始panic内容不能进入日志或持久诊断。
func (worker *Worker) execute(ctx context.Context, lease Lease) (fact *Observation, err error) {
	executing, cancel := context.WithTimeout(ctx, time.Duration(DefaultPolicy().TotalSeconds)*time.Second)
	monitored, stop := context.WithCancel(executing)
	finished := make(chan error, 1)
	go func() { finished <- worker.watch(monitored, cancel, lease) }()
	defer func() {
		stop()
		monitorErr := <-finished
		cancel()
		if recover() != nil {
			err = Failure("DEPLOY_EXECUTION_PANIC", "部署执行异常，原任务和数据库结果须先核实", true)
		}
		err = errors.Join(err, monitorErr)
	}()
	return worker.adapter.Execute(executing, lease, func(ctx context.Context, point Step) error {
		valid, err := worker.repository.CheckpointDeployment(ctx, lease, point)
		if err != nil {
			return err
		}
		if !valid {
			return Failure("DEPLOY_EXECUTION_FENCED", "原执行许可已失效，停止后续动作", true)
		}
		return nil
	})
}

// watch 正常停止自己的在途查询时返回成功；真正存储错误或失效仍中止执行。
func (worker *Worker) watch(ctx context.Context, cancel context.CancelFunc, lease Lease) (err error) {
	defer func() {
		if recover() != nil {
			cancel()
			err = Failure("DEPLOY_MONITOR_PANIC", "部署许可监测异常，停止原执行并保留结果核实", true)
		}
	}()
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		valid, err := worker.repository.DeploymentAuthorized(ctx, lease)
		if stopped := ctx.Err(); stopped != nil && errors.Is(err, stopped) {
			return nil
		}
		if err != nil {
			cancel()
			return err
		}
		if !valid {
			cancel()
			return Failure("DEPLOY_EXECUTION_FENCED", "原部署执行许可失效，须核实原资源", true)
		}
	}
}

// observe 只记录原资源事实，不因成功后的停改而自动重建或重新迁移。
func (worker *Worker) observe(ctx context.Context) error {
	targets, err := worker.repository.DeploymentTargets(ctx)
	if err != nil {
		return err
	}
	for _, target := range targets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		observing, cancel := context.WithTimeout(ctx, 20*time.Second)
		fact, err := worker.adapter.Inspect(observing, target)
		cancel()
		code := ""
		if err != nil {
			code, _, _ = Diagnostic(err)
		}
		if err := worker.repository.ObserveDeployment(ctx, target, fact, code, time.Now().UTC()); err != nil {
			return err
		}
	}
	return nil
}
