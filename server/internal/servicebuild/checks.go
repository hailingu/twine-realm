// 本文件定义第一组合真实执行的固定检查；工程文档本身不授予任意命令执行权。
package servicebuild

import (
	"context"
	"strings"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// start 在已确认的沙箱内准备有界临时目录，尚不执行服务源码。
func (work *execution) start(ctx context.Context) (id string, err error) {
	err = work.step(ctx, "sandbox", func() ([]byte, error) {
		var createErr error
		id, createErr = work.driver.createSandbox(ctx, work.box)
		if createErr != nil {
			return nil, createErr
		}
		return work.driver.run(ctx, "exec", id, "/usr/bin/mkdir", "-p", "/tmp/home", "/tmp/gocache", "/tmp/gomod", "/tmp/go", "/tmp/gotmp", "/tmp/out")
	})
	return id, err
}

// check 依赖下载后核实断网，所有业务代码只在断网沙箱内运行。
func (work *execution) check(ctx context.Context, id string, material project.SourceMaterial) error {
	if err := work.checkPrerequisites(ctx, id, material); err != nil {
		return err
	}
	checks := []struct {
		name string
		args []string
	}{{"vet", []string{"vet", "./..."}}, {"test", []string{"test", "-race", "-count=1", "./..."}}}
	for _, check := range checks {
		if err := work.step(ctx, check.name, func() ([]byte, error) { return work.goCommand(ctx, id, check.args...) }); err != nil {
			return err
		}
	}
	return work.step(ctx, "build", func() ([]byte, error) {
		return work.driver.run(ctx, "exec", "--env", "CGO_ENABLED=0", id, "/usr/local/go/bin/go", "build", "-trimpath", "-buildvcs=false", "-o", "/output/service", "./cmd/service")
	})
}

// checkPrerequisites 保持原依赖、断网、工具链、格式及模块检查，执行任何后续构建前均须真实通过。
func (work *execution) checkPrerequisites(ctx context.Context, id string, material project.SourceMaterial) error {
	if err := work.step(ctx, "dependencies", func() ([]byte, error) { return work.goCommand(ctx, id, "mod", "download") }); err != nil {
		return err
	}
	if err := work.step(ctx, "offline", func() ([]byte, error) { return nil, work.driver.offline(ctx, work.box, id) }); err != nil {
		return err
	}
	if err := work.step(ctx, "toolchain", func() ([]byte, error) {
		data, err := work.goCommand(ctx, id, "version")
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(string(data)) != "go version go1.27.3 linux/"+work.box.target.Environment.Architecture {
			return nil, failure("BUILD_GO_VERSION_CHANGED", "实际Go工具链版本与固定输入不一致")
		}
		return data, nil
	}); err != nil {
		return err
	}
	if err := work.step(ctx, "format", func() ([]byte, error) { return work.format(ctx, id, material) }); err != nil {
		return err
	}
	return work.step(ctx, "module", func() ([]byte, error) { return work.goCommand(ctx, id, "mod", "verify") })
}

// goCommand 不使用shell或用户声明argv，失败输出不会写进API。
func (work *execution) goCommand(ctx context.Context, id string, args ...string) ([]byte, error) {
	return work.driver.run(ctx, append([]string{"exec", id, "/usr/local/go/bin/go"}, args...)...)
}

// format 检查固定导出的全部Go文件，gofmt列表非空即失败而不修改源码。
func (work *execution) format(ctx context.Context, id string, material project.SourceMaterial) ([]byte, error) {
	args := []string{"exec", id, "/usr/local/go/bin/gofmt", "-l"}
	for _, file := range material.Files {
		if strings.HasSuffix(file.Path, ".go") {
			args = append(args, "./"+file.Path)
		}
	}
	if len(args) == 4 {
		return nil, failure("BUILD_GO_SOURCE_MISSING", "固定源码中没有可检查Go文件")
	}
	data, err := work.driver.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) != 0 {
		return nil, failure("BUILD_FORMAT_FAILED", "固定源码未通过gofmt零告警检查")
	}
	return data, nil
}
