// 本文件将实际运行与构建工作器归于同一平台生命周期，退出前收回全部goroutine。
package main

import (
	"context"
	"errors"
	"fmt"
)

// backgroundWorker 是生命周期实际消费的单方法端口，不将任务实现混入HTTP。
type backgroundWorker interface{ Run(context.Context) error }

// backgroundTasks 仅组合已明确启用的工作器，不持有数据库或配置秘密。
type backgroundTasks struct{ values []backgroundWorker }

// Run 任一后台意外结束即取消其余任务，并等待所有拥有者真正退出。
func (tasks *backgroundTasks) Run(ctx context.Context) (err error) {
	if len(tasks.values) == 0 {
		<-ctx.Done()
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, len(tasks.values))
	for _, worker := range tasks.values {
		go func() { results <- worker.Run(ctx) }()
	}
	for index := range tasks.values {
		result := <-results
		if index == 0 && ctx.Err() == nil && result == nil {
			result = fmt.Errorf("后台工作器意外退出，需核实持久操作")
		}
		err = errors.Join(err, result)
		cancel()
	}
	return err
}
