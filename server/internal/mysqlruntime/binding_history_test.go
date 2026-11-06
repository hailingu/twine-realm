// 本文件验证迁移内容证据，不能以版本数字相同掩盖缺失、重复或被替换的up文件。
package mysqlruntime

import (
	"strings"
	"testing"
)

// TestMigrationHistoryIntegrity 只允许当前候选的完整唯一内容集合，历史不完整不能再次DDL。
func TestMigrationHistoryIntegrity(t *testing.T) {
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	expected := map[uint64]string{1: a, 2: b}
	for _, test := range []struct {
		name    string
		rows    []string
		version uint64
		want    string
		invalid bool
	}{
		{"完整", []string{"1:" + a, "2:" + b}, 2, "verified", false},
		{"缺失已应用内容", []string{"2:" + b}, 2, "missing", false},
		{"重复", []string{"1:" + a, "1:" + a, "2:" + b}, 2, "mismatch", false},
		{"未知较新版本", []string{"1:" + a, "2:" + b, "3:" + a}, 2, "mismatch", false},
		{"版本不足", []string{"1:" + a}, 1, "mismatch", false},
		{"摘要替换", []string{"1:" + b, "2:" + b}, 2, "mismatch", false},
		{"非数字版本", []string{"not-version:" + a}, 2, "mismatch", false},
		{"非完整事实", []string{"1"}, 2, "unknown", true},
		{"多余分隔", []string{"1:a:foreign"}, 2, "unknown", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := migrationIntegrity(test.rows, test.version, expected)
			if got != test.want || (err != nil) != test.invalid {
				t.Fatal("迁移内容证据被误分类", got, err)
			}
		})
	}
}
