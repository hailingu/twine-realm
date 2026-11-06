// 本文件将执行器错误限制为固定诊断，原始工具输出不进入API或事件。
package servicebuild

// Fault 表达执行失败与外部结果未知的区别，不携带业务秘密。
type Fault struct {
	Code, Message string
	Unknown       bool
}

// Error 返回可直接呈现的固定诊断。
func (fault *Fault) Error() string { return fault.Message }

// failure 建立已知失败；只有证据不完整时另外标记Unknown。
func failure(code, message string) *Fault { return &Fault{Code: code, Message: message} }
