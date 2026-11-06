// 本文件显式实现 file-secrets-v1；明文只受当前 OS 用户保护，不冒充加密凭据库。
package mysqlruntime

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/hailingu/twine-realm/server/internal/privatefiles"
	"github.com/hailingu/twine-realm/server/internal/project"
	"io"
	"io/fs"
)

// 私有文件名与非法路径字符集中固定，避免不同恢复分支使用不同秘密材料。
const (
	projectFilePrefix    = "projects/"
	credentialRecordFile = "credentials.json"
	passwordFileName     = "root.secret"
)

// installation 将本机安装、适配器和初次核实的 daemon 绑定到私有根。
type installation struct {
	SchemaVersion int         `json:"schemaVersion"`
	Adapter       string      `json:"adapter"`
	ID            string      `json:"id"`
	Environment   Environment `json:"environment"`
}

// Driver 只处理其受限根和具有完整归属标签的资源，调用者负责停止 worker 后关闭。
type Driver struct {
	files        *privatefiles.Root
	directory    string
	installation installation
	execute      dockerCommand
	extensions   ComposeExtensions
}

// InitializeDirectory 只创建全新私有根，不接管已有目录或生成已有卷的新密码。
func InitializeDirectory(directory string, forbidden []string, environment Environment) (err error) {
	if !validEnvironment(environment) {
		return failure("RUNTIME_ENVIRONMENT_INVALID", "先核实本机 Docker 与固定镜像再初始化", false, false)
	}
	files, err := privatefiles.Create(directory, forbidden)
	if err != nil {
		return privateFileFailure(err)
	}
	defer joinClose(&err, files)
	id, err := project.NewID()
	if err != nil {
		return err
	}
	value := installation{1, "file-secrets-v1", id, environment}
	driver := &Driver{files: files, directory: files.Path(), installation: value, execute: localDocker}
	for _, name := range []string{"projects", "locks"} {
		if err := files.Mkdir(name); err != nil {
			return privateFileFailure(err)
		}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return driver.writeNew("installation.json", data)
}

// OpenDriver 核实显式配置的根；没有清单或权限不安全时绝不静默重建。
func OpenDriver(directory string, forbidden []string) (driver *Driver, err error) {
	files, err := privatefiles.Open(directory, forbidden)
	if err != nil {
		return nil, privateFileFailure(err)
	}
	driver = &Driver{files: files, directory: files.Path(), execute: localDocker}
	defer func() {
		if err != nil {
			joinClose(&err, files)
		}
	}()
	data, err := driver.readPrivate("installation.json", 4096)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&driver.installation) != nil || driver.installation.SchemaVersion != 1 || driver.installation.Adapter != "file-secrets-v1" || !project.ValidID(driver.installation.ID) {
		return nil, failure("RUNTIME_INSTALLATION_INVALID", "运行根缺少受支持安装身份，原文件保留", false, false)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, failure("RUNTIME_INSTALLATION_INVALID", "安装清单包含额外内容", false, false)
	}
	if !validEnvironment(driver.installation.Environment) {
		return nil, failure("RUNTIME_INSTALLATION_INVALID", "安装清单中的运行身份无效", false, false)
	}
	for _, name := range []string{"projects", "locks"} {
		if err := driver.privateParents(name + "/entry"); err != nil {
			return nil, err
		}
	}
	return driver, nil
}

// Close 在所有后台任务退出后释放目录描述符，不停止容器或清理数据。
func (driver *Driver) Close() error { return driver.files.Close() }

// InstallationID 返回非敏感安装身份，用于元数据库归属核实。
func (driver *Driver) InstallationID() string { return driver.installation.ID }

// Environment 返回初次核实的固定环境，不返回任何秘密位置或内容。
func (driver *Driver) Environment() Environment { return driver.installation.Environment }

// projectDirectory 只为已登记的随机资源身份建立私有子目录。
func (driver *Driver) projectDirectory(resource Resource) error {
	if !project.ValidID(resource.ID) || resource.SecretRef != resource.ID || resource.InstallationID != driver.InstallationID() {
		return failure("RUNTIME_OWNERSHIP_CONFLICT", "运行资源身份不属于此安装", false, false)
	}
	name := projectFilePrefix + resource.ID
	return privateFileFailure(driver.files.Mkdir(name))
}

