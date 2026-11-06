// 本文件拥有持久构建工作器及其授权监测；HTTP断开不取消构建。
package servicebuild

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Worker 领取固定输入并收回所有goroutine，跨进程正确性由SQL围栏和文件锁保护。
type Worker struct {
	queue         Queue
	authorization Authorization
	exporter      SourceExporter
	driver        *Driver
	logger        *slog.Logger
}

// NewWorker 只接受完整显式装配，不创建无持久所有者的后台执行。
func NewWorker(queue Queue, authorization Authorization, exporter SourceExporter, driver *Driver, logger *slog.Logger) (*Worker, error) {
	if queue == nil || authorization == nil || exporter == nil || driver == nil || logger == nil {
		return nil, fmt.Errorf("构建工作器缺少明确的持久、源码或执行端口")
	}
	return &Worker{queue, authorization, exporter, driver, logger}, nil
}

// Run 所有异常转换为安全错误，调用方须等待退出再关闭文件根和数据库。
func (worker *Worker) Run(ctx context.Context) (err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("构建工作器内部异常，需核实原操作与沙箱")
		}
	}()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := worker.runOne(ctx); err != nil && ctx.Err() == nil {
			worker.logger.Warn("构建持久结果尚未确认", "code", "BUILD_STORAGE_UNAVAILABLE")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// runOne 先持久领取，再执行；提交未知时保留原输入等待租约恢复。
func (worker *Worker) runOne(ctx context.Context) error {
	now := time.Now().UTC()
	lease, err := worker.queue.ClaimBuild(ctx, now, now.Add(9*time.Minute))
	if err != nil || lease == nil {
		return err
	}
	var completion Completion
	if lease.Recover {
		completion = worker.recover(ctx, *lease)
	} else {
		candidate, err := worker.execute(ctx, *lease)
		completion = buildCompletion(candidate, err, ctx.Err() != nil)
	}
	settling, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return worker.queue.FinishBuild(settling, *lease, completion)
}

// execute 在长命令期间监测取消与围栏，任何数据库故障也停止继续副作用。
func (worker *Worker) execute(ctx context.Context, lease Lease) (*Candidate, error) {
	executing, cancel := context.WithCancel(ctx)
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- worker.watch(executing, cancel, lease) }()
	candidate, err := worker.driver.Execute(executing, lease.Target, lease.Operation.ID, worker.exporter, func(ctx context.Context, point Step) (bool, error) {
		return worker.queue.CheckpointBuild(ctx, lease, point)
	})
	cancel()
	return candidate, errors.Join(err, <-stopped)
}

// watch 只拥有授权监测，不延长租约或改变操作代次。
func (worker *Worker) watch(ctx context.Context, cancel context.CancelFunc, lease Lease) (err error) {
	defer func() {
		if recover() != nil {
			cancel()
			err = fmt.Errorf("构建授权核实异常，需查询原操作")
		}
	}()
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			valid, err := worker.authorization.BuildAuthorized(ctx, lease)
			// 已结束监测的ctx取消不是新的存储故障；父进程退出仍由执行结果与interrupted处理。
			if stopped := ctx.Err(); stopped != nil && errors.Is(err, stopped) {
				return nil
			}
			if err != nil || !valid {
				cancel()
				return err
			}
		}
	}
}

// recover 只核实并收回原沙箱；丢失检查结果保持needs_attention直到明确取消。
func (worker *Worker) recover(ctx context.Context, lease Lease) Completion {
	if err := worker.driver.Recover(ctx, lease.Target, lease.Operation.ID); err != nil {
		code, message, _ := diagnostic(err)
		return Completion{State: "needs_attention", Code: code, Message: message}
	}
	if lease.Operation.CancelRequested {
		return Completion{State: "cancelled", Code: "CANCELLED", Message: "原沙箱已核实收回，未发布候选"}
	}
	return Completion{State: "needs_attention", Code: "BUILD_INTERRUPTED", Message: "原执行检查结果已中断，沙箱已核实收回；可明确取消后重新确认构建"}
}

// buildCompletion 仅在完整执行和沙箱清理成功后允许候选进入同库终态。
func buildCompletion(candidate *Candidate, err error, interrupted bool) Completion {
	if interrupted {
		return Completion{State: "needs_attention", Code: "BUILD_INTERRUPTED", Message: "控制面退出，原构建与外部结果需恢复核实"}
	}
	if err == nil && candidate != nil {
		return Completion{State: "succeeded", Code: "BUILT", Message: "固定源码检查与本地镜像摘要均已核实", Candidate: candidate}
	}
	code, message, unknown := diagnostic(err)
	state := "failed"
	if unknown {
		state = "needs_attention"
	}
	return Completion{State: state, Code: code, Message: message}
}
