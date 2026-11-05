// 本文件只读取明确目录内的固定规范，不执行文档或用户提供的命令。
package engineering

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"syscall"
)

var contextBlock = regexp.MustCompile("(?s)```twine-engineering[ \t]*\r?\n(.*?)\r?\n```")

// lock 固定服务已接受的方案身份，而非按用户文件建立新支持目录。
type lock struct {
	SchemaVersion int      `json:"schemaVersion"`
	Profile       Identity `json:"profile"`
}

// references 是 AGENTS 中的明确机器引用；不表示 agents 已收到原文。
type references struct {
	SchemaVersion int      `json:"schemaVersion"`
	Profile       string   `json:"profile"`
	Version       string   `json:"version"`
	Scope         []string `json:"scope"`
	Documents     []string `json:"documents"`
}

// Validate 核实每份完整快照与兼容声明；用户源码仍须执行实际检查。
func (catalog *Catalog) Validate(directory string) (value Validation, err error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return value, failure("ENGINEERING_DIRECTORY_UNAVAILABLE", "服务目录不可读取")
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	if err := catalog.validateBinding(root); err != nil {
		return value, err
	}
	if err := catalog.validateReferences(root); err != nil {
		return value, err
	}
	for _, document := range catalog.profile.Documents {
		data, err := readRootFile(root, document.Path)
		if err != nil {
			return value, err
		}
		if digest(data) != document.Digest {
			return value, failure("ENGINEERING_DOCUMENT_CHANGED", "必需工程文档与已接受版本不一致")
		}
	}
	if err := catalog.validateModule(root); err != nil {
		return value, err
	}
	return Validation{Profile: catalog.Identity(), Template: catalog.profile.Template, Documents: catalog.Profile().Documents, State: "documents-verified"}, nil
}

// validateBinding 不允许改写支持范围或仅修改锁的摘要来伪造新包。
func (catalog *Catalog) validateBinding(root *os.Root) error {
	data, err := readRootFile(root, ".twine/engineering.lock.json")
	if err != nil {
		return err
	}
	var binding lock
	if decode(data, &binding) != nil || binding.SchemaVersion != 1 || binding.Profile != catalog.Identity() {
		return failure("ENGINEERING_PROFILE_UNSUPPORTED", "工程方案版本、摘要或兼容组合未受支持")
	}
	data, err = readRootFile(root, ".twine/profile.json")
	if err != nil {
		return err
	}
	if digest(data) != catalog.release.ProfileDigest {
		return failure("ENGINEERING_PROFILE_CHANGED", "工程方案内容已改变，须显式接受支持版本")
	}
	return nil
}

// validateReferences 要求单一、完整、范围明确的相对引用，不扫描其他项目。
func (catalog *Catalog) validateReferences(root *os.Root) error {
	data, err := readRootFile(root, "AGENTS.md")
	if err != nil {
		return err
	}
	blocks := contextBlock.FindAllSubmatch(data, 2)
	var context references
	if len(blocks) != 1 || decode(blocks[0][1], &context) != nil || context.SchemaVersion != 1 || context.Profile != catalog.profile.ID || context.Version != catalog.profile.Version {
		return failure("ENGINEERING_REFERENCES_INVALID", "AGENTS 缺少单一明确的工程包引用")
	}
	if len(context.Scope) != 1 || context.Scope[0] != "**/*" || len(context.Documents) != len(catalog.profile.Documents) {
		return failure("ENGINEERING_REFERENCES_INVALID", "工程规范范围或引用集合不完整")
	}
	seen := map[string]bool{}
	for _, document := range context.Documents {
		if !fs.ValidPath(document) || seen[document] {
			return failure("ENGINEERING_REFERENCE_BOUNDARY", "工程规范引用越界、重复或无效")
		}
		seen[document] = true
	}
	for _, document := range catalog.profile.Documents {
		if !seen[document.Path] {
			return failure("ENGINEERING_REFERENCES_INVALID", "AGENTS 未引用全部必需工程规范")
		}
	}
	return nil
}

// validateModule 只验证本组合的固定工具 / 必需库声明，不冒充 go 命令的语法或完整依赖检查。
func (catalog *Catalog) validateModule(root *os.Root) error {
	data, err := readRootFile(root, "go.mod")
	if err != nil {
		return err
	}
	version := regexp.MustCompile(`(?m)^go[ \t]+` + regexp.QuoteMeta(catalog.profile.Stack.GoVersion) + `[ \t]*$`)
	unsupported := regexp.MustCompile(`(?m)^[ \t]*(replace|exclude|toolchain)[ \t(]`)
	if !version.Match(data) || unsupported.Match(data) {
		return failure("ENGINEERING_TOOLCHAIN_INCOMPATIBLE", "服务 Go 版本或模块覆盖不属于已验证组合")
	}
	for _, dependency := range catalog.profile.Stack.Dependencies {
		pattern := regexp.MustCompile(`(?m)^[ \t]*(require[ \t]+)?` + regexp.QuoteMeta(dependency.Module) + `[ \t]+` + regexp.QuoteMeta(dependency.Version) + `[ \t]*(//[^\r\n]*)?$`)
		if !pattern.Match(data) {
			return failure("ENGINEERING_DEPENDENCY_INCOMPATIBLE", "服务缺少已验证的必要依赖版本")
		}
	}
	sums, err := readRootFile(root, "go.sum")
	if err != nil {
		return err
	}
	if len(sums) == 0 {
		return failure("ENGINEERING_DEPENDENCY_UNLOCKED", "服务依赖锁定文件为空")
	}
	return nil
}

// readRootFile 同时拒绝路径跳转、任一符号链接、非普通文件与超大材料。
func readRootFile(root *os.Root, name string) (data []byte, err error) {
	if !fs.ValidPath(name) || len(name) > 512 {
		return nil, failure("ENGINEERING_REFERENCE_BOUNDARY", "工程规范引用超出服务目录")
	}
	prefix := ""
	for _, part := range strings.Split(name, "/") {
		prefix = strings.TrimPrefix(prefix+"/"+part, "/")
		info, err := root.Lstat(prefix)
		if err != nil {
			return nil, failure("ENGINEERING_DOCUMENT_MISSING", "必需工程材料缺失或不可读取")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, failure("ENGINEERING_REFERENCE_BOUNDARY", "工程规范引用不得经过符号链接")
		}
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, failure("ENGINEERING_DOCUMENT_MISSING", "必需工程材料不可读取")
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
		return nil, failure("ENGINEERING_DOCUMENT_INVALID", "工程材料类型或大小无效")
	}
	data, err = io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(data) > 65536 {
		return nil, failure("ENGINEERING_DOCUMENT_INVALID", "工程材料读取失败或超过大小限制")
	}
	return data, nil
}
