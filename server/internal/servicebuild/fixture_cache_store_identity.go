//go:build integration

// 本文件定义集成夹具缓存内容身份，随机项目和commit只作来源追溯。
package servicebuild

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"reflect"
	"strings"

	"github.com/hailingu/twine-realm/server/internal/engineering"
	"github.com/hailingu/twine-realm/server/internal/project"
)

// fixtureImplementation 将真实测试程序、固定命令、环境和镜像模板绑定到缓存版本。
func fixtureImplementation() (identity string, err error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(file, 512*1024*1024+1))
	if err != nil {
		return "", err
	}
	if size == 0 || size > 512*1024*1024 {
		return "", fmt.Errorf("夹具缓存实现程序大小无效")
	}
	return Fingerprint(struct {
		Version     int
		Program     string
		Commands    [][]string
		Environment map[string]string
		Dockerfile  string
	}{fixtureCacheVersion, hex.EncodeToString(hash.Sum(nil)), [][]string{{"go", "vet", "./..."}, {"go", "test", "-race", "-count=1", "./..."}, {"CGO_ENABLED=0", "go", "build", "-trimpath", "-buildvcs=false", "-o", "/output/service", "./cmd/service"}}, sandboxEnvironment(), fixedDockerfile})
}

// key 绑定全部文件内容和模式、依赖清单、固定工程包、环境、策略及安装身份。
func (cache *FixtureCache) key(target Target, material project.SourceMaterial) (string, error) {
	if err := validateFixtureMaterial(target, material); err != nil {
		return "", err
	}
	catalog, err := engineering.Load()
	if err != nil {
		return "", err
	}
	digest, err := Fingerprint(struct {
		Version        int
		Implementation string
		SourceDigest   string
		Files          []project.SourceFile
		GitVersion     string
		Profile        engineering.Identity
		Template       engineering.Identity
		Environment    Environment
		Policy         Policy
		InstallationID string
	}{fixtureCacheVersion, cache.implementation, material.Digest, material.Files, material.GitVersion, target.Profile, catalog.Profile().Template, target.Environment, target.Policy, target.InstallationID})
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(digest, "sha256:"), nil
}

// validateFixtureMaterial 重新核实完整源材料摘要，不信任仅由调用方提供的摘要字符串。
func validateFixtureMaterial(target Target, material project.SourceMaterial) error {
	if !project.ValidID(target.ID) || !project.ValidID(target.InstallationID) || target.Source.PinnedCommit != material.Commit || material.GitVersion == "" || len(material.GitVersion) > 128 || len(material.Files) == 0 || len(material.Files) > project.SourceExportFiles {
		return fmt.Errorf("夹具缓存固定来源无效")
	}
	previous, total := "", int64(0)
	for _, file := range material.Files {
		if !fs.ValidPath(file.Path) || strings.ContainsAny(file.Path, "\\\x00\r\n") || file.Path <= previous || file.Size < 0 || file.Size > project.SourceExportBytes || !digestPattern.MatchString(file.Digest) {
			return fmt.Errorf("夹具缓存源码清单无效")
		}
		previous, total = file.Path, total+file.Size
	}
	if total > project.SourceExportBytes {
		return fmt.Errorf("夹具缓存源码材料超限")
	}
	digest, err := Fingerprint(material.Files)
	if err != nil {
		return err
	}
	if digest != material.Digest {
		return fmt.Errorf("夹具缓存源码摘要不一致")
	}
	input, err := InputFingerprint(target.Source, target.Profile, target.Environment, target.Policy, target.InstallationID)
	if err != nil {
		return err
	}
	if input != target.InputDigest {
		return fmt.Errorf("夹具缓存目标输入摘要不一致")
	}
	return nil
}

// validateFixtureOrigin 仅允许完整成功来源，保留三项复用检查的实际时间与输出摘要。
func validateFixtureOrigin(target Target, material project.SourceMaterial, candidate *Candidate) error {
	if candidate == nil || !project.ValidID(candidate.OperationID) || candidate.SourceDigest != material.Digest || !reflect.DeepEqual(candidate.Migrations, collectMigrations(material)) {
		return fmt.Errorf("夹具缓存来源与源码材料不一致")
	}
	return ValidateCompletion(Lease{Target: target, Operation: Operation{ID: candidate.OperationID}}, Completion{State: "succeeded", Code: "BUILT", Message: "夹具缓存来源核实", Candidate: candidate})
}

// validFixtureKey 只接受固定长度SHA256十六进制文件名，不允许路径片段。
func validFixtureKey(key string) bool {
	return len(key) == 64 && digestPattern.MatchString("sha256:"+key)
}
