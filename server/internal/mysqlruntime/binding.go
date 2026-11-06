// 本文件为已接受的业务绑定提供受限管理边界，管理密码与任意SQL不离开本包。
package mysqlruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// BindingRequest 固定原数据库身份与两种用途账号，不接受连接串、SQL或秘密。
type BindingRequest struct {
	Resource                                                           Resource
	ServerUUID, ServiceID, ApplicationUser, MigrationUser, AccountHost string
	Binding                                                            project.DatabaseBinding
}

// BindingInfo 仅返回经过认证的原数据身份和实际私网，不包含root材料。
type BindingInfo struct{ ServerUUID, ContainerID, NetworkCIDR string }

// BindingFiles 仅向运行编排器提供当前服务两份只读秘密的路径，路径不能进入API或日志。
type BindingFiles struct{ Application, Migration string }

// BindingSession 串行拥有项目锁内的管理会话，调用者不能执行任意SQL。
type BindingSession struct {
	driver  *Driver
	request BindingRequest
	sql     *sqlSession
	prefix  string
}

var businessAccount = regexp.MustCompile(`^tr[am]_[0-9a-f]{24}$`)
var businessDatabase = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
var accountSubnet = regexp.MustCompile(`^[0-9.]+/[0-9.]+$`)

// BindingDatabase 只读核实原MySQL与Docker实际网络，预览不会创建账号或目录。
func (driver *Driver) BindingDatabase(ctx context.Context, resource Resource) (BindingInfo, error) {
	var value BindingInfo
	fact, err := driver.Inspect(ctx, resource)
	if err != nil {
		return value, err
	}
	if fact == nil || !fact.Ready {
		return value, failure("DEPLOY_DATABASE_NOT_READY", "原MySQL尚未认证就绪", false, false)
	}
	data, err := driver.execute(ctx, contextOption, driver.Environment().Context, "network", "inspect", resource.Network)
	if err != nil {
		return value, err
	}
	var networks []struct {
		IPAM struct{ Config []struct{ Subnet string } }
	}
	if json.Unmarshal(data, &networks) != nil || len(networks) != 1 || len(networks[0].IPAM.Config) != 1 {
		return value, failure("DEPLOY_NETWORK_UNSUPPORTED", "业务运行需要明确的单一IPv4私网", false, false)
	}
	return BindingInfo{ServerUUID: fact.ServerUUID, ContainerID: fact.ContainerID, NetworkCIDR: networks[0].IPAM.Config[0].Subnet}, nil
}

// WithBinding 与MySQL运行共用项目文件锁，并核实原UUID和数据库侧互斥后才交给受控管理器。
func (driver *Driver) WithBinding(ctx context.Context, request BindingRequest, action func(*BindingSession) error) (err error) {
	if err := driver.validateBinding(request); err != nil {
		return err
	}
	unlock, err := driver.projectLock(request.Resource.ProjectID)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	info, err := driver.BindingDatabase(ctx, request.Resource)
	if err != nil {
		return err
	}
	if info.ServerUUID != request.ServerUUID {
		return failure("RUNTIME_DATABASE_IDENTITY_CONFLICT", "业务绑定指向的原数据库身份已改变", false, false)
	}
	_, network, err := net.ParseCIDR(info.NetworkCIDR)
	if err != nil || network.IP.To4() == nil || network.IP.String()+"/"+net.IP(network.Mask).String() != request.AccountHost {
		return failure("DEPLOY_NETWORK_CHANGED", "原私网网段与用途账号Host不一致，禁止扩大账号访问范围", false, false)
	}
	connection, err := driver.databaseSQLWithTimeout(ctx, request.Resource, info.ContainerID, 6*time.Minute)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, connection.Close()) }()
	rows, err := connection.query(ctx, "SELECT GET_LOCK("+sqlText("twine-binding:"+request.Binding.ID)+",0)")
	if err != nil {
		return err
	}
	if len(rows) != 1 || rows[0] != "1" {
		return failure("DEPLOY_DATABASE_BUSY", "原绑定管理会话仍未退出，请核实原操作", true, true)
	}
	session := &BindingSession{driver: driver, request: request, sql: connection, prefix: "projects/" + request.Resource.ID + "/services/" + request.ServiceID + "/"}
	return action(session)
}

