// 本文件只在私有运行根保存业务用途秘密；元数据库、Compose环境与普通日志均不保存密码。
package mysqlruntime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
)

// bindingSecrets 已登记绑定必须恢复原完整材料，绝不通过重新生成密码修复丢失。
func (session *BindingSession) bindingSecrets(ctx context.Context, existing bool) error {
	if err := session.bindingDirectory(ctx, existing); err != nil {
		return err
	}
	record := map[string]string{}
	for _, name := range []string{"app.secret", "migration.secret"} {
		data, err := session.bindingPassword(ctx, name, existing)
		if err != nil {
			return err
		}
		digest, err := Fingerprint(string(data))
		if err != nil {
			return err
		}
		record[name] = digest
	}
	digest, err := session.bindingIntent()
	if err != nil {
		return err
	}
	record["intent"] = digest
	return session.bindingSecretRecord(ctx, record, existing)
}

// bindingDirectory 被动核实只读检查既有目录，首次执行才允许创建受限子目录。
func (session *BindingSession) bindingDirectory(ctx context.Context, existing bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	base := "projects/" + session.request.Resource.ID + "/services"
	for _, name := range []string{base, base + "/" + session.request.ServiceID} {
		var err error
		if existing {
			err = session.driver.files.CheckParents(name + "/entry")
		} else {
			err = session.driver.files.Mkdir(name)
		}
		if err != nil {
			return privateFileFailure(err)
		}
	}
	return nil
}

// bindingPassword 已登记秘密缺失时不生成替代值，合法材料始终是固定长度高熵内容。
func (session *BindingSession) bindingPassword(ctx context.Context, name string, existing bool) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := session.driver.readPrivate(session.prefix+name, 128)
	if errors.Is(err, fs.ErrNotExist) && !existing {
		random := make([]byte, 32)
		if _, err := rand.Read(random); err != nil {
			return nil, failure("DEPLOY_SECRET_UNAVAILABLE", "无法生成业务用途秘密", false, false)
		}
		data = []byte(hex.EncodeToString(random))
		if err := session.driver.writeNew(session.prefix+name, data); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, failure("DEPLOY_SECRET_MISSING", "原业务用途秘密缺失或不可安全读取，禁止重新生成", false, false)
	}
	decoded, err := hex.DecodeString(string(data))
	if err != nil || len(decoded) != 32 {
		return nil, failure("DEPLOY_SECRET_INVALID", "业务用途秘密内容不完整，禁止重置", false, false)
	}
	return data, nil
}

// bindingSecretRecord 只读逐字节核实已登记版本；不以覆盖记录修复未知或被替换材料。
func (session *BindingSession) bindingSecretRecord(ctx context.Context, record map[string]string, existing bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	current, err := session.driver.readPrivate(session.prefix+"credentials.json", 4096)
	if errors.Is(err, fs.ErrNotExist) && !existing {
		return session.driver.writeNew(session.prefix+"credentials.json", data)
	}
	if err != nil || !bytes.Equal(current, data) {
		return failure("DEPLOY_SECRET_INVALID", "业务用途秘密登记不匹配，保留原文件与数据库", false, false)
	}
	return nil
}

// ensureAccount 新账号带固定归属属性；已有账号只核实，绝不ALTER密码或认领其他Host。
func (session *BindingSession) ensureAccount(ctx context.Context, user, file string) error {
	rows, err := session.sql.query(ctx, "SELECT Host FROM mysql.user WHERE User='"+user+"'")
	if err != nil {
		return err
	}
	intent, err := session.bindingIntent()
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		password, err := session.driver.readPrivate(session.prefix+file, 128)
		if err != nil {
			return err
		}
		query := "CREATE USER " + session.accountSQL(user) + " IDENTIFIED WITH caching_sha2_password BY '" + string(password) + "' WITH MAX_USER_CONNECTIONS 16 PASSWORD EXPIRE NEVER ACCOUNT LOCK ATTRIBUTE '{\"twineBinding\":\"" + session.request.Binding.ID + "\",\"twineIntent\":\"" + intent + "\"}'"
		if _, err := session.sql.query(ctx, query); err != nil {
			return err
		}
	} else if len(rows) != 1 || rows[0] != session.request.AccountHost {
		return failure("DEPLOY_ACCOUNT_CONFLICT", "用途账号存在未登记Host，禁止接管或重置", false, false)
	}
	return session.verifyAccount(ctx, user, intent)
}

// verifyAccount 只读核实原用途账号，不在被动采集或恢复检查中创建账号。
func (session *BindingSession) verifyAccount(ctx context.Context, user, intent string) error {
	rows, err := session.sql.query(ctx, "SELECT Host FROM mysql.user WHERE User='"+user+"'")
	if err != nil {
		return err
	}
	if len(rows) != 1 || rows[0] != session.request.AccountHost {
		return failure("DEPLOY_ACCOUNT_CONFLICT", "用途账号出现额外或被替换的Host，保留原账号等待核实", false, false)
	}
	query := "SELECT CONCAT(JSON_UNQUOTE(JSON_EXTRACT(a.ATTRIBUTE,'$.twineBinding')),':',JSON_UNQUOTE(JSON_EXTRACT(a.ATTRIBUTE,'$.twineIntent')),':',u.plugin) FROM information_schema.USER_ATTRIBUTES a JOIN mysql.user u ON a.USER=u.User AND a.HOST=u.Host WHERE a.USER='" + user + "' AND a.HOST='" + session.request.AccountHost + "'"
	rows, err = session.sql.query(ctx, query)
	if err != nil {
		return err
	}
	if len(rows) != 1 || rows[0] != session.request.Binding.ID+":"+intent+":caching_sha2_password" {
		return failure("DEPLOY_ACCOUNT_CONFLICT", "用途账号归属或认证组合不匹配，禁止接管", false, false)
	}
	return nil
}
