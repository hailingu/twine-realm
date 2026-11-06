// 本文件穿过真实能力生产者与发布schema，防止默认禁用成为无效响应。
package servicebuild

import (
	"encoding/json"
	"testing"

	"github.com/hailingu/twine-realm/server/contract"
)

// TestDisabledCapabilityContract 未配置执行器是正常模式，能力仍须通过真实客户端共享契约。
func TestDisabledCapabilityContract(t *testing.T) {
	service, err := NewService(nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	value := service.Capability()
	if value.Enabled || value.Code != "BUILD_DISABLED" {
		t.Fatal("默认模式隐式启用了执行器")
	}
	schemas, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if err := schemas["BuildCapability"].Validate(document); err != nil {
		t.Fatal("真实默认能力响应不符合发布契约", err)
	}
}
