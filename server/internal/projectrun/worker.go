// 本文件拥有父操作轮询，浏览器断线不取消持久命令。
package projectrun

import (
	"context"
	"errors"
	"fmt"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
	"time"
)

// Worker 串行执行已领取父目标，不在租约过期时假定旧命令消失。
type Worker struct {
	repository Repository
	adapter    Adapter
}

// NewWorker 只装配显式启用的原资源适配器。
func NewWorker(repository Repository, adapter Adapter) *Worker { return &Worker{repository, adapter} }

// Run 平台退出后停止领取；未完成动作由持久账本保留。
func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			lease, err := w.repository.ClaimProjectRun(ctx, time.Now().UTC(), time.Now().UTC().Add(5*time.Minute))
			if err != nil {
				return err
			}
			if lease == nil {
				continue
			}
			if err := w.execute(ctx, *lease); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		}
	}
}

// execute 恢复未知动作只进入人工核实，绝不自动重发或反向执行。
func (w *Worker) execute(ctx context.Context, lease Lease) error {
	for _, step := range lease.Operation.Steps {
		if step.State == "executing" || step.State == "unknown" {
			return w.finish(lease, Completion{State: "needs_attention", Code: "PROJECT_RUN_UNKNOWN", Message: "原外部动作尚未结清，禁止反向恢复"})
		}
	}
	allowed, err := w.repository.ProjectRunAuthorized(ctx, lease)
	if err != nil {
		return err
	}
	if !allowed {
		return nil
	}
	steps := map[string]string{}
	checkpoint := func(ctx context.Context, step Step) error {
		steps[step.Name] = "unknown"
		allowed, err := w.repository.CheckpointProjectRun(ctx, lease, step)
		if err != nil {
			return err
		}
		if !allowed {
			return fmt.Errorf("父执行身份已失效")
		}
		steps[step.Name] = step.State
		return nil
	}
	var facts Facts
	if lease.Target.Desired == "stopped" {
		facts, err = w.adapter.StopExisting(ctx, lease, checkpoint)
	} else {
		facts, err = w.adapter.ResumeExisting(ctx, lease, checkpoint)
	}
	result := executionResult(ctx, facts, err, steps)

	if ctx.Err() != nil {
		return errors.Join(err, w.finish(lease, result))
	}
	return w.finish(lease, result)
}

// finish 使用独立短期限持久结算，平台取消不能抹去未知动作。
func (w *Worker) finish(lease Lease, result Completion) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return w.repository.FinishProjectRun(ctx, lease, result)
}

// executionResult 将外部诊断与本次持久步骤联合判定，未知不自动重试。
func executionResult(ctx context.Context, facts Facts, err error, steps map[string]string) Completion {
	result := Completion{State: "succeeded", Facts: &facts}
	if err == nil {
		return result
	}
	code, message, unknown := servicedeploy.Diagnostic(err)
	unsettled := false
	for _, state := range steps {
		if state == "executing" || state == "unknown" {
			unsettled = true
		}
	}
	result.State, result.Code, result.Message = "failed", code, message
	if unknown || unsettled {
		result.State = "needs_attention"
	}
	if ctx.Err() != nil && !unsettled && (!unknown || errors.Is(err, ctx.Err())) {
		result.State = "waiting_retry"
		result.Code = "PROJECT_RUN_INTERRUPTED"
		result.Message = "在已知步骤边界中断，保留原目标继续执行"
	}
	return result
}
