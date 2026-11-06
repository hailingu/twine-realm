// 本文件拥有私有目录句柄及其初始化，故障时保留已有内容。
package privatefiles

import (
	"errors"
	"io"
	"io/fs"
	"os"
)

// Error 只提供固定安全诊断；调用方映射为其发布的领域错误码。
type Error struct {
	Code, Message      string
	Retryable, Unknown bool
}

// Error 返回固定文案，不回显外部文件内容或秘密。
func (err *Error) Error() string { return err.Message }

// failure 在文件信任边界建立可映射的失败，不将失败降级成空成功。
func failure(code, message string, retryable, unknown bool) *Error {
	return &Error{code, message, retryable, unknown}
}

// Root 拥有受限文件描述符和打开时的规范路径，调用方在其执行器退出后关闭。
type Root struct {
	root      *os.Root
	directory string
}

// Create 只创建明确的全新0700目录；部分初始化失败时保留现场而不删除。
func Create(directory string, forbidden []string) (*Root, error) {
	path, err := newDirectoryPath(directory, forbidden)
	if err != nil {
		return nil, err
	}
	if err := os.Mkdir(path, 0700); err != nil {
		return nil, failure("DIRECTORY_EXISTS", "私有根必须是不存在的新目录，已有内容保留", false, false)
	}
	return openRoot(path)
}

// Open 只打开当前用户拥有的既有0700目录，不修复权限或重新初始化身份。
func Open(directory string, forbidden []string) (*Root, error) {
	path, err := existingDirectoryPath(directory, forbidden)
	if err != nil {
		return nil, err
	}
	return openRoot(path)
}

// openRoot 取得有界路径句柄，不向调用方暴露不受检查的文件描述符。
func openRoot(path string) (*Root, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, failure("DIRECTORY_UNAVAILABLE", "私有目录不可访问，保留现场", false, false)
	}
	return &Root{root: root, directory: path}, nil
}

// Path 返回已经规范化的非敏感位置，外部命令仍须自行核实完整归属。
func (files *Root) Path() string { return files.directory }

// Close 只释放本句柄，不停止外部资源或删除数据。
func (files *Root) Close() error { return files.root.Close() }

// Mkdir 新建或核实0700子目录，不改变已存在目录的权限或类型。
func (files *Root) Mkdir(name string) error {
	if err := files.CheckParents(name); err != nil {
		return err
	}
	if err := files.root.Mkdir(name, 0700); err != nil && !errors.Is(err, fs.ErrExist) {
		return failure("DIRECTORY_UNAVAILABLE", "无法准备私有子目录", false, false)
	}
	return files.CheckParents(name + "/entry")
}

// joinClose 同时保留执行和关闭错误，不让清理覆盖原始失败。
func joinClose(err *error, closer io.Closer) { *err = errors.Join(*err, closer.Close()) }
