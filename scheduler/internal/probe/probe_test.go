package probe

import (
	"context"
	"errors"
	"testing"
	"time"

	workerv1 "github.com/testpilot/testpilot/gen/worker/v1"
	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/dispatch"
)

func testCfg() Config {
	return Config{
		IdleTTL:          10 * time.Minute,
		MaxLifetime:      time.Hour,
		MaxPerWorker:     2,
		MaxPerTenant:     1,
		CmdTimeout:       2 * time.Second,
		SnapshotMaxBytes: 16 * 1024,
		EvalMaxBytes:     4 * 1024,
	}
}

// fakeWorker 注册一个假 Worker 并自动回执：open/act/snapshot → state，close → ack。
// reply=false 时模拟 Worker 消息处理挂起（用于超时路径）。
func fakeWorker(t *testing.T, d *dispatch.Dispatcher, h *Hub, id string, reply bool) *dispatch.Worker {
	t.Helper()
	w := &dispatch.Worker{
		ID:           id,
		Capabilities: []int32{3}, // CAPABILITY_PLAYWRIGHT
		Send:         make(chan *workerv1.SchedulerCommand, 8),
	}
	if err := d.Register(w); err != nil {
		t.Fatalf("register worker: %v", err)
	}
	go func() {
		for cmd := range w.Send {
			p := cmd.GetProbe()
			if p == nil || !reply {
				continue
			}
			rep := &workerv1.ProbeReply{RequestId: p.GetRequestId(), SessionId: p.GetSessionId()}
			if p.GetClose() != nil {
				rep.Payload = &workerv1.ProbeReply_Ack{Ack: &workerv1.ProbeAck{SessionId: p.GetSessionId()}}
			} else {
				rep.Payload = &workerv1.ProbeReply_State{State: &workerv1.ProbeState{
					FinalUrl: "https://aut.test/login", Title: "T", AriaSnapshot: "- button \"Sign in\"",
				}}
			}
			h.Deliver(rep)
		}
	}()
	return w
}

func mustOpen(t *testing.T, h *Hub, tenant int64, sessionID string) {
	t.Helper()
	_, _, err := h.Open(context.Background(), tenant, "u1", sessionID, "https://aut.test/login")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
}

func TestOpenRoutesAndReplies(t *testing.T) {
	d := dispatch.New(nil)
	h := New(d, testCfg())
	fakeWorker(t, d, h, "w1", true)

	state, workerID, err := h.Open(context.Background(), 7, "u1", "s1", "https://aut.test/login")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if workerID != "w1" || state.GetTitle() != "T" {
		t.Fatalf("state=%v worker=%s", state, workerID)
	}
	if h.Sessions() != 1 {
		t.Fatalf("sessions=%d", h.Sessions())
	}
}

func TestNoWorker(t *testing.T) {
	d := dispatch.New(nil)
	h := New(d, testCfg())

	_, _, err := h.Open(context.Background(), 7, "u1", "s1", "https://aut.test/x")
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != apperr.CodeProbeNoWorker {
		t.Fatalf("want PROBE_NO_WORKER, got %v", err)
	}
}

func TestTenantLimit(t *testing.T) {
	d := dispatch.New(nil)
	h := New(d, testCfg()) // MaxPerTenant=1
	fakeWorker(t, d, h, "w1", true)

	mustOpen(t, h, 7, "s1")
	_, _, err := h.Open(context.Background(), 7, "u1", "s2", "https://aut.test/x")
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != apperr.CodeProbeLimit {
		t.Fatalf("want PROBE_LIMIT, got %v", err)
	}
	// 其他租户不受影响
	mustOpen(t, h, 8, "s2")
}

func TestPerWorkerCapFallsToNextWorker(t *testing.T) {
	d := dispatch.New(nil)
	cfg := testCfg()
	cfg.MaxPerWorker = 1
	cfg.MaxPerTenant = 10
	h := New(d, cfg)
	fakeWorker(t, d, h, "w1", true)
	fakeWorker(t, d, h, "w2", true)

	mustOpen(t, h, 7, "s1")
	mustOpen(t, h, 7, "s2")
	if h.workerOfForTest("s1") == h.workerOfForTest("s2") {
		t.Fatal("second session must route to the other worker")
	}
}

func TestTimeoutWhenWorkerSilent(t *testing.T) {
	d := dispatch.New(nil)
	cfg := testCfg()
	cfg.CmdTimeout = 80 * time.Millisecond
	h := New(d, cfg)
	fakeWorker(t, d, h, "w1", false) // 不回执

	_, _, err := h.Open(context.Background(), 7, "u1", "s1", "https://aut.test/x")
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != apperr.CodeProbeTimeout {
		t.Fatalf("want PROBE_TIMEOUT, got %v", err)
	}
}

func TestCloseThenGone(t *testing.T) {
	d := dispatch.New(nil)
	h := New(d, testCfg())
	fakeWorker(t, d, h, "w1", true)

	mustOpen(t, h, 7, "s1")
	if err := h.Close(7, "s1", "user"); err != nil {
		t.Fatalf("close: %v", err)
	}
	if h.Sessions() != 0 {
		t.Fatalf("sessions after close = %d", h.Sessions())
	}
	if err := h.Close(7, "s1", "user"); err != nil { // 幂等
		t.Fatalf("idempotent close: %v", err)
	}
	if _, err := h.Snapshot(context.Background(), 7, "s1", ""); err == nil {
		t.Fatal("snapshot on closed session must fail")
	}
}

func TestWorkerDisconnectKillsSessions(t *testing.T) {
	d := dispatch.New(nil)
	h := New(d, testCfg())
	w := fakeWorker(t, d, h, "w1", true)

	mustOpen(t, h, 7, "s1")
	h.OnWorkerDisconnect("w1")
	_ = w
	if _, err := h.Snapshot(context.Background(), 7, "s1", ""); err == nil {
		t.Fatal("snapshot after worker disconnect must fail")
	}
}

func TestSweepExpiresIdleSessions(t *testing.T) {
	d := dispatch.New(nil)
	cfg := testCfg()
	cfg.IdleTTL = 5 * time.Millisecond
	h := New(d, cfg)
	fakeWorker(t, d, h, "w1", true)

	mustOpen(t, h, 7, "s1")
	time.Sleep(10 * time.Millisecond)
	h.Sweep()
	if h.Sessions() != 0 {
		t.Fatalf("sessions after sweep = %d", h.Sessions())
	}
}

func (h *Hub) workerOfForTest(sessionID string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessions[sessionID].WorkerID
}
