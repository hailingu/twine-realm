// Package engineering 发布首个离线业务工程包，核实文件而不宣称 agents 已加载原文。
package engineering

// Identity 固定工程包身份；摘要根据发布材料原始字节计算。
type Identity struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

// Dependency 表达本组合已验证的直接依赖，不替代服务实际 go.sum。
type Dependency struct {
	Module  string `json:"module"`
	Version string `json:"version"`
}

// Stack 只声明已验证的语言、框架、工具链与运行系统。
type Stack struct {
	Language     string       `json:"language"`
	Framework    string       `json:"framework"`
	GoVersion    string       `json:"goVersion"`
	Platforms    []string     `json:"platforms"`
	Dependencies []Dependency `json:"dependencies"`
}

// Document 固定完整离线快照与适用角色，路径相对于服务仓库。
type Document struct {
	Path   string `json:"path"`
	Role   string `json:"role"`
	Digest string `json:"digest"`
}

// Runtime 声明模板实际消费的命名、命令及探针；不代表已部署。
type Runtime struct {
	HTTPPort                 int        `json:"httpPort"`
	DefaultAddress           string     `json:"defaultAddress"`
	LivePath                 string     `json:"livePath"`
	ReadyPath                string     `json:"readyPath"`
	ExamplePath              string     `json:"examplePath"`
	PasswordFileEnv          string     `json:"passwordFileEnv"`
	MigrationPasswordFileEnv string     `json:"migrationPasswordFileEnv"`
	Commands                 [][]string `json:"commands"`
}

// Profile 是受控发布的组合、规范、模板来源和真实检查入口。
type Profile struct {
	SchemaVersion int        `json:"schemaVersion"`
	ID            string     `json:"id"`
	Version       string     `json:"version"`
	Template      Identity   `json:"template"`
	Stack         Stack      `json:"stack"`
	Documents     []Document `json:"documents"`
	Runtime       Runtime    `json:"runtime"`
	Checks        [][]string `json:"checks"`
}

// Validation 只记录实际文件核实结果，不记录模型已读或业务检查已通过。
type Validation struct {
	Profile   Identity   `json:"profile"`
	Template  Identity   `json:"template"`
	Documents []Document `json:"verifiedDocuments"`
	State     string     `json:"state"`
}

// Error 提供安全的阻塞原因，不回显文档、密码或宿主路径内容。
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error 返回可展示诊断，机器使用 Code 判断阻塞类型。
func (err *Error) Error() string { return err.Message }

// failure 统一创建不含外部材料的固定诊断。
func failure(code, message string) error { return &Error{Code: code, Message: message} }
