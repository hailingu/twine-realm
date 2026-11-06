// 本文件固定首个运行组合的资源与期限初值，不能通过请求关闭安全检查。
package servicedeploy

// Policy 是运行身份的一部分，修改需要新版本与重新接受目标。
type Policy struct {
	ID               string `json:"id"`
	Version          int    `json:"version"`
	TotalSeconds     int    `json:"totalSeconds"`
	MigrationSeconds int    `json:"migrationSeconds"`
	ReadinessSeconds int    `json:"readinessSeconds"`
	CommandSeconds   int    `json:"commandSeconds"`
	LeaseSeconds     int    `json:"leaseSeconds"`
	MemoryMiB        int    `json:"memoryMiB"`
	TmpfsMiB         int    `json:"tmpfsMiB"`
	PIDs             int    `json:"pids"`
	CPUs             int    `json:"cpus"`
}

// DefaultPolicy 的租约覆盖最大执行与收尾，后续必须由真实部署验收固定支持范围。
func DefaultPolicy() Policy {
	return Policy{ID: "go-mysql-compose-v1", Version: 1, TotalSeconds: 360, MigrationSeconds: 120, ReadinessSeconds: 120, CommandSeconds: 45, LeaseSeconds: 420, MemoryMiB: 512, TmpfsMiB: 64, PIDs: 128, CPUs: 1}
}
