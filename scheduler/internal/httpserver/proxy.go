package httpserver

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
)

// proxyCopilot 把 /copilot-api/* 反向代理到 Copilot 服务（生产前端托管在
// scheduler，前端 SSE 聊天调相对路径 /copilot-api/chat；dev 由 vite proxy 处理）。
// 路径重写 /copilot-api/ → /api/；Authorization 原样透传，由 Copilot 侧经
// Scheduler /me 校验（401 语义保留在 Copilot）。
//
// FlushInterval=-1：SSE 逐 token 立即 flush，不做缓冲聚合。
func (s *Server) proxyCopilot(ctx fiber.Ctx) error {
	target, err := url.Parse(s.cfg.CopilotURL)
	if err != nil || target.Host == "" {
		return writeErr(ctx, fiber.StatusBadGateway, "invalid copilot_url")
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.Path = "/api/" + strings.TrimPrefix(pr.In.URL.Path, "/copilot-api/")
			pr.Out.URL.RawPath = ""
		},
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			w.Header().Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
			w.WriteHeader(fiber.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":{"code":"COPILOT_UNAVAILABLE","message":"copilot upstream unreachable"}}`))
		},
	}
	// 注意：SSE 长连接受 fiber read_timeout_sec 约束；生产开启该超时需大于最长会话时长。
	return adaptor.HTTPHandler(proxy)(ctx)
}
