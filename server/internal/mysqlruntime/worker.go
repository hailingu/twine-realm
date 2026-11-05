// 本文件调和持久目标；网页断连不取消操作，控制面退出也不隐含停止业务容器。
package mysqlruntime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"
)

// Worker 在同一进程中拥有一次有界执行，跨进程正确性由持久条件与文件锁共同保护。
type Worker struct {
	queue  QueueStore
	facts  ObservationStore
	driver *Driver
	policy Policy
	logger *slog.Logger
}

// NewWorker 拒绝让执行期限越出租约，最多初次加5次有界重试。
func NewWorker(queue QueueStore, facts ObservationStore, driver *Driver, policy Policy, logger *slog.Logger) (*Worker, error) {
	if driver == nil || logger == nil || policy.Poll <= 0 || policy.ObserveEvery <= 0 || policy.ReadinessTimeout <= 0 || policy.LeaseDuration <= 20*time.Second || policy.ReadinessTimeout >= policy.LeaseDuration-15*time.Second || policy.MaxAttempts < 1 || policy.MaxAttempts > 6 {
		return nil, fmt.Errorf("运行工作器配置不符合有界执行策略")
	}
	return &Worker{queue, facts, driver, policy, logger}, nil
}

// Run 同步拥有执行与观测循环；所有panic转为安全错误，调用者必须等待返回。
func (worker *Worker) Run(ctx context.Context) (err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("运行工作器内部异常，需核实持久操作与原资源")
		}
	}()
	ticker := time.NewTicker(worker.policy.Poll)
	defer ticker.Stop()
	nextObservation := time.Time{}
	cursor := ""
	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := worker.runOne(ctx); err != nil && ctx.Err() == nil {
			worker.logger.Warn("运行步骤尚未确认", "code", "RUNTIME_STORAGE_UNAVAILABLE")
		}
		if !time.Now().Before(nextObservation) && ctx.Err() == nil {
			var err error
			cursor, err = worker.observeBatch(ctx, cursor)
			if err != nil && ctx.Err() == nil {
				worker.logger.Warn("运行采集尚未确认", "code", "RUNTIME_OBSERVATION_UNAVAILABLE")
			}
			nextObservation = time.Now().Add(worker.policy.ObserveEvery)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// runOne 在任何外部动作前持久领取，提交失败或代次失效立即停止推进。
func (worker *Worker) runOne(ctx context.Context) error {
	now := time.Now().UTC()
	lease, err := worker.queue.ClaimRuntime(ctx, now, now.Add(worker.policy.LeaseDuration))
	if err != nil || lease == nil {
		return err
	}
	executing, cancel := context.WithTimeout(ctx, worker.policy.LeaseDuration-15*time.Second)
	fact, executeErr := worker.driver.Execute(executing, *lease, worker.policy.ReadinessTimeout, func(ctx context.Context, point Checkpoint) (bool, error) {
		return worker.queue.CheckpointRuntime(ctx, *lease, point)
	})
	cancel()
	var fault *Fault
	if errors.As(executeErr, &fault) && fault.Code == "EXECUTION_FENCED" {
		return nil
	}
	completion := worker.completion(*lease, fact, executeErr, ctx.Err() != nil)
	settling, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	return worker.queue.FinishRuntime(settling, *lease, completion)
}

// completion 分离暂时错误、未知中断和需人工处理，耗尽保留最后稳定错误码。
func (worker *Worker) completion(lease Lease, fact *Observation, err error, interrupted bool) Completion {
	value := Completion{State: "succeeded", Code: "READY", Message: "固定MySQL目标已通过认证就绪核实", NextAttempt: time.Now().UTC(), Observation: fact}
	if lease.Target.Desired == "stopped" {
		value.Code, value.Message = "STOPPED", "停止事实已核实，数据卷和秘密保留"
	}
	if interrupted {
		value.State, value.Code, value.StepCode, value.Message = "waiting_retry", "RUNTIME_INTERRUPTED", "RUNTIME_INTERRUPTED", "控制面退出，原资源与未知结果将在重启后核实"
		return value
	}
	if err == nil {
		return value
	}
	fault := failure("RUNTIME_CHECKPOINT_UNCONFIRMED", "运行检查点或外部结果尚未确认，保留原资源", true, true)
	var actual *Fault
	if errors.As(err, &actual) {
		fault = actual
	}
	value.State, value.Code, value.StepCode, value.Message = "needs_attention", fault.Code, fault.Code, fault.Message
	if fault.Retryable {
		if lease.Operation.Attempts < worker.policy.MaxAttempts {
			value.State = "waiting_retry"
			value.NextAttempt = value.NextAttempt.Add(retryDelay(lease.Operation.Attempts))
		} else {
			value.Code, value.Message = "RETRY_EXHAUSTED", "暂时错误的恢复尝试已耗尽；最后稳定错误码："+fault.Code
		}
	}
	return value
}

// retryDelay 只用于退避抖动，不生成秘密；初值1秒，上限30秒。
func retryDelay(attempt int) time.Duration {
	base := time.Second * time.Duration(1<<max(0, min(attempt-1, 5)))
	base = min(base, 30*time.Second)
	return min(30*time.Second, base+time.Duration(rand.Int64N(int64(base/4)+1)))
}

// observeBatch 只采集事实；成功后的外部停删或资源漂移不触发自动重建。
func (worker *Worker) observeBatch(ctx context.Context, cursor string) (string, error) {
	values, err := worker.facts.RuntimeInstances(ctx, cursor)
	if err != nil {
		return cursor, err
	}
	for _, value := range values {
		if ctx.Err() != nil {
			return cursor, ctx.Err()
		}
		observing, cancel := context.WithTimeout(ctx, 10*time.Second)
		fact, observeErr := worker.driver.Inspect(observing, value.Target.Resource)
		cancel()
		code := observationCode(value, fact, observeErr)
		if ctx.Err() != nil {
			return cursor, ctx.Err()
		}
		persisting, stop := context.WithTimeout(ctx, 5*time.Second)
		err := worker.facts.ObserveRuntime(persisting, value.Target, fact, code, time.Now().UTC())
		stop()
		if err != nil {
			return cursor, err
		}
		cursor = value.Target.ProjectID
	}
	if len(values) < 16 {
		cursor = ""
	}
	return cursor, nil
}

// observationCode 把未知、数据丢失与目标漂移分开，永远不猜测已经停止。
func observationCode(instance Instance, fact *Observation, err error) string {
	if err != nil {
		var fault *Fault
		if errors.As(err, &fault) {
			return fault.Code
		}
		return "RUNTIME_OBSERVATION_UNAVAILABLE"
	}
	if fact == nil {
		return "RUNTIME_OBSERVATION_UNAVAILABLE"
	}
	if instance.VolumeConfirmed && !fact.VolumeFound {
		return "RUNTIME_VOLUME_MISSING"
	}
	if instance.ServerUUID != "" && fact.ServerUUID != "" && instance.ServerUUID != fact.ServerUUID {
		return "RUNTIME_DATABASE_IDENTITY_CONFLICT"
	}
	if Terminal(instance.OperationState) && ((instance.Target.Desired == "running" && !fact.Running) || (instance.Target.Desired == "stopped" && fact.Running)) {
		return "RUNTIME_DRIFT"
	}
	return ""
}
