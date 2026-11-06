// 本文件提供部署所需的只读镜像复核，不授权重新构建或向业务运行区复制源码。
package servicebuild

import (
	"context"
	"github.com/hailingu/twine-realm/server/internal/project"
	"reflect"
)

// VerifyCandidate 复用原构建身份和实际镜像检查；tag、daemon或内容变化均拒绝运行。
func (driver *Driver) VerifyCandidate(ctx context.Context, candidate Candidate) error {
	if err := validateCandidateIdentity(&candidate); err != nil {
		return err
	}
	digest, err := InputFingerprint(candidate.Source, candidate.Profile, candidate.Environment, candidate.Policy, driver.InstallationID())
	if err != nil {
		return err
	}
	if !project.ValidID(candidate.OperationID) || !project.ValidID(candidate.TargetID) || candidate.InputDigest != digest || candidate.Environment != driver.Environment() || !reflect.DeepEqual(candidate.Policy, DefaultPolicy()) {
		return failure("BUILD_IMAGE_IDENTITY_CONFLICT", "候选输入与原构建安装不一致")
	}
	target := Target{ID: candidate.TargetID, ProjectID: candidate.Source.ProjectID, InstallationID: driver.InstallationID(), InputDigest: candidate.InputDigest, Environment: candidate.Environment}
	if err := driver.Preflight(ctx); err != nil {
		return err
	}
	return driver.verifyImage(ctx, driver.newSandbox(target, candidate.OperationID), candidate.Image)
}
