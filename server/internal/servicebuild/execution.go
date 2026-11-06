// 本文件拥有单次构建的有界执行，任何未持久确认的检查点都不能继续产生外部副作用。
package servicebuild

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/hailingu/twine-realm/server/internal/engineering"
	"github.com/hailingu/twine-realm/server/internal/localdocker"
	"github.com/hailingu/twine-realm/server/internal/project"
)

// SourceExporter 只导出固定Git对象，不允许执行仓库脚本或改变原工作区。
type SourceExporter interface {
	ExportCommit(context.Context, string, string, string) (project.SourceMaterial, error)
}

// Reporter 的false表示代次失效或取消；执行器必须立即收回本次沙箱。
type Reporter func(context.Context, Step) (bool, error)

// execution 将固定输入、检查证据与清理所有权归于同一次执行。
type execution struct {
	driver *Driver
	box    sandbox
	report Reporter
	checks []Check
}

// InputFingerprint 只绑定影响实际构建的材料，布局revision改变不使相同源码证据失效。
func InputFingerprint(source Source, profile engineering.Identity, environment Environment, policy Policy, installationID string) (string, error) {
	source.ProjectRevision = ""
	return Fingerprint(struct {
		Source         Source
		Profile        engineering.Identity
		Environment    Environment
		Policy         Policy
		InstallationID string
	}{source, profile, environment, policy, installationID})
}

// Execute 不盲重用旧目录或在重启后重跑任意代码；失败材料保留以便核实。
func (driver *Driver) Execute(ctx context.Context, target Target, operationID string, exporter SourceExporter, report Reporter) (candidate *Candidate, err error) {
	if err := driver.validateTarget(target, operationID); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(target.Policy.TotalSeconds)*time.Second)
	defer cancel()
	lock, err := driver.files.Lock("locks/" + operationID + ".lock")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, lock()) }()
	work := &execution{driver: driver, box: driver.newSandbox(target, operationID), report: report, checks: []Check{}}
	defer func() {
		settling, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		cleanup := driver.cleanupSandbox(settling, work.box)
		if cleanup != nil {
			candidate = nil
			err = errors.Join(&Fault{Code: "BUILD_CLEANUP_UNCONFIRMED", Message: "原构建沙箱或输出卷尚未确认收回，保留材料等待核实", Unknown: true}, err, cleanup)
		} else if ctx.Err() != nil {
			candidate = nil
			err = errors.Join(failure("BUILD_EXECUTION_STOPPED", "原构建执行已中断且沙箱确认收回，未发布候选"), err)
		}
	}()
	if err := work.step(ctx, "preflight", func() ([]byte, error) { return nil, driver.Preflight(ctx) }); err != nil {
		return nil, err
	}
	material, validation, err := work.prepare(ctx, exporter)
	if err != nil {
		return nil, err
	}
	id, err := work.start(ctx)
	if err != nil {
		return nil, err
	}
	if err := work.check(ctx, id, material); err != nil {
		return nil, err
	}
	var image Image
	if err := work.step(ctx, "image", func() ([]byte, error) {
		var e error
		image, e = driver.buildImage(ctx, work.box, id)
		return []byte(image.ExportDigest), e
	}); err != nil {
		return nil, err
	}
	id, err = project.NewID()
	if err != nil {
		return nil, err
	}
	migrations := collectMigrations(material)
	migrationDigest, err := Fingerprint(migrations)
	if err != nil {
		return nil, err
	}
	candidate = &Candidate{ID: id, TargetID: target.ID, OperationID: operationID, InputDigest: target.InputDigest, SourceDigest: material.Digest, Source: target.Source, Profile: validation.Profile, Template: validation.Template, Environment: target.Environment, Policy: target.Policy, Checks: work.checks, Migrations: migrations, MigrationDigest: migrationDigest, Image: image, CreatedAt: Timestamp(time.Now())}
	if err := ValidateCompletion(Lease{Target: target, Operation: Operation{ID: operationID}}, Completion{State: "succeeded", Code: "BUILT", Message: "候选完整性核实", Candidate: candidate}); err != nil {
		return nil, failure("BUILD_PROOF_INVALID", "候选检查证明不完整或超过受控持久载荷上限")
	}
	if err := work.point(ctx, "complete", "succeeded", ""); err != nil {
		return nil, err
	}
	return candidate, nil
}

