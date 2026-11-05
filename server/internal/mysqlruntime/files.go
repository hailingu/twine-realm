// 本文件显式实现 file-secrets-v1；明文只受当前 OS 用户保护，不冒充加密凭据库。
package mysqlruntime

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// 私有文件名与非法路径字符集中固定，避免不同恢复分支使用不同秘密材料。
const (
	invalidPathCharacters = "$\r\n\x00"
	projectFilePrefix     = "projects/"
	credentialRecordFile  = "credentials.json"
	passwordFileName      = "root.secret"
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
	root         *os.Root
	directory    string
	installation installation
	execute      dockerCommand
}

// InitializeDirectory 只创建全新私有根，不接管已有目录或生成已有卷的新密码。
func InitializeDirectory(directory string, forbidden []string, environment Environment) (err error) {
	if !validEnvironment(environment) {
		return failure("RUNTIME_ENVIRONMENT_INVALID", "先核实本机 Docker 与固定镜像再初始化", false, false)
	}
	path, err := newDirectoryPath(directory, forbidden)
	if err != nil {
		return err
	}
	if err := os.Mkdir(path, 0700); err != nil {
		return failure("RUNTIME_DIRECTORY_EXISTS", "运行根必须是不存在的新目录，已有内容保留", false, false)
	}
	id, err := project.NewID()
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return failure("RUNTIME_DIRECTORY_UNAVAILABLE", "私有运行根不可访问，保留初始化现场", false, false)
	}
	defer joinClose(&err, root)
	value := installation{1, "file-secrets-v1", id, environment}
	driver := &Driver{root: root, directory: path, installation: value, execute: localDocker}
	for _, name := range []string{"projects", "locks"} {
		if err := root.Mkdir(name, 0700); err != nil {
			return failure("RUNTIME_DIRECTORY_UNAVAILABLE", "私有运行子目录初始化失败，保留现场", false, false)
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
	path, err := existingDirectoryPath(directory, forbidden)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, failure("RUNTIME_DIRECTORY_UNAVAILABLE", "私有运行根不可访问", false, false)
	}
	driver = &Driver{root: root, directory: path, execute: localDocker}
	defer func() {
		if err != nil {
			joinClose(&err, root)
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
	value := driver.installation.Environment
	if !validEnvironment(value) {
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
func (driver *Driver) Close() error { return driver.root.Close() }

// InstallationID 返回非敏感安装身份，用于元数据库归属核实。
func (driver *Driver) InstallationID() string { return driver.installation.ID }

// Environment 返回初次核实的固定环境，不返回任何秘密位置或内容。
func (driver *Driver) Environment() Environment { return driver.installation.Environment }

// newDirectoryPath 核实父目录和范围，绝不为宽泛或冲突路径修改权限。
func newDirectoryPath(directory string, forbidden []string) (string, error) {
	if !filepath.IsAbs(directory) || strings.ContainsAny(directory, invalidPathCharacters) {
		return "", failure("RUNTIME_DIRECTORY_INVALID", "运行根须为不含插值字符的绝对路径", false, false)
	}
	clean := filepath.Clean(directory)
	if _, err := os.Lstat(clean); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return "", failure("RUNTIME_DIRECTORY_EXISTS", "运行根已存在或无法安全核实，原内容保留", false, false)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(clean))
	if err != nil {
		return "", failure("RUNTIME_DIRECTORY_INVALID", "运行根的父目录必须已经存在", false, false)
	}
	path := filepath.Join(parent, filepath.Base(clean))
	if strings.ContainsAny(path, invalidPathCharacters) {
		return "", failure("RUNTIME_DIRECTORY_INVALID", "规范运行路径不得含插值字符", false, false)
	}
	if err := outsideRepositories(path, forbidden); err != nil {
		return "", err
	}
	return path, nil
}

// existingDirectoryPath 允许系统父路径的规范化，根本身与内部材料禁止符号链接。
func existingDirectoryPath(directory string, forbidden []string) (string, error) {
	if !filepath.IsAbs(directory) || strings.ContainsAny(directory, invalidPathCharacters) {
		return "", failure("RUNTIME_DIRECTORY_INVALID", "运行根配置无效", false, false)
	}
	info, err := os.Lstat(directory)
	if err != nil || !privateInfo(info, true) {
		return "", failure("RUNTIME_DIRECTORY_UNSAFE", "运行根必须由当前用户拥有且权限为0700，不得使用链接", false, false)
	}
	path, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return "", failure("RUNTIME_DIRECTORY_UNAVAILABLE", "无法核实私有根", false, false)
	}
	if strings.ContainsAny(path, invalidPathCharacters) {
		return "", failure("RUNTIME_DIRECTORY_INVALID", "规范运行路径不得含插值字符", false, false)
	}
	if err := outsideRepositories(path, forbidden); err != nil {
		return "", err
	}
	return path, nil
}

// outsideRepositories 同时拒绝仓库内部与包含仓库的宽泛根。
func outsideRepositories(path string, forbidden []string) error {
	for _, directory := range forbidden {
		absolute, err := filepath.Abs(directory)
		if err != nil {
			return failure("RUNTIME_DIRECTORY_INVALID", "禁止范围配置无效", false, false)
		}
		canonical, err := forbiddenDirectory(absolute)
		if err != nil {
			return failure("RUNTIME_DIRECTORY_INVALID", "无法核实禁止范围", false, false)
		}
		if within(path, canonical) || within(canonical, path) {
			return failure("RUNTIME_DIRECTORY_UNSAFE", "运行根必须位于仓库与平台静态目录范围之外", false, false)
		}
	}
	return nil
}

// within 以路径段核实包含关系，避免字符串前缀误判相邻目录。
func within(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && (relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
}

// privateInfo 核实对象类型、权限与当前 OS 所有者。
func privateInfo(info fs.FileInfo, directory bool) bool {
	if info == nil || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Getuid() {
		return false
	}
	if directory {
		return info.IsDir() && info.Mode().Perm() == 0700
	}
	return info.Mode().IsRegular() && info.Mode().Perm() == 0600
}

// privateParents 核实根内全部父目录，不把链接或额外权限当普通路径使用。
func (driver *Driver) privateParents(name string) error {
	opened, err := driver.root.Stat(".")
	if err != nil {
		return failure("RUNTIME_DIRECTORY_UNAVAILABLE", "私有运行根不可访问", false, false)
	}
	current, err := os.Lstat(driver.directory)
	if err != nil || !privateInfo(opened, true) || !privateInfo(current, true) || !os.SameFile(opened, current) {
		return failure("RUNTIME_DIRECTORY_UNSAFE", "私有根权限或路径已改变，停止读取与执行", false, false)
	}
	if !fs.ValidPath(name) || strings.Contains(name, "\\") {
		return failure("RUNTIME_FILE_UNSAFE", "运行材料路径无效", false, false)
	}
	parts := strings.Split(name, "/")
	for index := 1; index < len(parts); index++ {
		info, err := driver.root.Lstat(strings.Join(parts[:index], "/"))
		if err != nil || !privateInfo(info, true) {
			return failure("RUNTIME_FILE_UNSAFE", "运行材料父目录不存在、权限不安全或含链接", false, false)
		}
	}
	return nil
}

// readPrivate 非阻塞且不跟随最终链接，只接受有界0600普通文件。
func (driver *Driver) readPrivate(name string, limit int64) (data []byte, err error) {
	if err := driver.privateParents(name); err != nil {
		return nil, err
	}
	file, err := driver.root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fs.ErrNotExist
	}
	if err != nil {
		return nil, failure("RUNTIME_FILE_UNSAFE", "运行材料无法安全读取", false, false)
	}
	defer joinClose(&err, file)
	info, err := file.Stat()
	if err != nil || !privateInfo(info, false) || info.Size() > limit {
		return nil, failure("RUNTIME_FILE_UNSAFE", "运行材料必须为当前用户的0600有界普通文件", false, false)
	}
	data, err = io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, failure("RUNTIME_FILE_UNSAFE", "运行材料读取失败或超限", false, false)
	}
	return data, nil
}

// writeNew 仅独占创建固定材料，失败保留现场，不删除可能被后来写入的数据。
func (driver *Driver) writeNew(name string, data []byte) (err error) {
	if err := driver.privateParents(name); err != nil {
		return err
	}
	file, err := driver.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return failure("RUNTIME_FILE_CONFLICT", "运行材料已存在或不能独占创建，原内容保留", false, false)
	}
	defer joinClose(&err, file)
	if _, err := file.Write(data); err != nil {
		return failure("RUNTIME_FILE_UNAVAILABLE", "运行材料写入失败，保留现场", false, false)
	}
	if err := file.Sync(); err != nil {
		return failure("RUNTIME_FILE_UNAVAILABLE", "运行材料同步失败，保留现场", false, false)
	}
	return nil
}

// projectDirectory 只为已登记的随机资源身份建立私有子目录。
func (driver *Driver) projectDirectory(resource Resource) error {
	if !project.ValidID(resource.ID) || resource.SecretRef != resource.ID || resource.InstallationID != driver.InstallationID() {
		return failure("RUNTIME_OWNERSHIP_CONFLICT", "运行资源身份不属于此安装", false, false)
	}
	name := projectFilePrefix + resource.ID
	if err := driver.root.Mkdir(name, 0700); err != nil && !errors.Is(err, fs.ErrExist) {
		return failure("RUNTIME_DIRECTORY_UNAVAILABLE", "无法准备私有运行目录", false, false)
	}
	return driver.privateParents(name + "/entry")
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

// ensureMaterial 保留原字节，不能通过覆盖既有运行材料偷换凭据或配置。
func (driver *Driver) ensureMaterial(name string, data []byte) error {
	current, err := driver.readPrivate(name, 65536)
	if errors.Is(err, fs.ErrNotExist) {
		return driver.writeNew(name, data)
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(current, data) {
		return failure("RUNTIME_FILE_CONFLICT", "既有运行材料与固定目标不符，保留现场", false, false)
	}
	return nil
}
