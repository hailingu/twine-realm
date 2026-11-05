// 本文件只读取当前二进制内的受控包，不从用户文件声明建立新信任来源。
package engineering

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
)

//go:embed all:assets/go-net-http-v1
var assets embed.FS

const bundleRoot = "assets/go-net-http-v1"

// releaseFile 区分发布源文件与导出文件名，Go 模块清单在导出时恢复真实名称。
type releaseFile struct {
	Path   string `json:"path"`
	Source string `json:"source"`
	Digest string `json:"digest"`
	Size   int    `json:"size"`
}

// release 绑定整个原始模板；后续服务源码修改仍由真实检查验证。
type release struct {
	SchemaVersion int           `json:"schemaVersion"`
	ID            string        `json:"id"`
	Version       string        `json:"version"`
	ProfileDigest string        `json:"profileDigest"`
	Files         []releaseFile `json:"files"`
}

// Catalog 拥有已核实内置版本；调用方不能注册任意 URL 或替换其材料。
type Catalog struct {
	profile Profile
	release release
}

// Load 先核实发布清单和完整材料，缺失或漂移时不开放导出 / 验证能力。
func Load() (*Catalog, error) {
	data, err := assets.ReadFile(bundleRoot + "/release.json")
	if err != nil {
		return nil, failure("ENGINEERING_BUNDLE_UNAVAILABLE", "内置工程包不可读取")
	}
	value := &Catalog{}
	if decode(data, &value.release) != nil || value.release.SchemaVersion != 1 || len(value.release.Files) == 0 || len(value.release.Files) > 100 {
		return nil, failure("ENGINEERING_BUNDLE_INVALID", "内置工程包清单无效")
	}
	if err := value.verifyRelease(); err != nil {
		return nil, err
	}
	data, err = assets.ReadFile(bundleRoot + "/_template/.twine/profile.json")
	if err != nil || digest(data) != value.release.ProfileDigest || decode(data, &value.profile) != nil {
		return nil, failure("ENGINEERING_BUNDLE_INVALID", "内置工程方案身份不一致")
	}
	if value.profile.SchemaVersion != 1 || value.profile.ID != value.release.ID || value.profile.Version != value.release.Version {
		return nil, failure("ENGINEERING_BUNDLE_INVALID", "内置工程方案版本不一致")
	}
	return value, nil
}

// verifyRelease 不信任目录项类型、路径或大小，发布源与导出目标均不可重复。
func (catalog *Catalog) verifyRelease() error {
	seen := map[string]bool{}
	for _, file := range catalog.release.Files {
		if !fs.ValidPath(file.Path) || !fs.ValidPath(file.Source) || seen[file.Path] || file.Size < 0 || file.Size > 65536 {
			return failure("ENGINEERING_BUNDLE_INVALID", "内置工程包文件边界无效")
		}
		seen[file.Path] = true
		data, err := assets.ReadFile(bundleRoot + "/_template/" + file.Source)
		if err != nil || len(data) != file.Size || digest(data) != file.Digest {
			return failure("ENGINEERING_BUNDLE_INVALID", "内置工程包文件摘要不一致")
		}
	}
	return nil
}

// Profile 返回独立副本，调用者不能改变目录中的已接受内容。
func (catalog *Catalog) Profile() Profile {
	value := catalog.profile
	value.Documents = append([]Document{}, value.Documents...)
	value.Stack.Platforms = append([]string{}, value.Stack.Platforms...)
	value.Stack.Dependencies = append([]Dependency{}, value.Stack.Dependencies...)
	value.Checks = cloneCommands(value.Checks)
	value.Runtime.Commands = cloneCommands(value.Runtime.Commands)
	return value
}

// Identity 返回不含本机位置的固定身份。
func (catalog *Catalog) Identity() Identity {
	return Identity{ID: catalog.profile.ID, Version: catalog.profile.Version, Digest: catalog.release.ProfileDigest}
}

// cloneCommands 防止嵌套 argv 被外部修改；此处不执行其中的命令。
func cloneCommands(values [][]string) [][]string {
	result := make([][]string, len(values))
	for index, value := range values {
		result[index] = append([]string{}, value...)
	}
	return result
}

// digest 采用原始字节身份，不用自然语言摘要替代文件核实。
func digest(data []byte) string {
	value := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(value[:])
}

// decode 拒绝未知字段、尾随材料与伪造的第二个对象。
func decode(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return expectEnd(decoder)
}

// expectEnd 保证机器清单没有被追加独立指令或其他 JSON。
func expectEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return failure("ENGINEERING_BUNDLE_INVALID", "工程清单包含尾随材料")
	}
	return nil
}
