//go:build integration

// 本文件用真实 Git、HTTP 与 MySQL 验证固定源码的原子回执和恢复。
package httpapi_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// sourceGit 仅用固定参数准备测试拥有的仓库，不继承提交 hook 的 Git 定位。
func sourceGit(t *testing.T, path string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", path}, arguments...)...)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("源码夹具准备失败: %v %s", err, output)
	}
	return strings.TrimSpace(string(output))
}

// registerSource 保留登记回执及源码路径，提交动作只属于测试夹具。
func registerSource(t *testing.T, f *fixture, committed bool) (project.Receipt, string) {
	t.Helper()
	current := create(t, f)
	path := repository(t, f.root, "source")
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	if committed {
		sourceGit(t, path, "-c", "user.name=测试", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "初始源码")
	}
	command := project.RegisterCommand{MutationID: id(t), BaseRevision: current.Revision, RepositoryPath: path, Position: project.Position{X: 20, Y: 30}}
	value := decode[project.Receipt](t, call(t, f.handler, "POST", "/api/v1/projects/"+current.ID+"/services", command), 201, "Receipt")
	return value, path
}

// sourceURL 使用已确认实体身份定位源码元数据，不以路径或名称代替 ID。
func sourceURL(value project.Project) string {
	return "/api/v1/projects/" + value.ID + "/services/" + value.Nodes[0].ID + "/source"
}

