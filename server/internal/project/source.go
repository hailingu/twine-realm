// 本文件分离已固定源码与可变化的 Git 观察，不改变原项目快照契约。
package project

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
)

var sourceCommitPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// SourceVersion 是已持久化的服务源码关联；空固定值仍为未配置草稿。
type SourceVersion struct {
	ProjectID       string  `json:"projectId"`
	ProjectRevision string  `json:"projectRevision"`
	ServiceID       string  `json:"serviceId"`
	RepositoryID    string  `json:"repositoryId"`
	RepositoryPath  string  `json:"repositoryPath"`
	PinnedCommit    *string `json:"pinnedCommit"`
}

// GitObservation 表示带时间的只读观察，未知值不伪报为干净或可用。
type GitObservation struct {
	Available       bool    `json:"available"`
	ObservedAt      string  `json:"observedAt"`
	HeadCommit      *string `json:"headCommit"`
	Branch          *string `json:"branch"`
	Dirty           *bool   `json:"dirty"`
	PinnedAvailable *bool   `json:"pinnedAvailable"`
	Warning         *string `json:"warning"`
	Problem         *Error  `json:"problem"`
}

// SourceObservation 同时返回已保存版本和独立观察，仓库失联不抹除已固定值。
type SourceObservation struct {
	Version SourceVersion  `json:"version"`
	Git     GitObservation `json:"git"`
}

// PinSourceCommand 只固定完整 commit，不隐含暂存、提交或切换分支。
type PinSourceCommand struct {
	MutationID   string `json:"mutationId"`
	BaseRevision string `json:"baseRevision"`
	Commit       string `json:"commit"`
}

// Source 先读取稳定关联，再观察可变工作区；观察失败仍返回已有固定版本。
func (service *Service) Source(ctx context.Context, id, serviceID string) (SourceObservation, error) {
	if !ValidID(id) || !ValidID(serviceID) {
		return SourceObservation{}, Failure("INVALID_REQUEST", "项目或服务身份无效")
	}
	version, err := service.reader.Source(ctx, id, serviceID)
	if err != nil {
		return SourceObservation{}, err
	}
	observation, err := service.inspector.Observe(ctx, version.RepositoryPath, version.PinnedCommit)
	if err != nil {
		failure := Failure("GIT_READ_FAILED", "当前仓库无法可靠观察，已有固定版本保持不变")
		var domain *Error
		if errors.As(err, &domain) {
			failure = domain
		}
		observation = GitObservation{ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Problem: failure}
	}
	return SourceObservation{Version: version, Git: observation}, nil
}

// PinSource 在原键回放之后核实 commit，再交由元数据库原子保存选择和回执。
func (service *Service) PinSource(ctx context.Context, id, serviceID string, command PinSourceCommand) (Receipt, error) {
	command.Commit = strings.TrimSpace(command.Commit)
	if err := validMutation(id, command.MutationID, command.BaseRevision); err != nil {
		return Receipt{}, err
	}
	if !ValidID(serviceID) || !sourceCommitPattern.MatchString(command.Commit) {
		return Receipt{}, Failure("INVALID_COMMIT", "请选择完整的小写 commit 标识")
	}
	hash, err := requestHash("pinSource", id, struct {
		ServiceID string
		Command   PinSourceCommand
	}{serviceID, command})
	if err != nil {
		return Receipt{}, err
	}
	if receipt, found, err := service.replay(ctx, command.MutationID, hash); found || err != nil {
		return receipt, err
	}
	version, err := service.reader.Source(ctx, id, serviceID)
	if err != nil {
		return Receipt{}, err
	}
	if version.ProjectRevision != command.BaseRevision {
		return Receipt{}, &Error{Code: "REVISION_CONFLICT", Message: "项目已被其他窗口更新，请载入最新版本后重新确认源码", CurrentRevision: version.ProjectRevision}
	}
	if err := service.inspector.VerifyCommit(ctx, version.RepositoryPath, command.Commit); err != nil {
		return Receipt{}, err
	}
	return service.writer.PinSource(ctx, id, serviceID, command, hash)
}
