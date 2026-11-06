// 本文件登记业务库归属；未知CREATE结果保留现场，不把同名数据库静默收编。
package mysqlruntime

import (
	"context"
	"fmt"
	"strings"
)

const registryDatabase = "twine_runtime_control"
const registryTable = "`twine_runtime_control`.`bindings`"

// Prepare 先登记受理身份与私有秘密，再创建全新业务库与两种用途账号。
func (session *BindingSession) Prepare(ctx context.Context) error {
	if err := session.ensureRegistry(ctx); err != nil {
		return err
	}
	state, err := session.bindingState(ctx)
	if err != nil {
		return err
	}
	if err := session.bindingSecrets(ctx, state != ""); err != nil {
		return err
	}
	if state == "" {
		if err := session.newBinding(ctx); err != nil {
			return err
		}
		state = "intent"
	}
	if state == "intent" {
		if err := session.createBusinessDatabase(ctx); err != nil {
			return err
		}
	}
	if err := session.verifyBusinessIdentity(ctx); err != nil {
		return err
	}
	if err := session.ensureAccount(ctx, session.request.ApplicationUser, "app.secret"); err != nil {
		return err
	}
	if err := session.ensureAccount(ctx, session.request.MigrationUser, "migration.secret"); err != nil {
		return err
	}
	return session.verifyPrivileges(ctx, false)
}

// ensureRegistry 只接受唯一固定结构和此运行安装的登记表，部分初始化不自动补DDL。
func (session *BindingSession) ensureRegistry(ctx context.Context) error {
	rows, err := session.sql.query(ctx, "SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME="+sqlText(registryDatabase))
	if err != nil {
		return err
	}
	if len(rows) != 1 {
		return session.invalidRegistry()
	}
	if rows[0] == "0" {
		if err := session.createRegistry(ctx); err != nil {
			return err
		}
	} else if rows[0] != "1" {
		return session.invalidRegistry()
	}
	rows, err = session.sql.query(ctx, "SELECT CONCAT(TABLE_NAME,':',COLUMN_NAME,':',COLUMN_TYPE) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA="+sqlText(registryDatabase)+" ORDER BY TABLE_NAME,ORDINAL_POSITION")
	if err != nil {
		return err
	}
	expected := []string{"bindings:binding_id:char(36)", "bindings:installation_id:char(36)", "bindings:intent_digest:char(71)", "bindings:stage:varchar(16)", "installation:id:tinyint", "installation:installation_id:char(36)", "installation:server_uuid:char(36)"}
	if strings.Join(rows, "\n") != strings.Join(expected, "\n") {
		return session.invalidRegistry()
	}
	rows, err = session.sql.query(ctx, "SELECT CONCAT(id,':',installation_id,':',server_uuid) FROM `twine_runtime_control`.`installation`")
	if err != nil {
		return err
	}
	if len(rows) != 1 || rows[0] != "1:"+session.request.Resource.InstallationID+":"+session.request.ServerUUID {
		return session.invalidRegistry()
	}
	rows, err = session.sql.query(ctx, "SELECT COUNT(*) FROM "+registryTable+" WHERE installation_id<>"+sqlText(session.request.Resource.InstallationID))
	if err != nil {
		return err
	}
	if len(rows) != 1 || rows[0] != "0" {
		return session.invalidRegistry()
	}
	return nil
}

