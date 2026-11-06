// 本文件定义固定源码的实际导出证据，不把工作区或文件路径当内容身份。
package project

// 源码导出上限属于第一构建策略，不能由浏览器任意放宽。
const (
	SourceExportBytes = int64(64 * 1024 * 1024)
	SourceExportFiles = 4096
)

// SourceFile 固定原始提交中一个普通文件的字节、大小与执行权限。
type SourceFile struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	Executable bool   `json:"executable"`
	Digest     string `json:"digest"`
}

// SourceMaterial 记录受信Git对象读取的实际SHA256快照；不是签名或工作区状态。
type SourceMaterial struct {
	Commit     string       `json:"commit"`
	Tree       string       `json:"tree"`
	Digest     string       `json:"digest"`
	Files      []SourceFile `json:"files"`
	GitVersion string       `json:"gitVersion"`
}
