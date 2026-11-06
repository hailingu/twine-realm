// Package privatefiles 为本机受信执行器提供私有目录、不可覆盖材料及完整路径边界，不是用户账号或加密凭据库。
package privatefiles

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const invalidPathCharacters = "$\r\n\x00"

// newDirectoryPath 核实父目录和范围，绝不为宽泛或冲突路径修改权限。
func newDirectoryPath(directory string, forbidden []string) (string, error) {
	if !filepath.IsAbs(directory) || strings.ContainsAny(directory, invalidPathCharacters) {
		return "", failure("DIRECTORY_INVALID", "运行根须为不含插值字符的绝对路径", false, false)
	}
	clean := filepath.Clean(directory)
	if _, err := os.Lstat(clean); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return "", failure("DIRECTORY_EXISTS", "运行根已存在或无法安全核实，原内容保留", false, false)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(clean))
	if err != nil {
		return "", failure("DIRECTORY_INVALID", "运行根的父目录必须已经存在", false, false)
	}
	path := filepath.Join(parent, filepath.Base(clean))
	if strings.ContainsAny(path, invalidPathCharacters) {
		return "", failure("DIRECTORY_INVALID", "规范运行路径不得含插值字符", false, false)
	}
	if err := outsideRepositories(path, forbidden); err != nil {
		return "", err
	}
	return path, nil
}

// existingDirectoryPath 允许系统父路径的规范化，根本身与内部材料禁止符号链接。
func existingDirectoryPath(directory string, forbidden []string) (string, error) {
	if !filepath.IsAbs(directory) || strings.ContainsAny(directory, invalidPathCharacters) {
		return "", failure("DIRECTORY_INVALID", "运行根配置无效", false, false)
	}
	info, err := os.Lstat(directory)
	if err != nil || !privateInfo(info, true) {
		return "", failure("DIRECTORY_UNSAFE", "运行根必须由当前用户拥有且权限为0700，不得使用链接", false, false)
	}
	path, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return "", failure("DIRECTORY_UNAVAILABLE", "无法核实私有根", false, false)
	}
	if strings.ContainsAny(path, invalidPathCharacters) {
		return "", failure("DIRECTORY_INVALID", "规范运行路径不得含插值字符", false, false)
	}
	if err := outsideRepositories(path, forbidden); err != nil {
		return "", err
	}
	return path, nil
}

// outsideRepositories 同时拒绝仓库内部与包含仓库的宽泛根。
func outsideRepositories(path string, forbidden []string) error {
	for _, directory := range forbidden {
		absolute, err := filepath.Abs(directory)
		if err != nil {
			return failure("DIRECTORY_INVALID", "禁止范围配置无效", false, false)
		}
		canonical, err := forbiddenDirectory(absolute)
		if err != nil {
			return failure("DIRECTORY_INVALID", "无法核实禁止范围", false, false)
		}
		if within(path, canonical) || within(canonical, path) {
			return failure("DIRECTORY_UNSAFE", "运行根必须位于仓库与平台静态目录范围之外", false, false)
		}
	}
	return nil
}

// within 以路径段核实包含关系，避免字符串前缀误判相邻目录。
func within(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && (relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
}

// privateInfo 核实对象类型、权限与当前 OS 所有者。
func privateInfo(info fs.FileInfo, directory bool) bool {
	if info == nil || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Getuid() {
		return false
	}
	if directory {
		return info.IsDir() && info.Mode().Perm() == 0700
	}
	return info.Mode().IsRegular() && info.Mode().Perm() == 0600
}

// CheckParents 核实根内全部父目录，不把链接或额外权限当普通路径使用。
func (files *Root) CheckParents(name string) error {
	opened, err := files.root.Stat(".")
	if err != nil {
		return failure("DIRECTORY_UNAVAILABLE", "私有运行根不可访问", false, false)
	}
	current, err := os.Lstat(files.directory)
	if err != nil || !privateInfo(opened, true) || !privateInfo(current, true) || !os.SameFile(opened, current) {
		return failure("DIRECTORY_UNSAFE", "私有根权限或路径已改变，停止读取与执行", false, false)
	}
	if !fs.ValidPath(name) || strings.Contains(name, "\\") {
		return failure("FILE_UNSAFE", "运行材料路径无效", false, false)
	}
	parts := strings.Split(name, "/")
	for index := 1; index < len(parts); index++ {
		info, err := files.root.Lstat(strings.Join(parts[:index], "/"))
		if err != nil || !privateInfo(info, true) {
			return failure("FILE_UNSAFE", "运行材料父目录不存在、权限不安全或含链接", false, false)
		}
	}
	return nil
}

// Read 非阻塞且不跟随最终链接，只接受有界0600普通文件。
func (files *Root) Read(name string, limit int64) (data []byte, err error) {
	if err := files.CheckParents(name); err != nil {
		return nil, err
	}
	file, err := files.root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fs.ErrNotExist
	}
	if err != nil {
		return nil, failure("FILE_UNSAFE", "运行材料无法安全读取", false, false)
	}
	defer joinClose(&err, file)
	info, err := file.Stat()
	if err != nil || !privateInfo(info, false) || info.Size() > limit {
		return nil, failure("FILE_UNSAFE", "运行材料必须为当前用户的0600有界普通文件", false, false)
	}
	data, err = io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, failure("FILE_UNSAFE", "运行材料读取失败或超限", false, false)
	}
	return data, nil
}

// WriteNew 仅独占创建固定材料，失败保留现场，不删除可能被后来写入的数据。
func (files *Root) WriteNew(name string, data []byte) (err error) {
	if err := files.CheckParents(name); err != nil {
		return err
	}
	file, err := files.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return failure("FILE_CONFLICT", "运行材料已存在或不能独占创建，原内容保留", false, false)
	}
	defer joinClose(&err, file)
	if _, err := file.Write(data); err != nil {
		return failure("FILE_UNAVAILABLE", "运行材料写入失败，保留现场", false, false)
	}
	if err := file.Sync(); err != nil {
		return failure("FILE_UNAVAILABLE", "运行材料同步失败，保留现场", false, false)
	}
	return nil
}

// Ensure 保留原字节，不能通过覆盖既有运行材料偷换凭据或配置。
func (files *Root) Ensure(name string, data []byte) error {
	current, err := files.Read(name, 65536)
	if errors.Is(err, fs.ErrNotExist) {
		return files.WriteNew(name, data)
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(current, data) {
		return failure("FILE_CONFLICT", "既有运行材料与固定目标不符，保留现场", false, false)
	}
	return nil
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
