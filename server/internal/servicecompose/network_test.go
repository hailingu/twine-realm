// 本文件验证入口只属于原服务，并拒绝有歧义的Docker名单。
package servicecompose

import (
	"strings"
	"testing"
)

// TestIngressOwnership 外来成员、主机路由和公开默认发布不能冒充本机服务入口。
func TestIngressOwnership(t *testing.T) {
	p := structuralPlan(t)
	for _, test := range []struct {
		name   string
		change func(*ingressNetwork)
	}{
		{"归属丢失", func(v *ingressNetwork) { v.Labels = nil }},
		{"同名外来", func(v *ingressNetwork) { v.ID = "foreign" }},
		{"私网冒充入口", func(v *ingressNetwork) { v.Internal = true }},
		{"公开默认发布", func(v *ingressNetwork) { v.Options["com.docker.network.bridge.host_binding_ipv4"] = "0.0.0.0" }},
		{"未知路由选项", func(v *ingressNetwork) { v.Options["com.docker.network.bridge.gateway_mode_ipv4"] = "routed" }},
		{"外来成员", func(v *ingressNetwork) {
			v.Containers[strings.Repeat("c", 64)] = struct{ Name string }{Name: "foreign"}
		}},
		{"多个成员", func(v *ingressNetwork) {
			v.Containers[strings.Repeat("b", 64)] = struct{ Name string }{Name: p.Resources.Application}
			v.Containers[strings.Repeat("c", 64)] = struct{ Name string }{Name: p.Resources.Application}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			v := ingressNetwork{ID: strings.Repeat("a", 64), Name: p.Resources.IngressNetwork, Driver: "bridge", Scope: "local", EnableIPv4: true, Labels: labels(p, "ingress"), Options: map[string]string{"com.docker.network.bridge.host_binding_ipv4": "127.0.0.1"}, Containers: map[string]struct{ Name string }{}}
			if err := validateIngress(p, v); err != nil {
				t.Fatal("合法入口被拒绝", err)
			}
			v.Containers[strings.Repeat("b", 64)] = struct{ Name string }{Name: p.Resources.Application}
			if err := validateIngress(p, v); err != nil {
				t.Fatal("原应用连接被拒绝", err)
			}
			test.change(&v)
			if err := validateIngress(p, v); err == nil {
				t.Fatal("入口漂移被接管")
			}
		})
	}
}

// TestDockerListingAmbiguity 过滤的相似名称不是原对象，重复或截断结果不可作为存在证明。
func TestDockerListingAmbiguity(t *testing.T) {
	for _, test := range []struct {
		data, kind string
		invalid    bool
	}{
		{"", "container", false}, {"{\"Names\":\"original\"}", "container", false},
		{"{\"Name\":\"original\"}", "network", false}, {"{", "container", true},
		{"{}", "network", true}, {strings.Repeat("{\"Names\":\"original\"}\n", 501), "container", true},
	} {
		_, err := listedNames([]byte(test.data), test.kind)
		if (err != nil) != test.invalid {
			t.Fatal("名单边界未核实", err)
		}
	}
	for _, test := range []struct {
		names          []string
		found, invalid bool
	}{
		{nil, false, false}, {[]string{"original-extra"}, false, false},
		{[]string{"original", "original-extra"}, true, false}, {[]string{"original", "original"}, false, true},
	} {
		found, err := exactListedName(test.names, "original")
		if found != test.found || (err != nil) != test.invalid {
			t.Fatal("原名称未唯一核实", found, err)
		}
	}
}
