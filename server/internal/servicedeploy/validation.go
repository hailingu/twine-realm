// 本文件对存储写边界施加状态不变量，避免伪造running即成功。
package servicedeploy

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"time"
)

var diagnosticCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,95}$`)
var steps = []string{"preflight", "database", "material", "migration", "permissions", "container", "readiness"}

// ValidStep 固定阶段与有限状态，错误码不能携带外部日志。
func ValidStep(value Step) bool {
	return slices.Contains(steps, value.Name) && slices.Contains([]string{"executing", "succeeded"}, value.State) && (value.Code == "" || diagnosticCode.MatchString(value.Code))
}

// ValidateTargetObservation 将已知镜像、数据身份和业务入口绑定原目标，不能保存其他资源的就绪。
func ValidateTargetObservation(target Target, value Observation) error {
	if err := ValidateObservation(value); err != nil {
		return err
	}
	if value.ImageID != "" && value.ImageID != target.Plan.Candidate.Image.ID || value.DatabaseUUID != "" && value.DatabaseUUID != target.Plan.Database.ServerUUID || value.EntryURL != "" && value.EntryURL != "http://127.0.0.1:"+strconv.Itoa(target.Plan.Request.HostPort) {
		return fmt.Errorf("部署观测不属于原镜像、数据库或回环入口")
	}
	return nil
}

// ValidateCompletion 成功必须同时有真实运行、HTTP探针和干净且匹配的迁移身份。
func ValidateCompletion(value Completion) error {
	if !Terminal(value.State) || !diagnosticCode.MatchString(value.Code) || len(value.Message) == 0 || len(value.Message) > 1024 {
		return fmt.Errorf("部署终态诊断无效")
	}
	if value.Observation != nil {
		if err := ValidateObservation(*value.Observation); err != nil {
			return err
		}
	}
	if value.State == "succeeded" && (value.Observation == nil || !value.Observation.Ready || !value.Observation.Healthy || !value.Observation.Running || value.Observation.MigrationIntegrity != "verified" || value.Observation.DatabaseUUID == "") {
		return fmt.Errorf("部署成功缺少真实就绪与迁移证据")
	}
	return nil
}

// ValidateObservation 对内部采集结果同样检查有限状态和跨字段条件。
func ValidateObservation(value Observation) error {
	if !slices.Contains([]string{"not_created", "stopped", "running", "ready", "drifted", "unknown"}, value.State) || !slices.Contains([]string{"missing", "dirty", "verified", "mismatch", "unknown"}, value.MigrationIntegrity) {
		return fmt.Errorf("部署观测状态无效")
	}
	if _, err := time.Parse(time.RFC3339Nano, value.ObservedAt); err != nil {
		return fmt.Errorf("部署观测时间无效")
	}
	if value.Ready && (!value.Running || !value.Healthy || value.State != "ready" || value.MigrationIntegrity != "verified" || value.DatabaseUUID == "" || value.EntryURL == "") {
		return fmt.Errorf("业务就绪缺少交叉证据")
	}
	return nil
}
