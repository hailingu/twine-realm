// 本文件只读观察当前工作区与核实固定 commit，不对仓库加锁或执行配置脚本。
package gitworkspace

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// sourceRepository 复核已登记的规范根与对象存储，避免关联漂移和越界读取。
func (inspector *Inspector) sourceRepository(ctx context.Context, path string) (project.RepositorySource, error) {
	source, err := inspector.Inspect(ctx, path)
	if err != nil {
		return source, err
	}
	if source.Path != path {
		return source, project.Failure("REPOSITORY_CHANGED", "仓库定位已变化，请先核实原仓库关联")
	}
	if err := inspector.checkObjects(ctx, path); err != nil {
		return source, err
	}
	return source, nil
}

// VerifyCommit 仅接受本仓库对象存储中的完整 commit，不将 tag/tree/blob 当源码版本。
func (inspector *Inspector) VerifyCommit(ctx context.Context, path, commit string) error {
	if !commitPattern.MatchString(commit) {
		return project.Failure("INVALID_COMMIT", "固定版本必须为完整 commit 标识")
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	if _, err := inspector.sourceRepository(ctx, path); err != nil {
		return err
	}
	kind, err := inspector.command(ctx, path, "cat-file", "-t", commit)
	if err != nil {
		return project.Failure("COMMIT_NOT_FOUND", "此仓库中不存在或无法读取该 commit 对象")
	}
	if kind != "commit" {
		return project.Failure("INVALID_COMMIT_OBJECT", "请选择 commit 对象，不能固定 tag、tree 或 blob")
	}
	return nil
}

// Observe 返回可变化的当前状态，调用者的已固定值不被 HEAD 或工作区变化覆盖。
func (inspector *Inspector) Observe(ctx context.Context, path string, pinned *string) (project.GitObservation, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	source, err := inspector.sourceRepository(ctx, path)
	if err != nil {
		return project.GitObservation{}, err
	}
	value := project.GitObservation{Available: true, HeadCommit: source.HeadCommit}
	branch, err := inspector.command(ctx, path, "symbolic-ref", "--quiet", "--short", "HEAD")
	var exit *exec.ExitError
	if err == nil {
		value.Branch = &branch
	} else if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		return value, project.Failure("GIT_READ_FAILED", "无法核实当前分支状态")
	}
	if source.HeadCommit != nil && !inspector.isCommit(ctx, path, *source.HeadCommit) {
		return value, project.Failure("INVALID_COMMIT_OBJECT", "当前 HEAD 不是可读取的 commit")
	}
	value.Dirty, value.Warning = inspector.worktreeState(ctx, path)
	if pinned != nil {
		available := inspector.isCommit(ctx, path, *pinned)
		value.PinnedAvailable = &available
		if !available {
			message := "已固定 commit 在当前仓库不可用，固定记录仍然保留"
			if value.Warning != nil {
				message += "；" + *value.Warning
			}
			value.Warning = &message
		}
	}
	value.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return value, nil
}

// isCommit 不接受替换引用或可解析为其他对象类型的标识。
func (inspector *Inspector) isCommit(ctx context.Context, path, commit string) bool {
	kind, err := inspector.command(ctx, path, "cat-file", "-t", commit)
	return err == nil && kind == "commit"
}

// worktreeState 不运行 Git clean/process 过滤器，也不递归执行子模块配置。
func (inspector *Inspector) worktreeState(ctx context.Context, path string) (*bool, *string) {
	keys, err := inspector.command(ctx, path, "config", "--includes", "--name-only", "--get-regexp", `^filter\..*\.(clean|smudge|process)$`)
	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1) {
		return unknownWorktree()
	}
	modes, err := inspector.command(ctx, path, "ls-files", "--format=%(objectmode)")
	if err != nil {
		return unknownWorktree()
	}
	if keys != "" || strings.Contains(modes, "160000") {
		message := "包含外部 Git 过滤器或子模块，未执行其脚本，工作区修改状态未知"
		return nil, &message
	}
	status, err := inspector.command(ctx, path, "status", "--porcelain=v1", "-z", "--untracked-files=normal", "--ignore-submodules=all")
	if err != nil {
		return unknownWorktree()
	}
	dirty := status != ""
	return &dirty, nil
}

// unknownWorktree 公开不完整观察，不能把失败解释为工作区干净。
func unknownWorktree() (*bool, *string) {
	message := "工作区修改状态暂无法完整核实，请保留当前选择并稍后刷新"
	return nil, &message
}
