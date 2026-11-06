// 本文件合并平台代码、静态文件与显式仓库的禁止范围，不创建或修改这些目录。
package privatefiles

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
		return nil, failure("DIRECTORY_INVALID", "无法核实平台工作目录", false, false)
	}
	values := append([]string{}, repositories...)
	values = append(values, working)
	if webDirectory != "" {
		absolute, err := filepath.Abs(webDirectory)
		if err != nil {
			return nil, failure("DIRECTORY_INVALID", "静态目录配置无效", false, false)
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
			return nil, failure("DIRECTORY_INVALID", "无法核实平台源码边界", false, false)
		}
		if filepath.Dir(path) == path {
			break
		}
	}
	return values, nil
}
