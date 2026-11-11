//go:build integration

// 本文件用真实Git、工具链容器和Buildx证明第一组合，不以测试替身冒充构建成功。
package servicebuild

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/engineering"
	"github.com/hailingu/twine-realm/server/internal/gitworkspace"
	"github.com/hailingu/twine-realm/server/internal/project"
)

// realBuildFixture 建立仅属于本测试的仓库和私有构建根，不读取用户仓库或环境秘密。
type realBuildFixture struct {
	driver                  *Driver
	inspector               *gitworkspace.Inspector
	target                  Target
	operationID, repository string
}

// newRealBuildFixture 复用真实离线工程包导出，固定commit后故意留下脏修改。
func newRealBuildFixture(t *testing.T) realBuildFixture {
	return realBuildFixtureAt(t, t.TempDir())
}

// realBuildFixtureAt 允许缓存真实验收在清理未知时保留其独立安装材料，其余构建步骤与原夹具相同。
func realBuildFixtureAt(t *testing.T, root string) realBuildFixture {
	t.Helper()
	ctx := context.Background()
	environment, err := DiscoverEnvironment(ctx)
	if err != nil {
		t.Fatal(err)
	}
	repositories := filepath.Join(root, "repositories")
	if err := os.Mkdir(repositories, 0700); err != nil {
		t.Fatal(err)
	}
	repository := filepath.Join(repositories, "service")
	catalog, err := engineering.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Export(repository); err != nil {
		t.Fatal(err)
	}
	repository, err = filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatal(err)
	}
	gitBuild(t, repository, "init", "--initial-branch=main")
	gitBuild(t, repository, "-c", "user.name=隔离构建测试", "-c", "user.email=build@example.invalid", "add", ".")
	gitBuild(t, repository, "-c", "user.name=隔离构建测试", "-c", "user.email=build@example.invalid", "commit", "-m", "固定构建材料")
	commit := gitBuild(t, repository, "rev-parse", "HEAD")
	inspector, err := gitworkspace.New([]string{repositories})
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "build")
	if err := InitializeDirectory(directory, []string{repositories}, environment); err != nil {
		t.Fatal(err)
	}
	driver, err := OpenDriver(directory, []string{repositories})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := driver.Close(); err != nil {
			t.Error(err)
		}
	})
	source := Source{ProjectID: buildID(t), ProjectRevision: "1", ServiceID: buildID(t), RepositoryID: buildID(t), RepositoryPath: repository, PinnedCommit: commit}
	target := Target{ID: buildID(t), ProjectID: source.ProjectID, ServiceID: source.ServiceID, Generation: "1", CanvasRevision: "1", InstallationID: driver.InstallationID(), Source: source, Profile: catalog.Identity(), Environment: environment, Policy: DefaultPolicy(), CreatedAt: Timestamp(time.Now())}
	target.InputDigest, err = InputFingerprint(source, target.Profile, environment, target.Policy, target.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
	return realBuildFixture{driver, inspector, target, buildID(t), repository}
}

// gitBuild 执行固定测试命令，不继承外层Git定位或用户hook。
func gitBuild(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null"}, args...)...)
	command.Dir = directory
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0"}
	data, err := command.CombinedOutput()
	if err != nil {
		t.Fatal("测试Git命令失败：", err)
	}
	return strings.TrimSpace(string(data))
}

// buildID 返回真实随机平台身份，不与用户已有资源共用名称。
func buildID(t *testing.T) string {
	t.Helper()
	id, err := project.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestRealFixedCommitBuild 证明脏文件不进入检查，真实检查、断网与镜像内容可追溯。
func TestRealFixedCommitBuild(t *testing.T) {
	f := newRealBuildFixture(t)
	if err := os.WriteFile(filepath.Join(f.repository, "cmd/service/main.go"), []byte("unbuildable dirty source"), 0600); err != nil {
		t.Fatal(err)
	}
	before := gitBuild(t, f.repository, "status", "--porcelain=v1")
	states := []Step{}
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()
	candidate, err := f.driver.Execute(ctx, f.target, f.operationID, f.inspector, func(_ context.Context, step Step) (bool, error) {
		states = append(states, step)
		t.Log("真实检查点", step.Name, step.State)
		return true, nil
	})
	if err != nil {
		logPublicBuildFailure(t, f)
		t.Fatalf("真实构建未通过：%T %v", err, err)
	}
	t.Cleanup(func() { cleanupBuildImage(t, f.driver, f.driver.newSandbox(f.target, f.operationID), candidate.Image) })
	if candidate.Source.PinnedCommit != f.target.Source.PinnedCommit || candidate.InputDigest != f.target.InputDigest || !boundImageID(candidate.Image.ID, candidate.Image) || len(candidate.Migrations) == 0 {
		t.Fatal("候选没有绑定完整实际输入和内容摘要")
	}
	assertBuildChecks(t, states)
	assertActualImageID(t, f.driver, candidate.Image)
	if gitBuild(t, f.repository, "status", "--porcelain=v1") != before {
		t.Fatal("构建改变了原仓库工作区")
	}
	box := f.driver.newSandbox(f.target, f.operationID)
	value, err := f.driver.inspectSandbox(ctx, box)
	if err != nil || value != nil {
		t.Fatal("构建结束后没有清理本次沙箱：", err)
	}
	data, err := json.Marshal(candidate)
	if err != nil || strings.Contains(string(data), "PASSWORD") || reflect.DeepEqual(candidate.Checks, []Check{}) {
		t.Fatal("候选检查证据无效")
	}
}

// assertActualImageID 从真实Docker独立读取候选tag，核对实际ID而非在测试中复写config-ID假设。
func assertActualImageID(t *testing.T, driver *Driver, image Image) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	data, err := driver.run(ctx, "image", "inspect", "--format={{json .Id}}", image.Tag)
	if err != nil {
		t.Fatal(err)
	}
	var actual string
	if json.Unmarshal(data, &actual) != nil || actual != image.ID || !boundImageID(actual, image) {
		t.Fatal("真实引擎ID没有绑定原构建证明")
	}
}

// cleanupBuildImage 仅移除测试拥有的tag；共享镜像内容和其他tag不被删除。
func cleanupBuildImage(t *testing.T, driver *Driver, box sandbox, image Image) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := driver.verifyImage(ctx, box, image); err != nil {
		t.Error("测试镜像归属未确认，保留：", err)
		return
	}
	if _, err := driver.run(ctx, "image", "rm", image.Tag); err != nil {
		t.Error("测试镜像清理未确认：", err)
	}
}

// assertBuildChecks 要求实际成功检查完整，而非只看到最终镜像tag。
func assertBuildChecks(t *testing.T, states []Step) {
	t.Helper()
	seen := map[string]bool{}
	for _, step := range states {
		if step.State == "succeeded" {
			seen[step.Name] = true
		}
	}
	for _, name := range []string{"source", "documents", "dependencies", "offline", "format", "module", "vet", "test", "build", "image"} {
		if !seen[name] {
			t.Fatal("缺少真实检查：", name)
		}
	}
}

// logPublicBuildFailure 仅用于本文件固定公开模板，不读取用户服务日志。
func logPublicBuildFailure(t *testing.T, f realBuildFixture) {
	t.Helper()
	for _, phase := range []string{"test", "image"} {
		if data, readErr := f.driver.files.Read("operations/"+f.operationID+"/"+phase+".error.log", 2*1024*1024); readErr == nil {
			t.Log("固定公开模板测试私有诊断：", phase, string(data))
		}
	}
}
