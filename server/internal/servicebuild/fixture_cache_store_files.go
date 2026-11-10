//go:build integration

// 本文件提供缓存私有文件核实和完整目录原子发布；失败材料留待本轮清理。
package servicebuild

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// fixturePrivateInfo 限定当前uid、精确权限、无特殊类型且无多重硬链接。
func fixturePrivateInfo(info fs.FileInfo, mode fs.FileMode, directory bool) bool {
	if info == nil || info.Mode().Perm() != mode || info.Mode()&(os.ModeSymlink|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Getuid() {
		return false
	}
	if directory {
		return info.IsDir()
	}
	return info.Mode().IsRegular() && stat.Nlink == 1
}

// read 仅打开O_RDONLY句柄，不修复权限或修改被读取的只读二进制。
func (cache *FixtureCache) read(name string, mode fs.FileMode, limit int64) (data []byte, err error) {
	if err := cache.files.CheckParents(name); err != nil {
		return nil, err
	}
	file, err := cache.root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	return readFixtureFile(file, mode, limit)
}

// readFixtureFile 从已打开描述符验证类型、大小和实际读取长度，避免阻塞设备和变长文件。
func readFixtureFile(file *os.File, mode fs.FileMode, limit int64) ([]byte, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > DefaultPolicy().BinaryBytes || !fixturePrivateInfo(info, mode, false) || info.Size() <= 0 || info.Size() > limit {
		return nil, fmt.Errorf("夹具缓存文件权限、类型或大小无效")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != info.Size() || after.Size() != info.Size() || after.ModTime() != info.ModTime() || !fixturePrivateInfo(after, mode, false) {
		return nil, fmt.Errorf("夹具缓存文件读取期间发生变化")
	}
	return data, nil
}

// save 必须在真实冷构建、镜像完成且沙箱清理成功后调用；新目录整体原子发布且永不覆盖。
func (cache *FixtureCache) save(key string, target Target, material project.SourceMaterial, candidate *Candidate, binaryPath string) error {
	if err := cache.checkLocked(key); err != nil {
		return err
	}
	if err := cache.matchKey(key, target, material); err != nil {
		return err
	}
	if err := validateFixtureOrigin(target, material, candidate); err != nil {
		return err
	}
	existing, err := cache.load(key, target, material)
	if err != nil {
		return err
	}
	if existing != nil {
		return fmt.Errorf("夹具缓存条目已发布，禁止覆盖")
	}
	binary, err := readFixtureOriginBinary(binaryPath, target.Policy.BinaryBytes)
	if err != nil {
		return err
	}
	if ContentDigest(binary) != candidate.Image.BinaryDigest {
		return fmt.Errorf("夹具缓存来源二进制摘要不一致")
	}
	manifest := fixtureManifest{fixtureCacheVersion, cache.runID, key, cache.implementation, target, material, *candidate}
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if len(data) > fixtureManifestBytes {
		return fmt.Errorf("夹具缓存清单超限")
	}
	return cache.publish(key, data, binary)
}

// readFixtureOriginBinary 只读取当前冷构建冻结的0600普通文件，不跟随父目录或最终链接。
func readFixtureOriginBinary(path string, limit int64) (data []byte, err error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	if canonical != absolute {
		return nil, fmt.Errorf("夹具缓存来源二进制路径含链接")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	return readFixtureFile(file, 0600, limit)
}

// publish 未完成目录永不作为命中；失败不清除证据，下一次读取仍失败关闭。
func (cache *FixtureCache) publish(key string, manifest, binary []byte) error {
	pending := key + ".pending"
	if err := cache.root.Mkdir(pending, 0700); err != nil {
		return err
	}
	if err := cache.writeNew(pending+"/service", binary, 0400); err != nil {
		return err
	}
	if err := cache.writeNew(pending+"/manifest.json", manifest, 0600); err != nil {
		return err
	}
	if err := cache.syncDirectory(pending); err != nil {
		return err
	}
	if err := cache.checkLocked(key); err != nil {
		return err
	}
	if err := cache.root.Rename(pending, key); err != nil {
		return err
	}
	return cache.syncDirectory(".")
}

// writeNew 独占写入并同步内容后收紧权限，不向半成品开放读取。
func (cache *FixtureCache) writeNew(name string, data []byte, mode fs.FileMode) (err error) {
	if err := cache.files.CheckParents(name); err != nil {
		return err
	}
	file, err := cache.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Chmod(mode); err != nil {
		return err
	}
	return file.Sync()
}

// syncDirectory 将目录项提交作为发布成功屏障，失败仍明确上报。
func (cache *FixtureCache) syncDirectory(name string) (err error) {
	file, err := cache.root.Open(name)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	return file.Sync()
}

// checkEntry 拒绝条目内额外文件，不允许外部状态或秘密混入固定二文件清单。
func (cache *FixtureCache) checkEntry(key string) (err error) {
	file, err := cache.root.OpenFile(key, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	names, err := file.Readdirnames(3)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(names) != 2 || !(names[0] == "service" && names[1] == "manifest.json" || names[1] == "service" && names[0] == "manifest.json") {
		return fmt.Errorf("夹具缓存条目不属于固定二文件清单")
	}
	return nil
}
