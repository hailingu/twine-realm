// 本文件在任何基础设施调用之前验证领域命令的拒绝边界。
package project

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
)

// TestInvalidCommands 用中文子场景验证输入失败不会产生待持久化的半成品。
func TestInvalidCommands(t *testing.T) {
	ctx := context.Background()
	service := NewService(nil, nil, nil, nil)
	const id = "00000000-0000-4000-8000-000000000001"
	for _, test := range []struct {
		name, code string
		invoke     func() (Receipt, error)
	}{
		{"空白名称", "INVALID_NAME", func() (Receipt, error) { return service.Create(ctx, CreateCommand{MutationID: id, Name: "   "}) }},
		{"过长中文名称", "INVALID_NAME", func() (Receipt, error) {
			return service.Create(ctx, CreateCommand{MutationID: id, Name: strings.Repeat("字", 81)})
		}},
		{"名称控制符", "INVALID_NAME", func() (Receipt, error) { return service.Create(ctx, CreateCommand{MutationID: id, Name: "服\n务"}) }},
		{"请求身份无效", "INVALID_REQUEST", func() (Receipt, error) { return service.Create(ctx, CreateCommand{MutationID: "bad", Name: "项目"}) }},
		{"项目身份无效", "INVALID_REQUEST", func() (Receipt, error) {
			return service.Register(ctx, "bad", RegisterCommand{MutationID: id, BaseRevision: "0"})
		}},
		{"版本无效", "INVALID_REQUEST", func() (Receipt, error) {
			return service.SaveLayout(ctx, id, LayoutCommand{MutationID: id, BaseRevision: "-1"})
		}},
		{"非法显示名", "INVALID_NAME", func() (Receipt, error) {
			return service.Register(ctx, id, RegisterCommand{MutationID: id, BaseRevision: "0", DisplayName: "服" + string(rune(0x85)) + "务"})
		}},
		{"非有限位置", "INVALID_POSITION", func() (Receipt, error) {
			return service.Register(ctx, id, RegisterCommand{MutationID: id, BaseRevision: "0", Position: Position{X: math.NaN()}})
		}},
		{"越界位置", "INVALID_POSITION", func() (Receipt, error) {
			return service.Register(ctx, id, RegisterCommand{MutationID: id, BaseRevision: "0", Position: Position{X: 1000001}})
		}},
		{"重复节点", "INVALID_POSITION", func() (Receipt, error) {
			return service.SaveLayout(ctx, id, LayoutCommand{MutationID: id, BaseRevision: "0", Positions: []NodePosition{{ServiceID: id}, {ServiceID: id}}})
		}},
		{"节点身份无效", "INVALID_POSITION", func() (Receipt, error) {
			return service.SaveLayout(ctx, id, LayoutCommand{MutationID: id, BaseRevision: "0", Positions: []NodePosition{{ServiceID: "bad"}}})
		}},
		{"节点数超限", "INVALID_REQUEST", func() (Receipt, error) {
			return service.SaveLayout(ctx, id, LayoutCommand{MutationID: id, BaseRevision: "0", Positions: make([]NodePosition, 501)})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			receipt, err := test.invoke()
			var failure *Error
			if !errors.As(err, &failure) || failure.Code != test.code {
				t.Fatalf("期望 %s，得到 %v", test.code, err)
			}
			if receipt.MutationID != "" {
				t.Fatal("拒绝请求不应生成成功回执")
			}
		})
	}
}
