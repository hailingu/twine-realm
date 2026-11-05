//go:build integration

// 本文件只在门禁独占的临时 MySQL 中验证实际 v1 到 v2 迁移，不操作用户数据库。
package httpapi_test

import (
	"database/sql"
	"os"
	"reflect"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	migratemysql "github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/hailingu/twine-realm/server/internal/project"
)

// migrationRunner 使用与平台完全相同的版本化 SQL，连接关闭由迁移器统一负责。
func migrationRunner(t *testing.T, dsn string) *migrate.Migrate {
	t.Helper()
	configuration, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("临时数据库配置无效")
	}
	configuration.MultiStatements = true
	db, err := sql.Open("mysql", configuration.FormatDSN())
	if err != nil {
		t.Fatal("打开临时迁移连接失败")
	}
	source, err := iofs.New(os.DirFS("../store"), "migrations")
	if err != nil {
		t.Fatal(err)
	}
	driver, err := migratemysql.WithInstance(db, &migratemysql.Config{})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := migrate.NewWithInstance("iofs", source, "mysql", driver)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runner.Up(); err != nil && err != migrate.ErrNoChange {
			t.Error(err)
		}
		sourceErr, databaseErr := runner.Close()
		if sourceErr != nil {
			t.Error(sourceErr)
		}
		if databaseErr != nil {
			t.Error(databaseErr)
		}
	})
	return runner
}

// TestLegacyProjectMigration 验证旧登记、坐标、版本、原始回执和 null 固定值均能恢复。
func TestLegacyProjectMigration(t *testing.T) {
	f := openFixture(t)
	runner := migrationRunner(t, f.dsn)
	if err := runner.Steps(-1); err != nil {
		t.Fatal(err)
	}
	registered, _ := registerSource(t, f, true)
	if err := runner.Up(); err != nil {
		t.Fatal(err)
	}
	if err := f.database.Close(); err != nil {
		t.Fatal(err)
	}
	f.reopen(t)
	opened := decode[project.Project](t, call(t, f.handler, "GET", "/api/v1/projects/"+registered.Project.ID, nil), 200, "Project")
	if !reflect.DeepEqual(opened, registered.Project) {
		t.Fatal("迁移改变了旧项目实体或坐标")
	}
	oldReceipt := decode[project.Receipt](t, call(t, f.handler, "GET", "/api/v1/mutations/"+registered.MutationID, nil), 200, "Receipt")
	if !oldReceipt.Replayed || !reflect.DeepEqual(oldReceipt.Project, registered.Project) || oldReceipt.SourceVersion != nil {
		t.Fatal("迁移改变了既有回执形状或结果")
	}
	value := decode[project.SourceObservation](t, call(t, f.handler, "GET", sourceURL(opened), nil), 200, "SourceObservation")
	if value.Version.PinnedCommit != nil || value.Version.RepositoryID != opened.Nodes[0].Repository.ID || value.Version.ProjectRevision != opened.Revision {
		t.Fatal("迁移伪造固定值或丢失稳定关联")
	}
}
