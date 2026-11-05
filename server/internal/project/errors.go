// 本文件定义可安全向用户展示的领域失败，原始原因只在内部保留。
package project

// Error 以稳定代码表达可恢复失败及适用的冲突定位。
type Error struct {
	Code              string `json:"code"`
	Message           string `json:"message"`
	CurrentRevision   string `json:"currentRevision,omitempty"`
	ExistingServiceID string `json:"existingServiceId,omitempty"`
	Cause             error  `json:"-"`
}

// Error 返回安全文案，不拼接文件系统、数据库或 Git 的原始错误。
func (failure *Error) Error() string { return failure.Message }

// Unwrap 保留内部诊断错误链，不改变公开错误内容。
func (failure *Error) Unwrap() error { return failure.Cause }

// Failure 创建没有敏感细节的领域失败。
func Failure(code, message string) *Error { return &Error{Code: code, Message: message} }
