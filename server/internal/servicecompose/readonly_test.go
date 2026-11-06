// 本文件验证未就绪声明不会穿过只读预检，不将逻辑检查作为真实Docker运行证据。
package servicecompose

import (
	"context"
	"errors"
	"testing"

	"github.com/hailingu/twine-realm/server/internal/mysqlruntime"
	"github.com/hailingu/twine-realm/server/internal/servicebuild"
	"github.com/hailingu/twine-realm/server/internal/servicedeploy"
)

// readonlySources 只提供原目标查询，意外访问运行实例会通过未实现端口使测试失败。
type readonlySources struct {
	Sources
	err error
}

// RuntimeDeployment 不返回可执行目标，供错误与尚未部署两种路径断言。
func (source readonlySources) RuntimeDeployment(context.Context, string) (*servicedeploy.RuntimeMaterial, error) {
	return nil, source.err
}

// TestDeploymentReadOnlyPreconditions 预检在依赖未就绪时即拒绝，不依赖Docker句柄兜底。
func TestDeploymentReadOnlyPreconditions(t *testing.T) {
	ctx := context.Background()
	driver := &Driver{}
	if _, _, err := driver.Prepare(ctx, mysqlruntime.Snapshot{}, servicebuild.Snapshot{}); err == nil {
		t.Fatal("未就绪数据库进入部署")
	}
	runtime := mysqlruntime.Snapshot{Target: &mysqlruntime.Target{Desired: "running"}, Operation: &mysqlruntime.Operation{State: "succeeded"}, Observation: &mysqlruntime.Observation{Ready: true}, Freshness: "fresh"}
	if _, _, err := driver.Prepare(ctx, runtime, servicebuild.Snapshot{}); err == nil {
		t.Fatal("缺失候选仍访问构建或数据库适配器")
	}
	if _, err := driver.bindingRequest(ctx, servicedeploy.Plan{}); err == nil {
		t.Fatal("损坏计划访问了运行秘密")
	}
	if _, err := NewDriver(nil, nil, nil); err == nil {
		t.Fatal("缺失受信依赖仍启用执行器")
	}
}

// TestRuntimeOverlayReadFailureIsNotAbsence 原目标不可读不能被解释为安全的空扩展。
func TestRuntimeOverlayReadFailureIsNotAbsence(t *testing.T) {
	storage := errors.New("受控目标读取失败")
	for _, expected := range []error{nil, storage} {
		driver := &Driver{sources: readonlySources{err: expected}}
		overlay, err := driver.RuntimeOverlay(context.Background(), mysqlruntime.Resource{})
		if overlay != nil || !errors.Is(err, expected) {
			t.Fatal("读取错误被隐藏或空历史生成了材料", err)
		}
	}
}
