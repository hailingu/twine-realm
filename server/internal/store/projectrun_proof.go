// 本文件核实本次主动冻结证明，不以被动采集器的锁竞争诊断否认已核实事实。
package store

import (
	"fmt"
	"time"

	"github.com/hailingu/twine-realm/server/internal/projectrun"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// validateProjectRunProof 本次认证、HTTP、迁移和物理身份必须完整且仍在采样新鲜窗口内。
func validateProjectRunProof(frozen projectrun.Frozen, proof projectrun.Facts, now time.Time) error {
	runtime, deployment := proof.Runtime, proof.Deployment
	if runtime == nil || deployment == nil || !runtime.Ready || !deployment.Ready || runtime.ServerUUID != frozen.ServerUUID || deployment.DatabaseUUID != frozen.ServerUUID || runtime.ContainerID != frozen.DatabaseContainerID || deployment.ContainerID != frozen.ApplicationContainerID {
		return fmt.Errorf("本次冻结缺少原资源就绪证明")
	}
	if err := validateRuntimeObservation(*runtime, now); err != nil {
		return err
	}
	if err := servicedeploy.ValidateTargetObservation(frozen.Deployment, *deployment); err != nil {
		return err
	}
	for _, stamp := range []string{runtime.ObservedAt, deployment.ObservedAt} {
		observed, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil || observed.After(now.Add(5*time.Second)) || now.Sub(observed) > 20*time.Second {
			return fmt.Errorf("本次冻结证明已过期或时间无效")
		}
	}
	if runtime.State != "running" || deployment.ImageID != frozen.Deployment.Plan.Candidate.Image.ID {
		return fmt.Errorf("本次冻结证明不属于原运行镜像")
	}
	return nil
}
