// 本文件补齐平台源码与静态目录的禁止范围，不靠.gitignore保护运行秘密。
package mysqlruntime

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// LocalForbidden 合并显式仓库根、当前平台Git根和静态目录，不修改传入数组。
func LocalForbidden(repositories []string, webDirectory string) ([]string, error) {
	working, err := os.Getwd()
	if err != nil {
		return nil, failure("RUNTIME_DIRECTORY_INVALID", "无法核实平台工作目录", false, false)
	}
	values := append([]string{}, repositories...)
	values = append(values, working)
	if webDirectory != "" {
		absolute, err := filepath.Abs(webDirectory)
		if err != nil {
			return nil, failure("RUNTIME_DIRECTORY_INVALID", "静态目录配置无效", false, false)
		}
		values = append(values, absolute)
	}
	for path := working; ; path = filepath.Dir(path) {
		_, err := os.Lstat(filepath.Join(path, ".git"))
		if err == nil {
			values = append(values, path)
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, failure("RUNTIME_DIRECTORY_INVALID", "无法核实平台源码边界", false, false)
		}
		if filepath.Dir(path) == path {
			break
		}
	}
	return values, nil
}

// forbiddenDirectory 规范化尚未创建的禁止范围的既有父路径，不从缺失路径保留链接别名。
func forbiddenDirectory(absolute string) (string, error) {
	suffix := ""
	for current := absolute; ; current = filepath.Dir(current) {
		canonical, err := filepath.EvalSymlinks(current)
		if err == nil {
			return filepath.Join(canonical, suffix), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		if _, statErr := os.Lstat(current); !errors.Is(statErr, fs.ErrNotExist) {
			return "", err
		}
		if filepath.Dir(current) == current {
			return "", err
		}
		suffix = filepath.Join(filepath.Base(current), suffix)
	}
}
