//go:build integration

// 本文件直接构造旧schema真实数据，不能在旧表结构上调用需要新outbox的当前写入用例。
package httpapi_test

import (
	"crypto/sha256"
	"encoding/json"
	"testing"
	"time"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// legacyProject 只产生原版项目行，不假装旧版本拥有后来加入的通知表。
func legacyProject(t *testing.T, f *fixture) project.Project {
	t.Helper()
	// 固定非微秒时间，避免宿主机时钟精度掩盖旧行与回执不一致。
	now := time.Date(2026, time.November, 6, 12, 34, 56, 123456789, time.UTC)
	value := project.Project{SchemaVersion: 1, ID: id(t), Name: "旧版本项目", Revision: "0", Nodes: []project.Node{}, CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano)}
	db := stateSQL(t, f)
	if _, err := db.Exec("INSERT INTO projects(id,name,revision,created_at,updated_at) VALUES(?,?,0,?,?)", value.ID, value.Name, now, now); err != nil {
		t.Fatal(err)
	}
	// 旧生产写入同样回读数据库；回执必须反映 DATETIME(6) 的实际持久时间。
	if err := db.QueryRow("SELECT created_at,updated_at FROM projects WHERE id=?", value.ID).Scan(&value.CreatedAt, &value.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	for _, timestamp := range []*string{&value.CreatedAt, &value.UpdatedAt} {
		persisted, err := time.Parse("2006-01-02 15:04:05", *timestamp)
		if err != nil {
			t.Fatal(err)
		}
		*timestamp = persisted.UTC().Format(time.RFC3339Nano)
	}
	return value
}

// legacyPinSource 写入原版固定commit与专用回执，升级后由当前API核实其身份和内容。
func legacyPinSource(t *testing.T, f *fixture, current project.Project, command project.PinSourceCommand) project.Receipt {
	t.Helper()
	node := current.Nodes[0]
	current.Revision = "2"
	version := project.SourceVersion{ProjectID: current.ID, ProjectRevision: current.Revision, ServiceID: node.ID, RepositoryID: node.Repository.ID, RepositoryPath: node.Repository.Path, PinnedCommit: &command.Commit}
	value := project.Receipt{MutationID: command.MutationID, Kind: "pinSource", Project: current, SourceVersion: &version}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	db := stateSQL(t, f)
	for _, statement := range []struct {
		query     string
		arguments []any
	}{
		{"UPDATE services SET pinned_commit=? WHERE id=?", []any{command.Commit, node.ID}},
		{"UPDATE projects SET revision=2 WHERE id=?", []any{current.ID}},
		{"INSERT INTO mutation_receipts(id,intent_hash,response,created_at) VALUES(?,?,?,UTC_TIMESTAMP(6))", []any{value.MutationID, hash[:], data}},
	} {
		if _, err := db.Exec(statement.query, statement.arguments...); err != nil {
			t.Fatal(err)
		}
	}
	return value
}
