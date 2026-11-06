// 本文件守住同目标观测的单调性，多个采集者乱序提交也不能回退已知事实。
package store

import (
	"fmt"
	"time"

	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// currentDeploymentFact 先比较尝试和实际采集时间，迟到失败也不能盖掉较新的诊断。
func currentDeploymentFact(instance *deploymentInstance, target servicedeploy.Target, fact *servicedeploy.Observation, at time.Time) (bool, error) {
	if instance.lastAttempt.Valid && instance.lastAttempt.Time.After(at) {
		return false, nil
	}
	if fact == nil {
		return true, nil
	}
	if err := servicedeploy.ValidateTargetObservation(target, *fact); err != nil {
		return false, err
	}
	if len(instance.observation) == 0 {
		return true, nil
	}
	var previous servicedeploy.Observation
	if err := decodeRuntimeJSON(instance.observation, &previous); err != nil {
		return false, err
	}
	oldTime, err := time.Parse(time.RFC3339Nano, previous.ObservedAt)
	if err != nil {
		return false, fmt.Errorf("原部署观测时间记录无效")
	}
	newTime, err := time.Parse(time.RFC3339Nano, fact.ObservedAt)
	if err != nil {
		return false, fmt.Errorf("部署观测时间记录无效")
	}
	return !oldTime.After(newTime), nil
}
