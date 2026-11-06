// 本文件用真实私有目录验证首次缺席许可不会放宽既有材料、父路径和字节校验。
package mysqlruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// extensionFileFixture 仅创建本次私有材料，不假装已取得真实Docker资源缺席证明。
func extensionFileFixture(t *testing.T) (*Driver, Resource, ComposeOverlay, string) {
	t.Helper()
	driver, root := openPrivateFixture(t)
	resource := configuredResource(t, driver)
	if err := driver.prepareSecrets(resource, false); err != nil {
		t.Fatal(err)
	}
	value := ComposeOverlay{ServiceID: resource.ProjectID, Data: []byte(`{"services":{}}`)}
	return driver, resource, value, root
}

// TestComposeExtensionNeverCreatedAndStrictDefault 只有受信缺席许可可接受尚未生成；缺省仍拒绝缺失。
func TestComposeExtensionNeverCreatedAndStrictDefault(t *testing.T) {
	driver, resource, overlay, root := extensionFileFixture(t)
	ctx := context.Background()
	if _, err := driver.composeExtensionFile(ctx, resource, overlay); err == nil {
		t.Fatal("缺省放过了丢失扩展")
	}
	overlay.Unmaterialized = true
	if name, err := driver.composeExtensionFile(ctx, resource, overlay); err != nil || name != "" {
		t.Fatal("首次尚未生成却阻断原MySQL", err)
	}
	if _, err := os.Stat(filepath.Join(root, "projects", resource.ID, "services")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("只读缺席检查创建了业务目录", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := driver.composeExtensionFile(ctx, resource, overlay); !errors.Is(err, context.Canceled) {
		t.Fatal("取消后仍检查或准备材料", err)
	}
}

// TestComposeExtensionExistingBytesRemainStrict 已有完整材料可使用，未完成许可仍不能忽略内容改写。
func TestComposeExtensionExistingBytesRemainStrict(t *testing.T) {
	driver, resource, overlay, root := extensionFileFixture(t)
	prefix := "projects/" + resource.ID + "/services"
	for _, name := range []string{prefix, prefix + "/" + overlay.ServiceID} {
		if err := driver.files.Mkdir(name); err != nil {
			t.Fatal(err)
		}
	}
	name := prefix + "/" + overlay.ServiceID + "/compose.json"
	if err := driver.writeNew(name, overlay.Data); err != nil {
		t.Fatal(err)
	}
	for _, unmaterialized := range []bool{false, true} {
		overlay.Unmaterialized = unmaterialized
		file, err := driver.composeExtensionFile(context.Background(), resource, overlay)
		if err != nil || (file == "") != unmaterialized {
			t.Fatal("材料使用未区分受信未完成许可", err)
		}
	}
	absolute := filepath.Join(root, filepath.FromSlash(name))
	if err := os.WriteFile(absolute, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := driver.composeExtensionFile(context.Background(), resource, overlay); err == nil {
		t.Fatal("未完成许可忽略了被改写材料")
	}
	if err := os.Remove(absolute); err != nil {
		t.Fatal(err)
	}
	overlay.Unmaterialized = false
	if _, err := driver.composeExtensionFile(context.Background(), resource, overlay); err == nil {
		t.Fatal("已生成材料丢失被静默补写或放过")
	}
}

// TestComposeExtensionUnsafeParentIsNotAbsence 链接和不安全权限不能冒充首次缺席。
func TestComposeExtensionUnsafeParentIsNotAbsence(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		driver, resource, overlay, root := extensionFileFixture(t)
		overlay.Unmaterialized = true
		name := filepath.Join(root, "projects", resource.ID, "services")
		var err error
		if symlink {
			err = os.Symlink(t.TempDir(), name)
		} else {
			err = os.Mkdir(name, 0755)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := driver.composeExtensionFile(context.Background(), resource, overlay); err == nil {
			t.Fatal("不安全父路径被当作缺席")
		}
	}
}