// validateBinding 禁止通过持久计划为任意系统库、账号或组件生成管理SQL。
func (driver *Driver) validateBinding(request BindingRequest) error {
	if err := driver.validateResource(request.Resource); err != nil {
		return err
	}
	b := request.Binding
	if b.Purpose != "readWrite" && b.Purpose != "readOnly" {
		return failure("DEPLOY_BINDING_INVALID", "业务数据库用途无效，禁止默认扩大权限", false, false)
	}
	if !project.ValidID(request.ServiceID) || !project.ValidID(request.ServerUUID) || !project.ValidID(b.ID) || !project.ValidID(b.DatabaseID) || !project.ValidID(b.Credential.ID) || !Number(b.Credential.Version) || b.Source.ComponentID != request.ServiceID || b.Target.ComponentID != request.Resource.ComponentID || !businessAccount.MatchString(request.ApplicationUser) || !strings.HasPrefix(request.ApplicationUser, "tra_") || !businessAccount.MatchString(request.MigrationUser) || !strings.HasPrefix(request.MigrationUser, "trm_") || !businessDatabase.MatchString(b.DatabaseName) || !accountSubnet.MatchString(request.AccountHost) {
		return failure("DEPLOY_BINDING_INVALID", "持久业务绑定不属于受控数据库用途", false, false)
	}
	for _, name := range []string{"mysql", "information_schema", "performance_schema", "sys", "twine_runtime_control"} {
		if strings.EqualFold(b.DatabaseName, name) {
			return failure("DEPLOY_BINDING_INVALID", "系统或控制数据库不能作为业务绑定", false, false)
		}
	}
	return nil
}

// sqlText 只编码已校验的受控标量，十六进制字面量不受SQL模式或反斜杠转义影响。
func sqlText(value string) string { return fmt.Sprintf("CONVERT(0x%x USING utf8mb4)", []byte(value)) }

// sqlIdentifier 为已限制长度与字符的业务标识符加引用，不拼接用户SQL。
func sqlIdentifier(value string) string { return "`" + strings.ReplaceAll(value, "`", "``") + "`" }

// accountSQL 的用户名与Host在进入会话前已被白名单限制。
func (session *BindingSession) accountSQL(user string) string {
	return "'" + user + "'@'" + session.request.AccountHost + "'"
}

// Files 仅返回本绑定角色文件，调用者不得转发到HTTP、事件或业务源码。
func (session *BindingSession) Files() BindingFiles {
	return BindingFiles{Application: filepath.Join(session.driver.directory, session.prefix, "app.secret"), Migration: filepath.Join(session.driver.directory, session.prefix, "migration.secret")}
}

// BindingPaths 仅定位此受控绑定的固定角色文件，不创建目录或读取密码。
func (driver *Driver) BindingPaths(request BindingRequest) (BindingFiles, error) {
	if err := driver.validateBinding(request); err != nil {
		return BindingFiles{}, err
	}
	session := BindingSession{driver: driver, request: request, prefix: "projects/" + request.Resource.ID + "/services/" + request.ServiceID + "/"}
	return session.Files(), nil
}

// Verify 只读核实原绑定与秘密版本；采集不得调用Prepare补建丢失对象。
func (session *BindingSession) Verify(ctx context.Context) error {
	state, err := session.bindingState(ctx)
	if err != nil {
		return err
	}
	if state != "created" {
		return failure("DEPLOY_BINDING_UNCONFIRMED", "业务绑定尚未完成登记，须核实原部署", false, true)
	}
	if err := session.bindingSecrets(ctx, true); err != nil {
		return err
	}
	if err := session.verifyBusinessIdentity(ctx); err != nil {
		return err
	}
	intent, err := session.bindingIntent()
	if err != nil {
		return err
	}
	for _, user := range []string{session.request.ApplicationUser, session.request.MigrationUser} {
		if err := session.verifyAccount(ctx, user, intent); err != nil {
			return err
		}
	}
	return session.verifyPrivileges(ctx, true)
}

// Material 独占创建或逐字节核实固定服务overlay，不开放任意文件路径写入。
func (session *BindingSession) Material(ctx context.Context, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(data) == 0 || len(data) > 32768 {
		return failure("DEPLOY_MATERIAL_INVALID", "部署材料超过受控边界", false, false)
	}
	return session.driver.ensureMaterial(session.prefix+"compose.json", data)
}
