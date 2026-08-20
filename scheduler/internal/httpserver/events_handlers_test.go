package httpserver

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/testpilot/testpilot/internal/auth"
	"github.com/testpilot/testpilot/internal/config"
	"github.com/testpilot/testpilot/internal/dispatch"
	"github.com/testpilot/testpilot/internal/events"
	"github.com/testpilot/testpilot/internal/model"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

func TestEventsStreamPushesAndCloses(t *testing.T) {
	oldHeartbeat := sseHeartbeatInterval
	sseHeartbeatInterval = 200 * time.Millisecond
	defer func() { sseHeartbeatInterval = oldHeartbeat }()

	cfg := config.Defaults()
	_, d := newTestApp(t, cfg)
	disp := dispatch.New(d)
	app := New(d, cfg, disp, nil, nil, nil).App()

	project := model.Project{ID: model.NextID(), TenantID: 1, Name: "p"}
	if err := d.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	run := model.TestRun{ID: model.NextID(), TenantID: 1, PlanID: 0, Status: 1, StartedAt: time.Now()}
	if err := d.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	tok, _ := auth.IssueToken(cfg.JWTSecret, 1, 1, 1, 1)

	// 用真实 fasthttp 服务验证流式响应（app.Test 会等待流结束）。
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: app.Handler()}
	go srv.Serve(ln)
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return ln.Dial()
		},
	}}
	req, _ := http.NewRequestWithContext(context.Background(), "GET",
		fmt.Sprintf("http://test/api/v1/events?channels=run:%d", run.ID), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	got := make(chan string, 1)
	go func() {
		r := bufio.NewReader(resp.Body)
		var sb strings.Builder
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				break
			}
			if strings.HasPrefix(line, ":") {
				continue // 忽略 SSE heartbeat
			}
			sb.WriteString(line)
			if strings.HasSuffix(sb.String(), "\n\n") {
				break
			}
		}
		got <- sb.String()
	}()

	// 等流式 handler 建立订阅
	deadline := time.Now().Add(3 * time.Second)
	for disp.Events().SubscriberCount(fmt.Sprintf("run:%d", run.ID)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("SSE subscription was not established")
		}
		time.Sleep(10 * time.Millisecond)
	}
	disp.Events().Publish(fmt.Sprintf("run:%d", run.ID), events.Event{
		Type: "step_progress",
		Data: map[string]any{"run_id": fmt.Sprint(run.ID), "step_path": "steps[0]", "status": 1},
	})

	select {
	case body := <-got:
		if !strings.Contains(body, "event: step_progress") ||
			!strings.Contains(body, `"step_path":"steps[0]"`) {
			t.Fatalf("body=%q", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no SSE event")
	}
}

func TestEventsStreamChannelValidation(t *testing.T) {
	cfg := config.Defaults()
	_, d := newTestApp(t, cfg)
	disp := dispatch.New(d)
	app := New(d, cfg, disp, nil, nil, nil).App()

	project := model.Project{ID: model.NextID(), TenantID: 1, Name: "p"}
	if err := d.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	// 租户 2 请求租户 1 的 project channel → 404
	tok2, _ := auth.IssueToken(cfg.JWTSecret, 1, 2, 1, 1)
	req := httptest.NewRequest("GET",
		fmt.Sprintf("/api/v1/events?channels=project:%d", project.ID), nil)
	req.Header.Set("Authorization", "Bearer "+tok2)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("tenant isolation status=%d, want 404", resp.StatusCode)
	}

	// 缺少 channels → 400
	tok1, _ := auth.IssueToken(cfg.JWTSecret, 1, 1, 1, 1)
	req = httptest.NewRequest("GET", "/api/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+tok1)
	resp2, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 400 {
		t.Fatalf("missing channels status=%d, want 400", resp2.StatusCode)
	}
}
