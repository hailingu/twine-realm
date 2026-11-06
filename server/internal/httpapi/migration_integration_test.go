//go:build integration

// 本文件只在门禁独占的临时 MySQL 中验证旧版本升级，不操作用户数据库。
package httpapi_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
	runners := ownedMigrationRunners(t, dsn)
	t.Cleanup(func() { restoreMigrationRunners(t, runners) })
	for index := len(runners) - 1; index > 0; index-- {
		if err := runners[index].Down(); err != nil && err != migrate.ErrNoChange {
			t.Fatal(err)
		}
	}
	return runners[0]
}

// ownedMigrationRunners 按核心、运行、构建依赖顺序创建真实迁移器，失败保留原错误。
func ownedMigrationRunners(t *testing.T, dsn string) []*migrate.Migrate {
	t.Helper()
	runners := []*migrate.Migrate{}
	for _, spec := range []struct{ directory, table string }{{"migrations", ""}, {"runtime-migrations", "runtime_schema_migrations"}, {"build-migrations", "build_schema_migrations"}, {"deployment-migrations", "deployment_schema_migrations"}, {"state-migrations", "state_schema_migrations"}} {
		runner, err := newMigrationRunner(dsn, spec.directory, spec.table)
		if err != nil {
			closeMigrationRunners(t, runners)
			t.Fatal(err)
		}
		runners = append(runners, runner)
	}
	return runners
}

// restoreMigrationRunners 不清dirty、不禁外键，依赖顺序应用真实升级后关闭。
func restoreMigrationRunners(t *testing.T, runners []*migrate.Migrate) {
	t.Helper()
	for _, runner := range runners {
		if err := runner.Up(); err != nil && err != migrate.ErrNoChange {
			t.Error(err)
		}
	}
	closeMigrationRunners(t, runners)
}

// closeMigrationRunners 同时报告source与数据库关闭错误，不覆盖迁移失败。
func closeMigrationRunners(t *testing.T, runners []*migrate.Migrate) {
	t.Helper()
	for index := len(runners) - 1; index >= 0; index-- {
		source, database := runners[index].Close()
		if err := errors.Join(source, database); err != nil {
			t.Error(err)
		}
	}
}

// newMigrationRunner 在本次临时库按依赖顺序构造历史；不暴露产品回退入口。
func newMigrationRunner(dsn, path, table string) (*migrate.Migrate, error) {
	configuration, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, errors.New("临时数据库配置无效")
	}
	configuration.MultiStatements = true
	db, err := sql.Open("mysql", configuration.FormatDSN())
	if err != nil {
		return nil, errors.New("打开临时迁移连接失败")
	}
	source, err := iofs.New(os.DirFS("../store"), path)
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	driver, err := migratemysql.WithInstance(db, &migratemysql.Config{MigrationsTable: table})
	if err != nil {
		return nil, errors.Join(err, source.Close(), db.Close())
	}
	runner, err := migrate.NewWithInstance("iofs", source, "mysql", driver)
	if err != nil {
		return nil, errors.Join(err, source.Close(), driver.Close())
	}
	return runner, nil
}

// TestLegacyProjectMigration 验证旧登记、坐标、版本、原始回执和 null 固定值均能恢复。
func TestLegacyProjectMigration(t *testing.T) {
	f := openFixture(t)
	runner := migrationRunner(t, f.dsn)
	if err := runner.Migrate(1); err != nil {
		t.Fatal(err)
	}
	registered := legacyRegistration(t, f)
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
	canvas := decode[project.CanvasSnapshot](t, call(t, f.handler, "GET", canvasURL(opened), nil), 200, "CanvasSnapshot")
	if len(canvas.Canvas.Middlewares) != 0 || !reflect.DeepEqual(canvas.Project, opened) {
		t.Fatal("旧项目升级后出现伪造组件或服务丢失")
	}
}

// legacyRegistration 直接生成 v1 真实行和回执，避免用新代码假装历史版本行为。
func legacyRegistration(t *testing.T, f *fixture) project.Receipt {
	t.Helper()
	current := legacyProject(t, f)
	path, err := filepath.EvalSymlinks(repository(t, f.root, "legacy"))
	if err != nil {
		t.Fatal(err)
	}
	sourceGit(t, path, "-c", "user.name=测试", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "旧源码")
	commit := sourceGit(t, path, "rev-parse", "HEAD")
	repositoryID, serviceID := id(t), id(t)
	current.Revision = "1"
	current.Nodes = []project.Node{{ID: serviceID, DisplayName: "旧版本服务", Repository: project.Repository{ID: repositoryID, Path: path, HeadCommit: &commit}, Position: project.Position{X: 20, Y: 30}}}
	value := project.Receipt{MutationID: id(t), Kind: "registerRepository", Project: current}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("mysql", f.dsn)
	if err != nil {
		t.Fatal("打开临时旧版本夹具失败")
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	pathHash := sha256.Sum256([]byte(path))
	hash := sha256.Sum256(data)
	statements := []struct {
		query     string
		arguments []any
	}{
		{"UPDATE projects SET revision=1 WHERE id=?", []any{current.ID}},
		{"INSERT INTO repositories(id,path,path_hash) VALUES(?,?,?)", []any{repositoryID, path, pathHash[:]}},
		{"INSERT INTO services(id,project_id,repository_id,display_name,head_commit,position_x,position_y) VALUES(?,?,?,?,?,?,?)", []any{serviceID, current.ID, repositoryID, "旧版本服务", commit, 20, 30}},
		{"INSERT INTO mutation_receipts(id,intent_hash,response,created_at) VALUES(?,?,?,UTC_TIMESTAMP(6))", []any{value.MutationID, hash[:], data}},
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement.query, statement.arguments...); err != nil {
			t.Fatal(err)
		}
	}
	return value
}

// TestPinnedSourceMigration 验证中间件迁移不会抹除 Issue #2 的固定 commit 与专用回执。
func TestPinnedSourceMigration(t *testing.T) {
	f := openFixture(t)
	runner := migrationRunner(t, f.dsn)
	if err := runner.Migrate(2); err != nil {
		t.Fatal(err)
	}
	registered := legacyRegistration(t, f)
	command := project.PinSourceCommand{MutationID: id(t), BaseRevision: "1", Commit: *registered.Project.Nodes[0].Repository.HeadCommit}
	saved := legacyPinSource(t, f, registered.Project, command)
	if err := runner.Up(); err != nil {
		t.Fatal(err)
	}
	if err := f.database.Close(); err != nil {
		t.Fatal(err)
	}
	f.reopen(t)
	value := decode[project.SourceObservation](t, call(t, f.handler, "GET", sourceURL(registered.Project), nil), 200, "SourceObservation")
	if !reflect.DeepEqual(&value.Version, saved.SourceVersion) {
		t.Fatal("升级丢失固定源码")
	}
	replay := decode[project.Receipt](t, call(t, f.handler, "GET", "/api/v1/source-mutations/"+command.MutationID, nil), 200, "SourceReceipt")
	if !replay.Replayed || !reflect.DeepEqual(replay.SourceVersion, saved.SourceVersion) {
		t.Fatal("升级改变了原源码回执")
	}
}
