// 本文件证明无效原身份和已取消上下文在任何资源动作之前失败。
package mysqlruntime

import (
	"context"
	"errors"
	"testing"
)

// TestExistingRejectsInvalidIdentity 严格入口不将损坏身份转为创建或外部操作。
func TestExistingRejectsInvalidIdentity(t *testing.T) {
	driver := &Driver{}
	ctx := context.Background()
	if _, _, err := driver.InspectExisting(ctx, Resource{}); err == nil {
		t.Fatal("接受空资源身份")
	}
	if err := driver.ChangeExisting(ctx, Resource{}, ExistingIdentity{}, true); err == nil {
		t.Fatal("无效资源触发恢复")
	}
	if err := driver.VerifyExistingBindingMaterials(ctx, BindingRequest{}, nil); err == nil {
		t.Fatal("无效绑定被接受")
	}
	if err := driver.WithExistingBinding(ctx, BindingRequest{}, func(*BindingSession) error { t.Fatal("执行了无效绑定回调"); return nil }); err == nil {
		t.Fatal("无效绑定未拒绝")
	}
}

// TestExistingProjectCancelled 已取消拥有者不能获取锁或调用动作。
func TestExistingProjectCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := (&Driver{}).WithExistingProject(ctx, "invalid", func() error { t.Fatal("取消后仍执行动作"); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatal("取消错误未保留", err)
	}
}
