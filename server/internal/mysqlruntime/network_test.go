// 本文件覆盖实际Docker写入的IP族参数，不允许用兼容默认值接受外来或漂移网络。
package mysqlruntime

import "testing"

// TestNetworkDefaultsRemainStrict 只接受已固定IPv4私网语义及完整归属。
func TestNetworkDefaultsRemainStrict(t *testing.T) {
	driver, _ := openPrivateFixture(t)
	resource := testResource(t, driver)
	value := networkState{Name: resource.Network, Driver: "bridge", Scope: "local", Internal: true, EnableIPv4: true, Labels: labels(resource), Options: map[string]string{"com.docker.network.enable_ipv4": "true", "com.docker.network.enable_ipv6": "false"}}
	if !validNetworkState(resource, value) {
		t.Fatal("真实Docker的固定默认参数被拒绝")
	}
	for _, scenario := range []struct {
		name   string
		change func(*networkState)
	}{
		{"外来归属", func(value *networkState) { value.Labels = map[string]string{} }},
		{"额外驱动选项", func(value *networkState) {
			value.Options = map[string]string{"com.docker.network.bridge.name": "foreign"}
		}},
		{"改变IP族", func(value *networkState) { value.Options = map[string]string{"com.docker.network.enable_ipv6": "true"} }},
		{"实际IPv6开启", func(value *networkState) { value.EnableIPv6 = true }},
		{"实际IPv4关闭", func(value *networkState) { value.EnableIPv4 = false }},
		{"非内部网络", func(value *networkState) { value.Internal = false }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			changed := value
			scenario.change(&changed)
			if validNetworkState(resource, changed) {
				t.Fatal("不符固定计划的网络被接受")
			}
		})
	}
}
