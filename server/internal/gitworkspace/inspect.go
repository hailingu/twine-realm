// Package gitworkspace 只读观测本机允许目录内的 Git 工作区。
package gitworkspace

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/hailingu/twine-realm/server/internal/project"
)

var commitPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// Inspector 保持初始化时明确批准的规范目录集合。
type Inspector struct {
	roots []string
	git   string
}

// New 校验真实目录和 Git 可执行文件，拒绝允许整个文件系统。
func New(roots []string) (*Inspector, error) {
	if len(roots) == 0 {
		return nil, project.Failure("INVALID_ROOTS", "必须配置至少一个仓库允许目录")
	}
	inspector := &Inspector{}
	for _, root := range roots {
		path, err := canonical(root)
		if err != nil || path == string(filepath.Separator) {
			return nil, project.Failure("INVALID_ROOTS", "仓库允许目录无效")
		}
		inspector.roots = append(inspector.roots, path)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return nil, project.Failure("GIT_UNAVAILABLE", "未找到本机 Git，请先安装")
	}
	inspector.git = git
	return inspector, nil
}

// Inspect 解析真实工作区和 Git 元数据，任何持久化之前完成只读校验。
func (inspector *Inspector) Inspect(ctx context.Context, path string) (project.RepositorySource, error) {
	if len(path) > 4096 || !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return project.RepositorySource{}, project.Failure("INVALID_PATH", "请输入允许目录内的绝对路径")
	}
	resolved, err := canonical(path)
	if err != nil {
		return project.RepositorySource{}, project.Failure("PATH_UNREADABLE", "目录不存在或不可读取")
	}
	if !inspector.allowed(resolved) {
		return project.RepositorySource{}, project.Failure("PATH_NOT_ALLOWED", "目录不在本机允许的仓库范围内")
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	root, err := inspector.run(ctx, resolved, "--show-toplevel")
	if err != nil {
		return project.RepositorySource{}, project.Failure("NOT_GIT_WORKTREE", "该目录不是可读取的 Git 工作区")
	}
	root, err = canonical(root)
	if err != nil || !inspector.allowed(root) {
		return project.RepositorySource{}, project.Failure("PATH_NOT_ALLOWED", "仓库根目录不在允许范围内")
	}
	if err := inspector.checkMetadata(ctx, root); err != nil {
		return project.RepositorySource{}, err
	}
	head, err := inspector.run(ctx, root, "--verify", "--quiet", "HEAD")
	if err == nil && commitPattern.MatchString(head) {
		return project.RepositorySource{Path: root, HeadCommit: &head}, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return project.RepositorySource{Path: root, HeadCommit: nil}, nil
	}
	return project.RepositorySource{}, project.Failure("GIT_READ_FAILED", "无法读取仓库版本，请检查 Git 元数据")
}

// checkMetadata 同时限制工作区私有元数据和公共对象库的位置。
func (inspector *Inspector) checkMetadata(ctx context.Context, root string) error {
	for _, argument := range []string{"--absolute-git-dir", "--git-common-dir"} {
		metadata, err := inspector.run(ctx, root, "--path-format=absolute", argument)
		if err != nil {
			return project.Failure("NOT_GIT_WORKTREE", "Git 元数据不可读取")
		}
		metadata, err = canonical(metadata)
		if err != nil || !inspector.allowed(metadata) {
			return project.Failure("GIT_METADATA_NOT_ALLOWED", "Git 元数据不在允许范围内")
		}
	}
	return nil
}

// allowed 使用目录边界而非字符串前缀，阻止同名前缀和符号链接越界。
func (inspector *Inspector) allowed(path string) bool {
	for _, root := range inspector.roots {
		relative, err := filepath.Rel(root, path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// canonical 解析链接并确认当前账号可以列举目录。
func canonical(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode().Perm()&0444 == 0 || info.Mode().Perm()&0111 == 0 {
		return "", os.ErrPermission
	}
	if _, err := os.ReadDir(resolved); err != nil {
		return "", err
	}
	return filepath.Abs(resolved)
}

// run 固定命令与环境，不继承 Git 重定向、外部过滤器或用户配置。
func (inspector *Inspector) run(ctx context.Context, path string, arguments ...string) (string, error) {
	args := append([]string{"-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-C", path, "rev-parse"}, arguments...)
	command := exec.CommandContext(ctx, inspector.git, args...)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "LANG=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_SYSTEM=" + os.DevNull, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0"}
	var output bytes.Buffer
	command.Stdout = &output
	if err := command.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(output.String()), nil
}
