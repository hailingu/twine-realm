//go:build integration

// 本文件仅供集成夹具复用同轮重检查证明；正常构建入口始终执行全部真实检查。
package servicebuild

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/hailingu/twine-realm/server/internal/engineering"
	"github.com/hailingu/twine-realm/server/internal/project"
)

// Execute 仅为固定测试夹具准备候选，不进入HTTP/worker；命中仍使用本次独立沙箱、镜像及全部归属围栏。
func (cache *FixtureCache) Execute(ctx context.Context, driver *Driver, target Target, operationID string, exporter SourceExporter, report Reporter) (candidate *Candidate, hit bool, err error) {
	if err := driver.validateTarget(target, operationID); err != nil {
		return nil, false, err
	}
	material, err := fixtureSource(ctx, driver, target, exporter)
	if err != nil {
		return nil, false, err
	}
	key, err := cache.key(target, material)
	if err != nil {
		return nil, false, err
	}
	unlock, err := cache.Acquire(ctx, key)
	if err != nil {
		return nil, false, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	proof, err := cache.load(key, target, material)
	if err != nil {
		return nil, false, err
	}
	if proof != nil {
		candidate, err = cache.executeHit(ctx, driver, driver.newSandbox(target, operationID), exporter, report, key, proof)
		return candidate, true, err
	}
	candidate, err = driver.Execute(ctx, target, operationID, exporter, report)
	if err != nil {
		return nil, false, err
	}
	binary := filepath.Join(driver.files.Path(), "operations", operationID, "image", "service")
	if err := cache.save(key, target, material, candidate, binary); err != nil {
		return nil, false, errors.Join(err, driver.discardFixtureCandidate(target, candidate))
	}
	return candidate, false, nil
}

// discardFixtureCandidate 缓存发布失败不能遗留已成功生成的夹具镜像；仅删除完整核实的本次tag。
func (driver *Driver) discardFixtureCandidate(target Target, candidate *Candidate) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	box := driver.newSandbox(target, candidate.OperationID)
	if err := driver.verifyImage(ctx, box, candidate.Image); err != nil {
		return errors.Join(&Fault{Code: "BUILD_CLEANUP_UNCONFIRMED", Message: "夹具缓存发布失败且镜像归属未确认，保留现场", Unknown: true}, err)
	}
	if _, err := driver.run(ctx, "image", "rm", candidate.Image.Tag); err != nil {
		return errors.Join(&Fault{Code: "BUILD_CLEANUP_UNCONFIRMED", Message: "夹具缓存发布失败且镜像清理未确认，保留现场", Unknown: true}, err)
	}
	return nil
}

// fixtureSource 通过原Git对象导出计算内容身份，临时副本不进入用户仓库或后续沙箱。
func fixtureSource(ctx context.Context, driver *Driver, target Target, exporter SourceExporter) (material project.SourceMaterial, err error) {
	if err := driver.files.CheckParents("fixture-probe"); err != nil {
		return material, err
	}
	directory, err := os.MkdirTemp(driver.files.Path(), "fixture-probe-")
	if err != nil {
		return material, err
	}
	defer func() {
		if checkErr := driver.files.CheckParents(filepath.Base(directory) + "/source"); checkErr != nil {
			err = errors.Join(err, checkErr)
			return
		}
		err = errors.Join(err, os.RemoveAll(directory))
	}()
	return exporter.ExportCommit(ctx, target.Source.RepositoryPath, target.Source.PinnedCommit, filepath.Join(directory, "source"))
}

// executeHit 每个命中都新建原规格沙箱并取得当前真实材料；只读证明不能跳过归属、断网或取消。
func (cache *FixtureCache) executeHit(ctx context.Context, driver *Driver, box sandbox, exporter SourceExporter, report Reporter, key string, proof *fixtureProof) (candidate *Candidate, err error) {
	target, operationID := box.target, box.operationID
	ctx, cancel := context.WithTimeout(ctx, time.Duration(target.Policy.TotalSeconds)*time.Second)
	defer cancel()
	unlock, err := driver.files.Lock("locks/" + operationID + ".lock")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	work := &execution{driver: driver, box: box, report: report, checks: []Check{}}
	defer func() {
		settling, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		cleanup := driver.cleanupSandbox(settling, work.box)
		if cleanup != nil {
			candidate = nil
			err = errors.Join(&Fault{Code: "BUILD_CLEANUP_UNCONFIRMED", Message: "夹具原沙箱或输出卷尚未确认收回，保留现场", Unknown: true}, err, cleanup)
		} else if ctx.Err() != nil {
			candidate = nil
			err = errors.Join(failure("BUILD_EXECUTION_STOPPED", "夹具执行已中断，未发布候选"), err)
		}
	}()
	if err := work.step(ctx, "preflight", func() ([]byte, error) { return nil, driver.Preflight(ctx) }); err != nil {
		return nil, err
	}
	material, validation, err := work.prepare(ctx, exporter)
	if err != nil {
		return nil, err
	}
	actual, err := cache.key(target, material)
	if err != nil || actual != key {
		return nil, errors.Join(failure("FIXTURE_SOURCE_CHANGED", "夹具固定材料在缓存核实后改变"), err)
	}
	id, err := work.start(ctx)
	if err != nil {
		return nil, err
	}
	if err := work.checkFixtureMaterial(ctx, id, material, proof.Origin.Checks); err != nil {
		return nil, err
	}
	image, err := work.fixtureImage(ctx, id, proof)
	if err != nil {
		return nil, err
	}
	candidate, err = work.fixtureCandidate(material, validation, image)
	if err != nil {
		return nil, err
	}
	if err := work.point(ctx, "complete", "succeeded", ""); err != nil {
		return nil, err
	}
	return candidate, nil
}

