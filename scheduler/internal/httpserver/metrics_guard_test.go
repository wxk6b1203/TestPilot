package httpserver

import (
	"net"
	"testing"
)

func TestMetricsIPAllowed(t *testing.T) {
	nets := parseMetricsCIDRs("10.0.0.0/8, 192.168.0.0/16, bad-cidr")
	if len(nets) != 2 {
		t.Fatalf("want 2 valid nets (bad-cidr skipped), got %d", len(nets))
	}
	// 命中
	for _, ip := range []string{"10.1.2.3", "192.168.1.1"} {
		if !ipAllowed(net.ParseIP(ip), nets) {
			t.Fatalf("%s should be allowed", ip)
		}
	}
	// 未命中
	for _, ip := range []string{"203.0.113.9", "8.8.8.8", "172.16.0.1"} {
		if ipAllowed(net.ParseIP(ip), nets) {
			t.Fatalf("%s should be denied", ip)
		}
	}
	// 空白名单 = 全放行（默认行为）
	if !ipAllowed(net.ParseIP("127.0.0.1"), nil) {
		t.Fatal("empty whitelist should allow all")
	}
	// 非法 IP
	if ipAllowed(nil, nets) {
		t.Fatal("nil ip should be denied when whitelist set")
	}
}
