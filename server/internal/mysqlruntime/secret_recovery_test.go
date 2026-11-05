// 本文件验证损坏或丢失的已登记秘密不会被重置，全部文件均属于独占测试目录。
package mysqlruntime

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// privateFailure 要求稳定安全代码，不依赖错误文案或回显材料内容。
func privateFailure(t *testing.T, err error, code string) {
	t.Helper()
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != code {
		t.Fatalf("期望私有材料诊断 %s，实际 %v", code, err)
	}
}

// secretState 保留可比较的存在性、权限和字节，不将真实随机材料输出到日志。
type secretState struct {
	Exists bool
	Mode   fs.FileMode
	Data   string
}

// secretDamage 描述独占测试材料的一种外部损坏，不向产品提供文件修改入口。
type secretDamage struct {
	name, file, contents, code string
	remove, public, volume     bool
}

// damageSecret 在断言前制造真实文件损坏，恢复调用本身不得再次修改材料。
func damageSecret(t *testing.T, directory string, item secretDamage) {
	t.Helper()
	path := filepath.Join(directory, item.file)
	var err error
	switch {
	case item.remove:
		err = os.Remove(path)
	case item.public:
		err = os.Chmod(path, 0644)
	default:
		err = os.WriteFile(path, []byte(item.contents), 0600)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// secretSnapshot 只读取本测试创建的三个已知材料，不能扫描用户目录。
func secretSnapshot(t *testing.T, directory string) map[string]secretState {
	t.Helper()
	result := map[string]secretState{}
	for _, name := range []string{passwordFileName, "client.secret", credentialRecordFile} {
		path := filepath.Join(directory, name)
		info, err := os.Stat(path)
		if errors.Is(err, fs.ErrNotExist) {
			result[name] = secretState{}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		result[name] = secretState{true, info.Mode().Perm(), string(data)}
	}
	return result
}

// TestSecretRecoveryPreservesDamagedEvidence 损坏、丢失与权限改变必须阻塞且原字节完整保留。
func TestSecretRecoveryPreservesDamagedEvidence(t *testing.T) {
	cases := []secretDamage{
		{"密码长度错误", passwordFileName, "ab", "RUNTIME_SECRET_INVALID", false, false, true},
		{"密码不是十六进制", passwordFileName, "not-hex", "RUNTIME_SECRET_INVALID", false, false, true},
		{"密码公开权限", passwordFileName, "", "RUNTIME_FILE_UNSAFE", false, true, true},
		{"版本证据改变", credentialRecordFile, "{}", "RUNTIME_SECRET_INVALID", false, false, true},
		{"已有卷缺版本证据", credentialRecordFile, "", "RUNTIME_SECRET_INVALID", true, false, true},
		{"客户端文件改变", "client.secret", "changed", "RUNTIME_FILE_CONFLICT", false, false, true},
		{"客户端公开权限", "client.secret", "", "RUNTIME_FILE_UNSAFE", false, true, true},
		{"版本证据公开权限", credentialRecordFile, "", "RUNTIME_FILE_UNSAFE", false, true, true},
		{"无卷但原密码已登记", passwordFileName, "", "RUNTIME_SECRET_MISSING", true, false, false},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			driver, root := openPrivateFixture(t)
			resource := testResource(t, driver)
			if err := driver.prepareSecrets(resource, false); err != nil {
				t.Fatal(err)
			}
			directory := filepath.Join(root, "projects", resource.ID)
			damageSecret(t, directory, item)
			before := secretSnapshot(t, directory)
			privateFailure(t, driver.prepareSecrets(resource, item.volume), item.code)
			if !reflect.DeepEqual(secretSnapshot(t, directory), before) {
				t.Fatal("失败恢复改写或补造了已有材料")
			}
		})
	}
}

// TestObservationNeverCreatesMissingSecrets 只读观测不因派生材料可重建就隐式写入。
func TestObservationNeverCreatesMissingSecrets(t *testing.T) {
	for _, name := range []string{passwordFileName, "client.secret", credentialRecordFile} {
		t.Run(name, func(t *testing.T) {
			driver, root := openPrivateFixture(t)
			resource := testResource(t, driver)
			if err := driver.prepareSecrets(resource, false); err != nil {
				t.Fatal(err)
			}
			directory := filepath.Join(root, "projects", resource.ID)
			if err := os.Remove(filepath.Join(directory, name)); err != nil {
				t.Fatal(err)
			}
			before := secretSnapshot(t, directory)
			code := "RUNTIME_SECRET_INVALID"
			if name == passwordFileName {
				code = "RUNTIME_SECRET_MISSING"
			}
			privateFailure(t, driver.verifySecrets(resource), code)
			if !reflect.DeepEqual(secretSnapshot(t, directory), before) {
				t.Fatal("观测创建或改写了秘密")
			}
		})
	}
}

// TestForeignSecretResourcesCannotCreateFiles 外来安装、改义引用和无效资源身份不分配秘密。
func TestForeignSecretResourcesCannotCreateFiles(t *testing.T) {
	for _, mutate := range []func(*Resource){
		func(value *Resource) { value.InstallationID = "other-installation" },
		func(value *Resource) { value.SecretRef = "other-resource" },
		func(value *Resource) { value.ID = "invalid"; value.SecretRef = "invalid" },
	} {
		driver, root := openPrivateFixture(t)
		resource := testResource(t, driver)
		mutate(&resource)
		privateFailure(t, driver.prepareSecrets(resource, false), "RUNTIME_OWNERSHIP_CONFLICT")
		entries, err := os.ReadDir(filepath.Join(root, "projects"))
		if err != nil || len(entries) != 0 {
			t.Fatal("外来资源产生了秘密目录")
		}
	}
}

// TestMaterialConflictNeverOverwrites 既有文件、超限材料和无效父路径都不能被替换为新配置。
func TestMaterialConflictNeverOverwrites(t *testing.T) {
	driver, root := openPrivateFixture(t)
	for _, name := range []string{"../installation.json", "./installation.json", "projects\\entry", "projects/missing/entry"} {
		_, err := driver.readPrivate(name, 128)
		privateFailure(t, err, "RUNTIME_FILE_UNSAFE")
	}
	if err := driver.writeNew("marker", []byte("original")); err != nil {
		t.Fatal(err)
	}
	privateFailure(t, driver.writeNew("marker", []byte("replacement")), "RUNTIME_FILE_CONFLICT")
	privateFailure(t, driver.ensureMaterial("marker", []byte("replacement")), "RUNTIME_FILE_CONFLICT")
	_, err := driver.readPrivate("marker", 4)
	privateFailure(t, err, "RUNTIME_FILE_UNSAFE")
	data, err := os.ReadFile(filepath.Join(root, "marker"))
	if err != nil || !bytes.Equal(data, []byte("original")) {
		t.Fatal("冲突或超限读取改变了原文件")
	}
}
