//go:build integration

// 本文件只为同轮固定集成夹具保存完整来源证明，不进入生产构建路径。
package servicebuild

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"

	"github.com/hailingu/twine-realm/server/internal/privatefiles"
	"github.com/hailingu/twine-realm/server/internal/project"
)

const fixtureCacheVersion = 1
const fixtureManifestBytes = 2 * 1024 * 1024

// FixtureCache 拥有单轮私有缓存句柄；调用方在Acquire锁内完成读取、冷构建清理和发布。
// 同轮缓存只保存二进制及来源证明，不保存数据库、容器或秘密状态。
type FixtureCache struct {
	files          *privatefiles.Root
	root           *os.Root
	runID          string
	implementation string
	mu             sync.Mutex
	held           map[string]*os.File
}

// fixtureRunMarker 由runner在全新0700根中独占创建，Open绝不初始化或修复它。
type fixtureRunMarker struct {
	SchemaVersion int    `json:"schemaVersion"`
	RunID         string `json:"runId"`
}

// fixtureProof 保留原始检查及完成时间，调用方不得将复用检查伪装为本次新执行。
type fixtureProof struct {
	Origin Candidate
	Binary []byte
}

// fixtureManifest 将原始目标、源码、候选与内容键一起绑定到同一轮执行。
type fixtureManifest struct {
	SchemaVersion  int                    `json:"schemaVersion"`
	RunID          string                 `json:"runId"`
	Key            string                 `json:"key"`
	Implementation string                 `json:"implementation"`
	Target         Target                 `json:"target"`
	Material       project.SourceMaterial `json:"material"`
	Origin         Candidate              `json:"origin"`
}

// OpenFixtureCache 只打开runner已建的私有根及0600 run.json；错误保留现场且禁止跨轮读取。
func OpenFixtureCache(directory, runID string) (cache *FixtureCache, err error) {
	if !project.ValidID(runID) {
		return nil, fmt.Errorf("夹具缓存轮次身份无效")
	}
	files, err := privatefiles.Open(directory, nil)
	if err != nil {
		return nil, err
	}
	cache = &FixtureCache{files: files, runID: runID, held: map[string]*os.File{}}
	opened := cache
	defer func() {
		if err != nil {
			err = errors.Join(err, opened.Close())
		}
	}()
	cache.root, err = os.OpenRoot(files.Path())
	if err != nil {
		return nil, err
	}
	if err = cache.checkRun(); err != nil {
		return nil, err
	}
	cache.implementation, err = fixtureImplementation()
	if err != nil {
		return nil, err
	}
	return cache, nil
}

// Close 只释放句柄，不删除缓存；所有Acquire返回的释放函数须先执行。
func (cache *FixtureCache) Close() error {
	var err error
	if cache.root != nil {
		err = cache.root.Close()
	}
	return errors.Join(err, cache.files.Close())
}

// checkRun 每次磁盘动作重新核实根、轮次标记，绝不静默恢复被替换材料。
func (cache *FixtureCache) checkRun() error {
	opened, err := cache.root.Stat(".")
	if err != nil {
		return err
	}
	current, err := os.Lstat(cache.files.Path())
	if err != nil || !fixturePrivateInfo(opened, 0700, true) || !fixturePrivateInfo(current, 0700, true) || !os.SameFile(opened, current) {
		return fmt.Errorf("夹具缓存根身份或权限已改变")
	}
	if err := cache.files.CheckParents("run.json"); err != nil {
		return err
	}
	data, err := cache.read("run.json", 0600, 4096)
	if err != nil {
		return err
	}
	var marker fixtureRunMarker
	if err := decode(data, &marker); err != nil {
		return err
	}
	if marker.SchemaVersion != fixtureCacheVersion || marker.RunID != cache.runID {
		return fmt.Errorf("夹具缓存不属于当前轮次")
	}
	return nil
}

// load 只有完整条目确实不存在才返回nil；未完成发布或损坏始终是错误。
func (cache *FixtureCache) load(key string, target Target, material project.SourceMaterial) (*fixtureProof, error) {
	if err := cache.checkLocked(key); err != nil {
		return nil, err
	}
	if err := cache.matchKey(key, target, material); err != nil {
		return nil, err
	}
	if _, err := cache.root.Lstat(key + ".pending"); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("夹具缓存存在未完成发布材料")
	}
	info, err := cache.root.Lstat(key)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !fixturePrivateInfo(info, 0700, true) {
		return nil, fmt.Errorf("夹具缓存条目目录无效")
	}
	if err := cache.checkEntry(key); err != nil {
		return nil, err
	}
	data, err := cache.read(key+"/manifest.json", 0600, fixtureManifestBytes)
	if err != nil {
		return nil, err
	}
	var manifest fixtureManifest
	if err := decode(data, &manifest); err != nil {
		return nil, err
	}
	if err := cache.validateManifest(key, manifest); err != nil {
		return nil, err
	}
	binary, err := cache.read(key+"/service", 0400, target.Policy.BinaryBytes)
	if err != nil {
		return nil, err
	}
	if ContentDigest(binary) != manifest.Origin.Image.BinaryDigest {
		return nil, fmt.Errorf("夹具缓存二进制摘要不一致")
	}
	if err := cache.checkLocked(key); err != nil {
		return nil, err
	}
	return &fixtureProof{Origin: manifest.Origin, Binary: binary}, nil
}

// validateManifest 校验来源完整成功证明；目标随机身份只保存在来源而不进入内容键。
func (cache *FixtureCache) validateManifest(key string, manifest fixtureManifest) error {
	if manifest.SchemaVersion != fixtureCacheVersion || manifest.RunID != cache.runID || manifest.Key != key || manifest.Implementation != cache.implementation {
		return fmt.Errorf("夹具缓存清单身份不一致")
	}
	if err := cache.matchKey(key, manifest.Target, manifest.Material); err != nil {
		return err
	}
	return validateFixtureOrigin(manifest.Target, manifest.Material, &manifest.Origin)
}

// matchKey 防止调用方用旧键读取已经改变的内容或执行策略。
func (cache *FixtureCache) matchKey(key string, target Target, material project.SourceMaterial) error {
	actual, err := cache.key(target, material)
	if err != nil {
		return err
	}
	if actual != key {
		return fmt.Errorf("夹具缓存内容键与固定材料不一致")
	}
	return nil
}
