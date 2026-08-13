package httpserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/testpilot/testpilot/internal/config"
)

// newProxyApp 仅挂反代路由的最小 app（避免 App() 全量依赖）。
func newProxyApp(copilotURL string) *fiber.App {
	s := &Server{cfg: config.Defaults()}
	s.cfg.CopilotURL = copilotURL
	app := fiber.New()
	if s.cfg.CopilotURL != "" {
		app.All("/copilot-api/*", s.proxyCopilot)
	}
	return app
}

func TestProxyCopilot(t *testing.T) {
	var gotPath, gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	app := newProxyApp(upstream.URL)
	req, _ := http.NewRequest(http.MethodGet, "/copilot-api/healthz", nil)
	req.Header.Set("Authorization", "Bearer tok-123")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	if gotPath != "/api/healthz" {
		t.Fatalf("upstream path=%q, want /api/healthz", gotPath)
	}
	if gotAuth != "Bearer tok-123" {
		t.Fatalf("auth header not forwarded: %q", gotAuth)
	}
}

func TestProxyCopilotUpstreamDown(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	dead.Close() // 立即关闭：连接被拒

	app := newProxyApp(dead.URL)
	req, _ := http.NewRequest(http.MethodGet, "/copilot-api/chat", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusBadGateway {
		t.Fatalf("status=%d, want 502", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "COPILOT_UNAVAILABLE") {
		t.Fatalf("body=%s", body)
	}
}

func TestProxyCopilotInvalidURL(t *testing.T) {
	app := newProxyApp("://bad-url")
	req, _ := http.NewRequest(http.MethodGet, "/copilot-api/chat", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusBadGateway {
		t.Fatalf("status=%d, want 502", resp.StatusCode)
	}
}