// TestPinnedSourceSurvivesChanges 验证保存、后续 HEAD、游离、未提交修改和平台重开。
func TestPinnedSourceSurvivesChanges(t *testing.T) {
	f := openFixture(t)
	registered, path := registerSource(t, f, true)
	url := sourceURL(registered.Project)
	before := decode[project.SourceObservation](t, call(t, f.handler, "GET", url, nil), 200, "SourceObservation")
	if before.Version.PinnedCommit != nil || before.Git.HeadCommit == nil || before.Git.Branch == nil || before.Git.Dirty == nil || *before.Git.Dirty {
		t.Fatalf("登记快照被错误固定: %+v", before)
	}
	command := project.PinSourceCommand{MutationID: id(t), BaseRevision: registered.Project.Revision, Commit: *before.Git.HeadCommit}
	saved := decode[project.Receipt](t, call(t, f.handler, "PUT", url, command), 200, "SourceReceipt")
	if saved.SourceVersion == nil || saved.SourceVersion.PinnedCommit == nil || *saved.SourceVersion.PinnedCommit != command.Commit || saved.Project.Revision != "2" || saved.SourceVersion.ProjectRevision != "2" {
		t.Fatalf("未原子固定源码: %+v", saved)
	}
	if !reflect.DeepEqual(saved.Project.Nodes, registered.Project.Nodes) {
		t.Fatal("固定改写了登记快照或布局")
	}
	sourceGit(t, path, "-c", "user.name=测试", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "后续源码")
	sourceGit(t, path, "checkout", "--detach")
	if err := os.WriteFile(filepath.Join(path, "draft.txt"), []byte("不属于固定内容"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := f.database.Close(); err != nil {
		t.Fatal(err)
	}
	f.reopen(t)
	after := decode[project.SourceObservation](t, call(t, f.handler, "GET", url, nil), 200, "SourceObservation")
	if !reflect.DeepEqual(saved.SourceVersion, &after.Version) || after.Git.HeadCommit == nil || *after.Git.HeadCommit == command.Commit || after.Git.Branch != nil || after.Git.Dirty == nil || !*after.Git.Dirty {
		t.Fatalf("重开后固定漂移或观察失实: %+v", after)
	}
	replay := decode[project.Receipt](t, call(t, f.handler, "GET", "/api/v1/source-mutations/"+command.MutationID, nil), 200, "SourceReceipt")
	if !replay.Replayed || !reflect.DeepEqual(replay.SourceVersion, saved.SourceVersion) {
		t.Fatal("重开后丢失原源码回执")
	}
	decode[any](t, call(t, f.handler, "GET", "/api/v1/mutations/"+command.MutationID, nil), 400, "ErrorResponse")
	decode[project.Receipt](t, call(t, f.handler, "GET", "/api/v1/mutations/"+registered.MutationID, nil), 200, "Receipt")
}

// TestSourceFailurePreservesPin 核实仓库失联不删除记录，原键回放先于仓库访问。
func TestSourceFailurePreservesPin(t *testing.T) {
	f := openFixture(t)
	registered, path := registerSource(t, f, true)
	url := sourceURL(registered.Project)
	commit := sourceGit(t, path, "rev-parse", "HEAD")
	command := project.PinSourceCommand{MutationID: id(t), BaseRevision: "1", Commit: commit}
	saved := decode[project.Receipt](t, call(t, f.handler, "PUT", url, command), 200, "SourceReceipt")
	if err := os.Rename(path, path+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	observation := decode[project.SourceObservation](t, call(t, f.handler, "GET", url, nil), 200, "SourceObservation")
	if observation.Git.Available || observation.Git.Problem == nil || !reflect.DeepEqual(&observation.Version, saved.SourceVersion) {
		t.Fatal("仓库失联抹除了已固定记录或伪报可用")
	}
	replay := decode[project.Receipt](t, call(t, f.handler, "PUT", url, command), 200, "SourceReceipt")
	if !replay.Replayed || !reflect.DeepEqual(replay.SourceVersion, saved.SourceVersion) {
		t.Fatal("原键恢复错误依赖仓库仍在线")
	}
	command.Commit = strings.Repeat("f", 40)
	decode[any](t, call(t, f.handler, "PUT", url, command), 409, "ErrorResponse")
	command.MutationID, command.BaseRevision = id(t), "2"
	decode[any](t, call(t, f.handler, "PUT", url, command), 400, "ErrorResponse")
	decode[any](t, call(t, f.handler, "GET", "/api/v1/source-mutations/"+command.MutationID, nil), 404, "ErrorResponse")
}

// TestRejectedSourceKeepsPriorSelection 对象校验、错误归属和版本冲突均不能推进固定值。
func TestRejectedSourceKeepsPriorSelection(t *testing.T) {
	f := openFixture(t)
	registered, path := registerSource(t, f, true)
	url := sourceURL(registered.Project)
	head := sourceGit(t, path, "rev-parse", "HEAD")
	first := project.PinSourceCommand{MutationID: id(t), BaseRevision: "1", Commit: head}
	decode[project.Receipt](t, call(t, f.handler, "PUT", url, first), 200, "SourceReceipt")
	for _, commit := range []string{strings.Repeat("f", 40), sourceGit(t, path, "rev-parse", "HEAD^{tree}"), "HEAD", "deadbee"} {
		command := project.PinSourceCommand{MutationID: id(t), BaseRevision: "2", Commit: commit}
		decode[any](t, call(t, f.handler, "PUT", url, command), 400, "ErrorResponse")
		decode[any](t, call(t, f.handler, "GET", "/api/v1/source-mutations/"+command.MutationID, nil), 404, "ErrorResponse")
	}
	first.MutationID = id(t)
	decode[any](t, call(t, f.handler, "PUT", url, first), 409, "ErrorResponse")
	decode[any](t, call(t, f.handler, "GET", "/api/v1/projects/"+id(t)+"/services/"+registered.Project.Nodes[0].ID+"/source", nil), 404, "ErrorResponse")
	after := decode[project.SourceObservation](t, call(t, f.handler, "GET", url, nil), 200, "SourceObservation")
	if after.Version.ProjectRevision != "2" || after.Version.PinnedCommit == nil || *after.Version.PinnedCommit != head {
		t.Fatal("拒绝请求产生了部分变更")
	}
}

// TestConcurrentSourcePins 同键并发只提交一次，独立竞争意图必须显式冲突。
func TestConcurrentSourcePins(t *testing.T) {
	f := openFixture(t)
	registered, path := registerSource(t, f, true)
	url := sourceURL(registered.Project)
	command := project.PinSourceCommand{MutationID: id(t), BaseRevision: "1", Commit: sourceGit(t, path, "rev-parse", "HEAD")}
	var wait sync.WaitGroup
	codes := make(chan int, 2)
	for range 2 {
		wait.Go(func() { codes <- call(t, f.handler, "PUT", url, command).Code })
	}
	wait.Wait()
	for range 2 {
		if code := <-codes; code != 200 {
			t.Fatalf("同键并发失败: %d", code)
		}
	}
	for range 2 {
		request := project.PinSourceCommand{MutationID: id(t), BaseRevision: "2", Commit: command.Commit}
		wait.Go(func() { codes <- call(t, f.handler, "PUT", url, request).Code })
	}
	wait.Wait()
	observed := map[int]int{}
	for range 2 {
		observed[<-codes]++
	}
	if observed[200] != 1 || observed[409] != 1 {
		t.Fatalf("并发固定丢失冲突: %v", observed)
	}
}

// TestSourceStorageFailureRecovery 存储不可用不伪报保存，恢复后允许原键提交。
func TestSourceStorageFailureRecovery(t *testing.T) {
	f := openFixture(t)
	registered, path := registerSource(t, f, true)
	url := sourceURL(registered.Project)
	command := project.PinSourceCommand{MutationID: id(t), BaseRevision: "1", Commit: sourceGit(t, path, "rev-parse", "HEAD")}
	if err := f.database.Close(); err != nil {
		t.Fatal(err)
	}
	decode[any](t, call(t, f.handler, "PUT", url, command), 503, "ErrorResponse")
	f.reopen(t)
	decode[any](t, call(t, f.handler, "GET", "/api/v1/source-mutations/"+command.MutationID, nil), 404, "ErrorResponse")
	value := decode[project.Receipt](t, call(t, f.handler, "PUT", url, command), 200, "SourceReceipt")
	if value.Project.Revision != "2" {
		t.Fatal("原键重试重复推进或丢失版本")
	}
}

// TestEmptySourceRemainsDraft 旧空仓库记录可读取，但不能把缺失对象当固定源码。
func TestEmptySourceRemainsDraft(t *testing.T) {
	f := openFixture(t)
	registered, _ := registerSource(t, f, false)
	url := sourceURL(registered.Project)
	value := decode[project.SourceObservation](t, call(t, f.handler, "GET", url, nil), 200, "SourceObservation")
	if !value.Git.Available || value.Git.HeadCommit != nil || value.Version.PinnedCommit != nil {
		t.Fatal("空仓库被伪报为已固定")
	}
	command := project.PinSourceCommand{MutationID: id(t), BaseRevision: "1", Commit: strings.Repeat("f", 40)}
	decode[any](t, call(t, f.handler, "PUT", url, command), 400, "ErrorResponse")
	decode[any](t, call(t, f.handler, "GET", "/api/v1/source-mutations/"+registered.MutationID, nil), 404, "ErrorResponse")
	decode[any](t, call(t, f.handler, "GET", "/api/v1/source-mutations/bad", nil), 400, "ErrorResponse")
}