// validateTarget 再次约束私有或数据库载荷，不能靠请求字段放宽执行政策。
func (driver *Driver) validateTarget(value Target, operationID string) error {
	if !project.ValidID(value.ID) || !project.ValidID(operationID) || !project.ValidID(value.ProjectID) || !project.ValidID(value.ServiceID) || value.InstallationID != driver.InstallationID() || value.Environment != driver.Environment() || value.Policy != DefaultPolicy() || value.Source.ProjectID != value.ProjectID || value.Source.ServiceID != value.ServiceID {
		return failure("BUILD_TARGET_INVALID", "构建目标的固定身份或执行策略无效")
	}
	digest, err := InputFingerprint(value.Source, value.Profile, value.Environment, value.Policy, value.InstallationID)
	if err != nil {
		return err
	}
	if digest != value.InputDigest {
		return failure("BUILD_TARGET_INVALID", "构建输入摘要与固定材料不一致")
	}
	return nil
}

// prepare 导出源对象并核实工程材料，不运行其中声明的任意命令。
func (work *execution) prepare(ctx context.Context, exporter SourceExporter) (material project.SourceMaterial, validation engineering.Validation, err error) {
	name := "operations/" + work.box.operationID
	if err := work.driver.files.Mkdir(name); err != nil {
		return material, validation, err
	}
	data, err := json.Marshal(work.box.target)
	if err != nil {
		return material, validation, err
	}
	if err := work.driver.files.WriteNew(name+"/target.json", data); err != nil {
		return material, validation, failure("BUILD_MATERIAL_EXISTS", "原构建材料已存在，须核实原操作而不能重跑")
	}
	err = work.step(ctx, "source", func() ([]byte, error) {
		var exportErr error
		material, exportErr = exporter.ExportCommit(ctx, work.box.target.Source.RepositoryPath, work.box.target.Source.PinnedCommit, filepath.Join(work.box.directory, "source"))
		return []byte(material.Digest), exportErr
	})
	if err != nil {
		return material, validation, err
	}
	work.checks[len(work.checks)-1].ToolVersion = material.GitVersion
	err = work.step(ctx, "documents", func() ([]byte, error) {
		catalog, e := engineering.Load()
		if e != nil {
			return nil, e
		}
		if catalog.Identity() != work.box.target.Profile {
			return nil, failure("BUILD_PROFILE_CHANGED", "工程包已改变，不能重用旧检查身份")
		}
		validation, e = catalog.Validate(filepath.Join(work.box.directory, "source"))
		if e != nil {
			return nil, e
		}
		if len(collectMigrations(material)) == 0 {
			return nil, failure("BUILD_MIGRATIONS_MISSING", "服务缺少第一组合要求的固定SQL迁移材料")
		}
		return json.Marshal(validation)
	})
	return material, validation, err
}

// step 在副作用两侧持久检查点，结果未确认时绝不发布成功候选。
func (work *execution) step(ctx context.Context, name string, action func() ([]byte, error)) error {
	if err := work.point(ctx, name, "executing", ""); err != nil {
		return err
	}
	output, err := action()
	if err != nil {
		var command *localdocker.Fault
		if name != "preflight" && errors.As(err, &command) && len(command.Output) > 0 {
			return errors.Join(err, work.driver.files.WriteNew("operations/"+work.box.operationID+"/"+name+".error.log", command.Output))
		}
		return err
	}
	digest := ContentDigest(output)
	work.checks = append(work.checks, Check{Name: name, State: "succeeded", ToolVersion: work.toolVersion(name), OutputDigest: digest, CompletedAt: Timestamp(time.Now())})
	return work.point(ctx, name, "succeeded", "")
}

// toolVersion 如实绑定执行该检查的工具，不将Docker或规范核实误记为Go版本。
func (work *execution) toolVersion(name string) string {
	switch name {
	case "image":
		return work.box.target.Environment.BuildxVersion + " / " + work.box.target.Environment.BuildkitVersion
	case "preflight", "sandbox", "offline":
		return "docker " + work.box.target.Environment.DockerVersion
	case "documents":
		return work.box.target.Profile.ID + "/" + work.box.target.Profile.Version
	case "source":
		return "git-object-export-v1"
	default:
		return "go1.27.3"
	}
}

// point 在取消、围栏或持久失败时停止，网页断开本身不会调用取消。
func (work *execution) point(ctx context.Context, name, state, code string) error {
	valid, err := work.report(ctx, Step{Name: name, State: state, Code: code, UpdatedAt: Timestamp(time.Now())})
	if err != nil {
		return err
	}
	if !valid {
		return failure("BUILD_EXECUTION_FENCED", "构建已取消或执行代次失效，原操作仍可查询")
	}
	return nil
}

// collectMigrations 记录固定源码中的真实SQL字节身份，不将其当作已执行迁移。
func collectMigrations(material project.SourceMaterial) []Migration {
	values := []Migration{}
	for _, file := range material.Files {
		if strings.HasPrefix(file.Path, "internal/schema/migrations/") && strings.HasSuffix(file.Path, ".sql") {
			values = append(values, Migration{file.Path, file.Digest})
		}
	}
	return values
}
