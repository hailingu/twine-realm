// 本文件在读取 commit 前验证公共对象库及其 alternates 的允许范围。
package gitworkspace

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// checkObjects 限制共享对象库的真实目录，循环引用不会导致无限遍历。
func (inspector *Inspector) checkObjects(ctx context.Context, path string) error {
	root, err := inspector.run(ctx, path, "--path-format=absolute", "--git-path", "objects")
	if err != nil {
		return project.Failure("GIT_READ_FAILED", "无法读取仓库对象存储")
	}
	pending, seen := []string{root}, map[string]bool{}
	for len(pending) > 0 {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		current, err := canonical(pending[0])
		pending = pending[1:]
		if err != nil || !inspector.allowed(current) {
			return project.Failure("GIT_METADATA_NOT_ALLOWED", "Git 对象库不在允许范围内")
		}
		if seen[current] {
			continue
		}
		seen[current] = true
		if len(seen) > 32 {
			return project.Failure("GIT_METADATA_NOT_ALLOWED", "Git 共享对象库数量超出当前支持范围")
		}
		additional, err := inspector.alternateObjects(current)
		if err != nil {
			return err
		}
		pending = append(pending, additional...)
	}
	return nil
}

// alternateObjects 先解析文件链接再读取，禁止从元数据链接绕过路径允许范围。
func (inspector *Inspector) alternateObjects(root string) ([]string, error) {
	file, err := filepath.EvalSymlinks(filepath.Join(root, "info", "alternates"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !inspector.allowed(file) {
		return nil, project.Failure("GIT_METADATA_NOT_ALLOWED", "Git 共享对象库配置不可读取或越界")
	}
	handle, err := os.Open(file)
	if err != nil {
		return nil, project.Failure("GIT_READ_FAILED", "无法读取 Git 共享对象库配置")
	}
	data, readErr := io.ReadAll(io.LimitReader(handle, 65537))
	closeErr := handle.Close()
	if readErr != nil || closeErr != nil || len(data) > 65536 {
		return nil, project.Failure("GIT_READ_FAILED", "Git 共享对象库配置不完整或超限")
	}
	var result []string
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		if !filepath.IsAbs(line) {
			line = filepath.Join(root, line)
		}
		result = append(result, line)
	}
	return result, nil
}
