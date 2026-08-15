package httpserver

import (
	"net"
	"strings"

	"github.com/gofiber/fiber/v3"
)

// metricsGuard 返回 /metrics 来源校验中间件：
//   - MetricsAllowedCIDRs 为空（默认）→ 不限制（本地开发/Prom 可达性由网络层决定）；
//   - 配置了 CIDR 列表 → 仅来源 IP 命中列表的请求放行，其余 403。
//
// 说明：fiber 默认不信任 X-Forwarded-For（需显式配置 ProxyHeader），因此此处
// 用连接来源 IP；生产反代架构下建议在反代层做同等或更强限制。
// parseMetricsCIDRs 解析配置的 CIDR 白名单（非法条目跳过）。
func parseMetricsCIDRs(cfg string) []*net.IPNet {
	var nets []*net.IPNet
	for _, cidr := range strings.Split(cfg, ",") {
		cidr = strings.TrimSpace(cidr)
		if cidr == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(cidr); err == nil {
			nets = append(nets, n)
		}
	}
	return nets
}

// ipAllowed 判断来源 IP 是否命中白名单（空白名单=全放行）。
func ipAllowed(ip net.IP, nets []*net.IPNet) bool {
	if len(nets) == 0 {
		return true
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// metricsGuard 返回 /metrics 来源校验中间件：
//   - MetricsAllowedCIDRs 为空（默认）→ 不限制（本地开发/Prom 可达性由网络层决定）；
//   - 配置了 CIDR 列表 → 仅来源 IP 命中列表的请求放行，其余 403。
//
// 说明：fiber 默认不信任 X-Forwarded-For（需显式配置 ProxyHeader），因此此处
// 用连接来源 IP；生产反代架构下建议在反代层做同等或更强限制。
func (s *Server) metricsGuard() fiber.Handler {
	if strings.TrimSpace(s.cfg.MetricsAllowedCIDRs) == "" {
		return func(c fiber.Ctx) error { return c.Next() }
	}
	nets := parseMetricsCIDRs(s.cfg.MetricsAllowedCIDRs)
	return func(c fiber.Ctx) error {
		if ipAllowed(net.ParseIP(c.IP()), nets) {
			return c.Next()
		}
		return c.Status(fiber.StatusForbidden).
			SendString(`{"error":{"code":"FORBIDDEN","message":"metrics access denied"}}`)
	}
}
