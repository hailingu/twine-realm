// 本文件验证生命周期身份边界，不用适配器成功替代真实资源验收。
package projectrun

import "testing"

// TestCommandValidation 验证十进制版本必须使用唯一规范表示。
func TestCommandValidation(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	for _, value := range []string{"00", "+1", "-1", " 1", "18446744073709551616", ""} {
		t.Run(value, func(t *testing.T) {
			if validate(id, Command{MutationID: id, BaseRevision: value, BaseGeneration: "0", Desired: "stopped"}) == nil {
				t.Fatal("接受非规范版本")
			}
		})
	}
	if err := validate(id, Command{MutationID: id, BaseRevision: "1", BaseGeneration: "0", Desired: "stopped"}); err != nil {
		t.Fatal(err)
	}
	for _, desired := range []string{"", "restart", "deleted"} {
		if validate(id, Command{MutationID: id, BaseRevision: "1", BaseGeneration: "0", Desired: desired}) == nil {
			t.Fatal("接受未知目标")
		}
	}
}