// checkFixtureMaterial 保留五个真实前置检查，仅复制三项原始证明，不重写时间或伪造本次执行检查点。
func (work *execution) checkFixtureMaterial(ctx context.Context, id string, material project.SourceMaterial, origin []Check) error {
	if err := work.checkPrerequisites(ctx, id, material); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, check := range origin {
		if check.Name != "vet" && check.Name != "test" && check.Name != "build" {
			continue
		}
		if seen[check.Name] || check.State != "succeeded" || !digestPattern.MatchString(check.OutputDigest) || check.ToolVersion != "go1.27.3" {
			return failure("FIXTURE_CHECK_INVALID", "夹具原检查证据不完整")
		}
		if _, err := time.Parse(time.RFC3339Nano, check.CompletedAt); err != nil {
			return failure("FIXTURE_CHECK_INVALID", "夹具原检查时间无效")
		}
		seen[check.Name] = true
		work.checks = append(work.checks, check)
	}
	if len(seen) != 3 {
		return failure("FIXTURE_CHECK_INVALID", "夹具缺少完整重检查证明")
	}
	return nil
}

// fixtureImage 仅复制已核实字节到本次输出卷，冻结后再次比较，再调用原镜像打包及归属检查。
func (work *execution) fixtureImage(ctx context.Context, id string, proof *fixtureProof) (value Image, err error) {
	err = work.step(ctx, "image", func() ([]byte, error) {
		name := "operations/" + work.box.operationID + "/fixture-service"
		if ContentDigest(proof.Binary) != proof.Origin.Image.BinaryDigest {
			return nil, failure("FIXTURE_BINARY_CHANGED", "夹具缓存二进制内容改变")
		}
		if err := work.driver.files.WriteNew(name, proof.Binary); err != nil {
			return nil, err
		}
		if _, err := work.driver.run(ctx, "container", "cp", filepath.Join(work.driver.files.Path(), name), id+":/output/service"); err != nil {
			return nil, err
		}
		frozen, err := work.driver.frozenBinary(ctx, work.box, id)
		if err != nil {
			return nil, err
		}
		if frozen.BinaryDigest != proof.Origin.Image.BinaryDigest {
			return nil, failure("FIXTURE_BINARY_CHANGED", "夹具输出在复制后改变，不能生成镜像")
		}
		value, err = work.driver.buildFrozenImage(ctx, work.box, frozen)
		return []byte(value.ExportDigest), err
	})
	return value, err
}

// fixtureCandidate 仅绑定当前真实材料和新镜像，原生产完成校验不因夹具缓存放宽。
func (work *execution) fixtureCandidate(material project.SourceMaterial, validation engineering.Validation, image Image) (*Candidate, error) {
	id, err := project.NewID()
	if err != nil {
		return nil, err
	}
	migrations := collectMigrations(material)
	digest, err := Fingerprint(migrations)
	if err != nil {
		return nil, err
	}
	target := work.box.target
	candidate := &Candidate{ID: id, TargetID: target.ID, OperationID: work.box.operationID, InputDigest: target.InputDigest, SourceDigest: material.Digest, Source: target.Source, Profile: validation.Profile, Template: validation.Template, Environment: target.Environment, Policy: target.Policy, Checks: work.checks, Migrations: migrations, MigrationDigest: digest, Image: image, CreatedAt: Timestamp(time.Now())}
	if err := ValidateCompletion(Lease{Target: target, Operation: Operation{ID: work.box.operationID}}, Completion{State: "succeeded", Code: "CHECKED", Message: "同轮夹具证明及当前镜像已核实", Candidate: candidate}); err != nil {
		return nil, err
	}
	return candidate, nil
}
