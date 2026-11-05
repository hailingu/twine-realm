// 本文件读取显式注入的受限文件，不接受符号链接、宽松权限或无限长材料。
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// ReadPassword 的返回值只交给 SQL 驱动，不进入配置显示、错误、日志或 API。
func ReadPassword(path string) (value string, err error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("密码文件必须使用受控绝对路径")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", fmt.Errorf("密码文件不可读取")
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return "", fmt.Errorf("密码文件须为仅所有者可访问的普通文件")
	}
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(data) > 4096 {
		return "", fmt.Errorf("密码文件读取失败或超过大小限制")
	}
	data = bytes.TrimSuffix(data, []byte("\n"))
	data = bytes.TrimSuffix(data, []byte("\r"))
	if len(data) < 16 || bytes.ContainsAny(data, "\x00\r\n") {
		return "", fmt.Errorf("密码文件内容无效")
	}
	return string(data), nil
}