// createRegistry 仅用于已明确不存在的新控制库，部分初始化不通过此函数补建。
func (session *BindingSession) createRegistry(ctx context.Context) error {
	queries := []string{"CREATE DATABASE `twine_runtime_control` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci", "CREATE TABLE `twine_runtime_control`.`installation` (id TINYINT PRIMARY KEY,installation_id CHAR(36) CHARACTER SET ascii NOT NULL,server_uuid CHAR(36) CHARACTER SET ascii NOT NULL) ENGINE=InnoDB", "INSERT INTO `twine_runtime_control`.`installation` VALUES(1," + sqlText(session.request.Resource.InstallationID) + "," + sqlText(session.request.ServerUUID) + ")", "CREATE TABLE " + registryTable + " (binding_id CHAR(36) CHARACTER SET ascii PRIMARY KEY, installation_id CHAR(36) CHARACTER SET ascii NOT NULL, intent_digest CHAR(71) CHARACTER SET ascii NOT NULL, stage VARCHAR(16) NOT NULL) ENGINE=InnoDB"}
	for _, query := range queries {
		if _, err := session.sql.query(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

// bindingIntent 归属摘要不含秘密；恢复不能换库、服务、凭据版本或网段后沿用旧登记。
func (session *BindingSession) bindingIntent() (string, error) {
	r := session.request
	return Fingerprint(struct {
		Installation, Resource, UUID, Service, App, Migration, Host string
		Binding                                                     any
	}{r.Resource.InstallationID, r.Resource.ID, r.ServerUUID, r.ServiceID, r.ApplicationUser, r.MigrationUser, r.AccountHost, r.Binding})
}

// bindingState 已有同名库缺原登记时不返回可接管的空状态。
func (session *BindingSession) bindingState(ctx context.Context) (string, error) {
	digest, err := session.bindingIntent()
	if err != nil {
		return "", err
	}
	rows, err := session.sql.query(ctx, "SELECT CONCAT(installation_id,':',intent_digest,':',stage) FROM "+registryTable+" WHERE binding_id="+sqlText(session.request.Binding.ID))
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	prefix := session.request.Resource.InstallationID + ":" + digest + ":"
	if len(rows) != 1 || !strings.HasPrefix(rows[0], prefix) {
		return "", session.invalidRegistry()
	}
	state := strings.TrimPrefix(rows[0], prefix)
	if state != "intent" && state != "created" {
		return "", session.invalidRegistry()
	}
	return state, nil
}

// newBinding 先确认业务库和两种账号均不存在，再保存创建意图；不执行IF NOT EXISTS收编。
func (session *BindingSession) newBinding(ctx context.Context) error {
	r := session.request
	query := "SELECT (SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME=" + sqlText(r.Binding.DatabaseName) + ")+(SELECT COUNT(*) FROM mysql.user WHERE User IN ('" + r.ApplicationUser + "','" + r.MigrationUser + "'))"
	rows, err := session.sql.query(ctx, query)
	if err != nil {
		return err
	}
	if len(rows) != 1 || rows[0] != "0" {
		return failure("DEPLOY_DATABASE_OWNERSHIP_CONFLICT", "同名业务库或用途账号已存在，禁止接管或重置密码", false, false)
	}
	digest, err := session.bindingIntent()
	if err != nil {
		return err
	}
	_, err = session.sql.query(ctx, "INSERT INTO "+registryTable+"(binding_id,installation_id,intent_digest,stage) VALUES("+sqlText(r.Binding.ID)+","+sqlText(r.Resource.InstallationID)+","+sqlText(digest)+",'intent')")
	return err
}

// createBusinessDatabase 未确认DDL到登记之间的中断进入人工核实，不盲目重跑已存在库。
func (session *BindingSession) createBusinessDatabase(ctx context.Context) error {
	rows, err := session.sql.query(ctx, "SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME="+sqlText(session.request.Binding.DatabaseName))
	if err != nil {
		return err
	}
	if len(rows) != 1 || rows[0] != "0" {
		return failure("DEPLOY_DATABASE_CREATE_UNKNOWN", "原建库结果未登记完成，请核实原库归属，禁止重复建库", false, true)
	}
	if _, err := session.sql.query(ctx, "CREATE DATABASE "+sqlIdentifier(session.request.Binding.DatabaseName)+" CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"); err != nil {
		return err
	}
	identity := sqlIdentifier(session.request.Binding.DatabaseName) + ".`twine_binding_identity`"
	if _, err := session.sql.query(ctx, "CREATE TABLE "+identity+" (id TINYINT PRIMARY KEY,binding_id CHAR(36) CHARACTER SET ascii NOT NULL,database_id CHAR(36) CHARACTER SET ascii NOT NULL) ENGINE=InnoDB"); err != nil {
		return err
	}
	if _, err := session.sql.query(ctx, "INSERT INTO "+identity+" VALUES(1,"+sqlText(session.request.Binding.ID)+","+sqlText(session.request.Binding.DatabaseID)+")"); err != nil {
		return err
	}
	_, err = session.sql.query(ctx, "UPDATE "+registryTable+" SET stage='created' WHERE binding_id="+sqlText(session.request.Binding.ID)+" AND stage='intent'")
	return err
}

// verifyBusinessIdentity 检测已登记业务库或身份表的删除、替换，禁止静默补建后重跑DDL。
func (session *BindingSession) verifyBusinessIdentity(ctx context.Context) error {
	rows, err := session.sql.query(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA="+sqlText(session.request.Binding.DatabaseName)+" AND TABLE_NAME='twine_binding_identity' AND TABLE_TYPE='BASE TABLE'")
	if err != nil {
		return err
	}
	if len(rows) != 1 || rows[0] != "1" {
		return failure("DEPLOY_DATABASE_IDENTITY_CONFLICT", "原业务库或绑定身份表缺失，禁止补建或重新迁移", false, true)
	}
	rows, err = session.sql.query(ctx, "SELECT CONCAT(id,':',binding_id,':',database_id) FROM "+sqlIdentifier(session.request.Binding.DatabaseName)+".`twine_binding_identity`")
	if err != nil {
		return err
	}
	if len(rows) != 1 || rows[0] != "1:"+session.request.Binding.ID+":"+session.request.Binding.DatabaseID {
		return failure("DEPLOY_DATABASE_IDENTITY_CONFLICT", "业务库绑定身份发生漂移，保留数据等待核实", false, true)
	}
	return nil
}

// invalidRegistry 不回显可能被外部改写的数据库内容。
func (session *BindingSession) invalidRegistry() error {
	return failure("DEPLOY_REGISTRY_CONFLICT", fmt.Sprintf("业务归属登记不完整或不匹配，保留原数据并核实%s", registryDatabase), false, true)
}