// prepareSecrets 仅在没有旧卷时生成新密码；已有卷缺秘密时保留并阻塞。
func (driver *Driver) prepareSecrets(resource Resource, volumeFound bool) error {
	if err := driver.projectDirectory(resource); err != nil {
		return err
	}
	password, err := driver.runtimePassword(resource, volumeFound)
	if err != nil {
		return err
	}
	client, record, err := secretMaterials(resource, password)
	if err != nil {
		return err
	}
	prefix := projectFilePrefix + resource.ID + "/"
	current, err := driver.readPrivate(prefix+credentialRecordFile, 4096)
	if errors.Is(err, fs.ErrNotExist) {
		if volumeFound {
			return failure("RUNTIME_SECRET_INVALID", "已有数据卷缺少完整秘密版本证据，保留现场", false, false)
		}
	} else if err != nil {
		return err
	} else if !bytes.Equal(current, record) {
		return failure("RUNTIME_SECRET_INVALID", "原秘密版本已改变，禁止隐式轮换", false, false)
	}
	if err := driver.ensureMaterial(prefix+"client.secret", client); err != nil {
		return err
	}
	return driver.ensureMaterial(prefix+credentialRecordFile, record)
}

// runtimePassword 只为未登记过秘密且没有旧卷的实例分配新密码。
func (driver *Driver) runtimePassword(resource Resource, volumeFound bool) ([]byte, error) {
	prefix := projectFilePrefix + resource.ID + "/"
	password, err := driver.readPrivate(prefix+passwordFileName, 128)
	if errors.Is(err, fs.ErrNotExist) {
		if volumeFound {
			return nil, failure("RUNTIME_SECRET_MISSING", "已有数据卷缺少原秘密，必须恢复原秘密后核实", false, false)
		}
		if _, recordErr := driver.readPrivate(prefix+credentialRecordFile, 4096); !errors.Is(recordErr, fs.ErrNotExist) {
			return nil, failure("RUNTIME_SECRET_MISSING", "原秘密已登记但文件缺失，必须恢复原文件", false, false)
		}
		data := make([]byte, 32)
		if _, err := rand.Read(data); err != nil {
			return nil, failure("RUNTIME_SECRET_UNAVAILABLE", "无法生成运行秘密", false, false)
		}
		password = []byte(hex.EncodeToString(data))
		if err := driver.writeNew(prefix+passwordFileName, password); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return password, nil
}

// secretMaterials 给同一高熵秘密固定内容证据，不向元数据库保存密码或摘要。
func secretMaterials(resource Resource, password []byte) ([]byte, []byte, error) {
	decoded, err := hex.DecodeString(string(password))
	if err != nil || len(decoded) != 32 {
		return nil, nil, failure("RUNTIME_SECRET_INVALID", "原秘密不完整，保留文件并阻止重新生成", false, false)
	}
	hash := sha256.Sum256(password)
	record, err := json.Marshal(struct {
		SchemaVersion int    `json:"schemaVersion"`
		ResourceID    string `json:"resourceId"`
		Digest        string `json:"digest"`
	}{1, resource.ID, "sha256:" + hex.EncodeToString(hash[:])})
	if err != nil {
		return nil, nil, err
	}
	client := []byte("[client]\nuser=root\npassword=" + string(password) + "\nprotocol=socket\n")
	return client, record, nil
}

// verifySecrets 只读核实已有材料，观测和探针绝不创建或改写秘密。
func (driver *Driver) verifySecrets(resource Resource) error {
	prefix := projectFilePrefix + resource.ID + "/"
	password, err := driver.readPrivate(prefix+passwordFileName, 128)
	if err != nil {
		return failure("RUNTIME_SECRET_MISSING", "原运行秘密不可安全读取，保留资源", false, false)
	}
	client, record, err := secretMaterials(resource, password)
	if err != nil {
		return err
	}
	for name, expected := range map[string][]byte{"client.secret": client, credentialRecordFile: record} {
		current, err := driver.readPrivate(prefix+name, 4096)
		if err != nil || !bytes.Equal(current, expected) {
			return failure("RUNTIME_SECRET_INVALID", "原秘密材料缺失、改变或权限不安全，禁止隐式轮换", false, false)
		}
	}
	return nil
}
