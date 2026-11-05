// Package schema 固定当前源码的业务迁移摘要，独立于平台元数据库迁移。
package schema

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"

	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var files embed.FS

// Set 是编译进当前服务 commit 的迁移版本与完整文件字节摘要。
type Set struct {
	Latest  uint
	Digests map[uint]string
}

// Load 限定迁移数量与大小，不解析仓库之外的任意文件。
func Load() (Set, error) {
	names, err := fs.Glob(files, "migrations/*.up.sql")
	if err != nil || len(names) == 0 || len(names) > 64 {
		return Set{}, fmt.Errorf("当前源码迁移清单无效")
	}
	value := Set{Digests: map[uint]string{}}
	for _, name := range names {
		prefix, _, found := strings.Cut(path.Base(name), "_")
		version, err := strconv.ParseUint(prefix, 10, 32)
		if err != nil || !found || version == 0 || value.Digests[uint(version)] != "" {
			return Set{}, fmt.Errorf("当前源码迁移版本无效或重复")
		}
		data, err := files.ReadFile(name)
		if err != nil || len(data) > 65536 {
			return Set{}, fmt.Errorf("当前源码迁移文件无效或过大")
		}
		digest := sha256.Sum256(data)
		value.Digests[uint(version)] = hex.EncodeToString(digest[:])
		value.Latest = max(value.Latest, uint(version))
	}
	return value, nil
}

// Source 只提供当前二进制内嵌迁移，调用者负责关闭。
func Source() (source.Driver, error) { return iofs.New(files, "migrations") }
