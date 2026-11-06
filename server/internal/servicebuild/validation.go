// 本文件校验持久执行边界，避免损坏或不完整结果被记为成功候选。
package servicebuild

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/hailingu/twine-realm/server/internal/engineering"
	"github.com/hailingu/twine-realm/server/internal/project"
)

var codePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,95}$`)
var stepNames = []string{"preflight", "source", "documents", "sandbox", "dependencies", "offline", "toolchain", "format", "module", "vet", "test", "build", "image", "complete"}

// ValidStep 限定固定步骤集合和状态，不接受任意事件名或工具输出。
func ValidStep(value Step) bool {
	return hasExact(stepNames, value.Name) && hasExact([]string{"executing", "succeeded"}, value.State) && (value.Code == "" || codePattern.MatchString(value.Code))
}

// ValidateCompletion 失败与取消禁止候选，成功必须有完整输入和全部真实检查证据。
func ValidateCompletion(lease Lease, value Completion) error {
	if !Terminal(value.State) || !codePattern.MatchString(value.Code) || len(value.Message) == 0 || len(value.Message) > 1024 {
		return fmt.Errorf("构建终态诊断无效")
	}
	if value.State != "succeeded" {
		if value.Candidate != nil {
			return fmt.Errorf("非成功构建不得发布候选")
		}
		return nil
	}
	candidate, target := value.Candidate, lease.Target
	if candidate == nil || candidate.TargetID != target.ID || candidate.OperationID != lease.Operation.ID || candidate.InputDigest != target.InputDigest || candidate.Profile != target.Profile || candidate.Environment != target.Environment || candidate.Policy != target.Policy || !reflect.DeepEqual(candidate.Source, target.Source) {
		return fmt.Errorf("构建成功候选不属于完整固定输入")
	}
	if err := validateCandidateIdentity(candidate); err != nil {
		return err
	}
	if !digestPattern.MatchString(candidate.SourceDigest) || !digestPattern.MatchString(candidate.Image.ManifestDigest) || !digestPattern.MatchString(candidate.Image.ConfigDigest) || candidate.Image.ID != candidate.Image.ConfigDigest || !digestPattern.MatchString(candidate.Image.BinaryDigest) || len(candidate.Migrations) == 0 {
		return fmt.Errorf("构建成功候选缺少实际内容摘要")
	}
	if err := validateChecks(candidate.Checks); err != nil {
		return err
	}
	return validateMigrations(candidate)
}

// validateMigrations 核实固定目录与摘要，不允许把外部路径变成迁移许可。
func validateMigrations(candidate *Candidate) error {
	if len(candidate.Migrations) > 512 {
		return fmt.Errorf("构建迁移清单超出上限")
	}
	previous := ""
	for _, file := range candidate.Migrations {
		if !fs.ValidPath(file.Path) || !strings.HasPrefix(file.Path, "internal/schema/migrations/") || !strings.HasSuffix(file.Path, ".sql") || file.Path <= previous || !digestPattern.MatchString(file.Digest) {
			return fmt.Errorf("构建迁移清单无效")
		}
		previous = file.Path
	}
	digest, err := Fingerprint(candidate.Migrations)
	if err != nil {
		return err
	}
	if digest != candidate.MigrationDigest {
		return fmt.Errorf("构建迁移清单摘要不一致")
	}
	return nil
}

// validateCandidateIdentity 核实产物身份、发布时间、固定模板与持久载荷上限。
func validateCandidateIdentity(candidate *Candidate) error {
	if !project.ValidID(candidate.ID) {
		return fmt.Errorf("构建候选身份无效")
	}
	if _, err := time.Parse(time.RFC3339Nano, candidate.CreatedAt); err != nil {
		return fmt.Errorf("构建候选时间无效")
	}
	catalog, err := engineering.Load()
	if err != nil {
		return err
	}
	if candidate.Template != catalog.Profile().Template {
		return fmt.Errorf("构建候选模板来源不一致")
	}
	data, err := json.Marshal(candidate)
	if err != nil || len(data) > 48*1024 {
		return fmt.Errorf("构建候选超过受控持久载荷上限")
	}
	return nil
}

// validateChecks 要求固定检查全部成功且各有实际输出摘要。
func validateChecks(checks []Check) error {
	seen := map[string]bool{}
	for _, check := range checks {
		if seen[check.Name] || !hasExact(stepNames, check.Name) || check.State != "succeeded" || !digestPattern.MatchString(check.OutputDigest) {
			return fmt.Errorf("构建成功候选检查记录无效")
		}
		if len(check.ToolVersion) == 0 || len(check.ToolVersion) > 128 {
			return fmt.Errorf("构建检查工具版本未确认")
		}
		if _, err := time.Parse(time.RFC3339Nano, check.CompletedAt); err != nil {
			return fmt.Errorf("构建检查时间无效")
		}
		seen[check.Name] = true
	}
	for _, name := range stepNames[:len(stepNames)-1] {
		if !seen[name] {
			return fmt.Errorf("构建成功候选缺少必需检查")
		}
	}
	return nil
}
