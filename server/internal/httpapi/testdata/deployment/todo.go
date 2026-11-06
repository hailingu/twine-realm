// 本文件是独立验收仓库的业务示例，不属于平台路由或发布工程包。
package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/go-sql-driver/mysql"
)

// todoRecord 使用客户端身份保证未知创建可以按原ID核实。
type todoRecord struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Completed bool   `json:"completed"`
}

var todoID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

const todoQueryFailure = "业务查询失败"

// WithTodo 只向导出的临时业务仓库添加示例路由，平台不内置Todo。
func WithTodo(handler *Handler, db *sql.DB) *Handler {
	handler.mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("独立 Todo 服务：/api/todos\n"))
	})
	handler.mux.HandleFunc("GET /api/todos", func(w http.ResponseWriter, r *http.Request) { listTodos(w, r, db) })
	handler.mux.HandleFunc("POST /api/todos", func(w http.ResponseWriter, r *http.Request) { createTodo(w, r, db) })
	handler.mux.HandleFunc("PUT /api/todos/{id}", func(w http.ResponseWriter, r *http.Request) { changeTodo(w, r, db, false) })
	handler.mux.HandleFunc("DELETE /api/todos/{id}", func(w http.ResponseWriter, r *http.Request) { changeTodo(w, r, db, true) })
	handler.mux.HandleFunc("GET /test/permissions", func(w http.ResponseWriter, r *http.Request) { permissionWitness(w, r, db) })
	return handler
}

// listTodos 只读业务库，空列表保持数组形状。
func listTodos(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	rows, err := db.QueryContext(r.Context(), "SELECT id,title,completed FROM todos ORDER BY id")
	if err != nil {
		http.Error(w, todoQueryFailure, 503)
		return
	}
	values := []todoRecord{}
	for rows.Next() {
		var value todoRecord
		if err := rows.Scan(&value.ID, &value.Title, &value.Completed); err != nil {
			_ = rows.Close()
			http.Error(w, todoQueryFailure, 503)
			return
		}
		values = append(values, value)
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		http.Error(w, todoQueryFailure, 503)
		return
	}
	writeJSON(w, 200, values)
}

// readTodo 限定业务输入，不开放任意SQL或无限正文。
func readTodo(w http.ResponseWriter, r *http.Request) (todoRecord, bool) {
	var value todoRecord
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || len(value.Title) == 0 || len(value.Title) > 100 || strings.TrimSpace(value.Title) != value.Title {
		http.Error(w, "Todo输入无效", 400)
		return value, false
	}
	return value, true
}

// createTodo 同ID重复不会产生第二条记录，也不会覆盖原内容。
func createTodo(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	value, ok := readTodo(w, r)
	if !ok {
		return
	}
	if !todoID.MatchString(value.ID) {
		http.Error(w, "Todo身份无效", 400)
		return
	}
	_, err := db.ExecContext(r.Context(), "INSERT INTO todos(id,title,completed) VALUES(?,?,?)", value.ID, value.Title, false)
	var duplicate *mysql.MySQLError
	if errors.As(err, &duplicate) && duplicate.Number == 1062 {
		writeJSON(w, 200, map[string]string{"id": value.ID})
		return
	}
	if err != nil {
		http.Error(w, "业务写入失败", 503)
		return
	}
	writeJSON(w, 201, map[string]string{"id": value.ID})
}

// changeTodo 使用参数化业务写入，删除仅限独立示例数据。
func changeTodo(w http.ResponseWriter, r *http.Request, db *sql.DB, remove bool) {
	id := r.PathValue("id")
	if !todoID.MatchString(id) {
		http.Error(w, "Todo身份无效", 400)
		return
	}
	var err error
	if remove {
		_, err = db.ExecContext(r.Context(), "DELETE FROM todos WHERE id=?", id)
	} else {
		value, ok := readTodo(w, r)
		if !ok {
			return
		}
		_, err = db.ExecContext(r.Context(), "UPDATE todos SET title=?,completed=? WHERE id=?", value.Title, value.Completed, id)
	}
	if err != nil {
		http.Error(w, "业务写入失败", 503)
		return
	}
	w.WriteHeader(204)
}

// permissionWitness 在临时业务进程使用真实应用身份证明SQL被拒绝，不返回密码、SQL错误原文或数据。
func permissionWitness(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	queries := []string{"CREATE TABLE forbidden_ddl(id INT)", "UPDATE schema_migrations SET dirty=dirty", "SELECT User FROM mysql.user LIMIT 1", "SELECT binding_id FROM twine_runtime_control.bindings LIMIT 1"}
	denied := 0
	for _, query := range queries {
		_, err := db.ExecContext(r.Context(), query)
		var failure *mysql.MySQLError
		if errors.As(err, &failure) && (failure.Number == 1142 || failure.Number == 1143) {
			denied++
		}
	}
	_, migration := os.Stat("/run/secrets/migration_password")
	_, root := os.Stat("/run/secrets/root_password")
	isolated := errors.Is(migration, fs.ErrNotExist) && errors.Is(root, fs.ErrNotExist) && os.Getenv("MIGRATION_USER") == "" && os.Getenv("MYSQL_ROOT_PASSWORD_FILE") == ""
	writeJSON(w, 200, map[string]any{"denied": denied, "isolated": isolated})
}
