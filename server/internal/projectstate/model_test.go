// 本文件核实规范游标和用例边界，不用浮点数截断完整版本。
package projectstate

import (
	"context"
	"errors"
	"testing"

	"github.com/hailingu/twine-realm/server/internal/project"
)

const stateProjectID = "11111111-1111-4111-8111-111111111111"

// stateReader 仅模拟读取端口错误，真实事务和HTTP另有集成证据。
type stateReader struct {
	failure error
	calls   int
}

// ProjectState 返回受控持久读取结果，取消沿用调用方context。
func (reader *stateReader) ProjectState(ctx context.Context, id string) (Snapshot, error) {
	reader.calls++
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Cursor: "7"}, reader.failure
}

// ProjectEvents 返回原游标形态，不模拟真实事件落盘或重放。
func (reader *stateReader) ProjectEvents(ctx context.Context, id, after string) (Page, error) {
	reader.calls++
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	return Page{Next: after}, reader.failure
}

// TestCursorBoundary 保留最大uint64且拒绝非规范数字、空值、符号与溢出。
func TestCursorBoundary(t *testing.T) {
	for _, value := range []string{"0", "1", "9007199254740993", "18446744073709551615"} {
		if _, err := Cursor(value); err != nil {
			t.Fatal(value, err)
		}
	}
	for _, value := range []string{"", "00", "01", "-1", "+1", " 1", "1\n", "1.0", "18446744073709551616", "100000000000000000000"} {
		_, err := Cursor(value)
		var failure *project.Error
		if !errors.As(err, &failure) || failure.Code != "INVALID_REQUEST" {
			t.Fatal("非法游标被接受", value)
		}
	}
}

// TestStateServiceBoundaries 先拒绝归属与游标，再显式传递存储失败和取消。
func TestStateServiceBoundaries(t *testing.T) {
	reader := &stateReader{}
	service := NewService(reader)
	if _, err := service.Snapshot(context.Background(), "bad"); err == nil {
		t.Fatal("接受非法项目")
	}
	if _, err := service.Events(context.Background(), stateProjectID, "00"); err == nil {
		t.Fatal("接受非法游标")
	}
	if _, err := service.Events(context.Background(), "bad", "0"); err == nil || reader.calls != 0 {
		t.Fatal("无效输入触发持久读取")
	}
	value, err := service.Snapshot(context.Background(), stateProjectID)
	if err != nil || value.Cursor != "7" {
		t.Fatal("完整读取未透传")
	}
	page, err := service.Events(context.Background(), stateProjectID, "7")
	if err != nil || page.Next != "7" {
		t.Fatal("游标被重写")
	}
	reader.failure = errors.New("读取不可用")
	if _, err := service.Snapshot(context.Background(), stateProjectID); !errors.Is(err, reader.failure) {
		t.Fatal("读取失败变成成功")
	}
	if _, err := service.Events(context.Background(), stateProjectID, "7"); !errors.Is(err, reader.failure) {
		t.Fatal("事件读取失败变成空历史")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Snapshot(ctx, stateProjectID); !errors.Is(err, context.Canceled) {
		t.Fatal("取消未传播")
	}
	if _, err := service.Events(ctx, stateProjectID, "7"); !errors.Is(err, context.Canceled) {
		t.Fatal("流读取取消未传播")
	}
}
