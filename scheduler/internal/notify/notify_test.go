package notify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/testpilot/testpilot/internal/db"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), "", db.Pool{})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// dingExpectedSign 按钉钉规则复算签名：HMAC-SHA256(key=secret, msg=ts+"\n"+secret) → base64。
func dingExpectedSign(ts, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "\n" + secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// feishuExpectedSign 按飞书规则复算签名：HMAC-SHA256(key=ts+"\n"+secret, msg=空) → base64。
func feishuExpectedSign(ts, secret string) string {
	mac := hmac.New(sha256.New, []byte(ts+"\n"+secret))
	mac.Write(nil)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func TestDingSign(t *testing.T) {
	t.Run("已有 query 用 & 追加且签名可复算", func(t *testing.T) {
		raw := "https://oapi.dingtalk.com/robot/send?access_token=abc"
		got := dingSign(raw, "s3cr3t")
		if !strings.HasPrefix(got, raw+"&timestamp=") {
			t.Fatalf("prefix: %s", got)
		}
		u, err := url.Parse(got)
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		ts := q.Get("timestamp")
		if ts == "" {
			t.Fatal("missing timestamp param")
		}
		ms, err := strconv.ParseInt(ts, 10, 64)
		if err != nil {
			t.Fatalf("timestamp not int: %v", err)
		}
		// timestamp 应为当前毫秒（允许 1 分钟漂移）
		if d := time.Now().UnixMilli() - ms; d < 0 || d > 60_000 {
			t.Fatalf("timestamp drift %d ms", d)
		}
		want := dingExpectedSign(ts, "s3cr3t")
		if q.Get("sign") != want {
			t.Fatalf("sign mismatch: got %q want %q", q.Get("sign"), want)
		}
		// 精确拼接：sign 经 url.QueryEscape
		wantURL := raw + "&timestamp=" + ts + "&sign=" + url.QueryEscape(want)
		if got != wantURL {
			t.Fatalf("full URL mismatch:\n got %s\nwant %s", got, wantURL)
		}
	})

	t.Run("无 query 用 ? 追加", func(t *testing.T) {
		raw := "https://example.com/hook"
		got := dingSign(raw, "k")
		if !strings.HasPrefix(got, raw+"?timestamp=") {
			t.Fatalf("prefix: %s", got)
		}
		u, err := url.Parse(got)
		if err != nil {
			t.Fatal(err)
		}
		ts := u.Query().Get("timestamp")
		if u.Query().Get("sign") != dingExpectedSign(ts, "k") {
			t.Fatal("sign mismatch")
		}
	})

	t.Run("空 secret 按实现仍加签", func(t *testing.T) {
		// dingSign 本身不判空 secret（调用方 deliver 判），空 secret 时用空 key 计算。
		got := dingSign("https://example.com/hook", "")
		u, err := url.Parse(got)
		if err != nil {
			t.Fatal(err)
		}
		ts := u.Query().Get("timestamp")
		if ts == "" {
			t.Fatal("missing timestamp")
		}
		if u.Query().Get("sign") != dingExpectedSign(ts, "") {
			t.Fatal("sign mismatch for empty secret")
		}
	})
}

func TestFeishuSign(t *testing.T) {
	secret := "fs-secret"
	ts, sign := feishuSign(secret)
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		t.Fatalf("timestamp not int: %v", err)
	}
	if d := time.Now().Unix() - sec; d < 0 || d > 60 {
		t.Fatalf("timestamp drift %d s", d)
	}
	if want := feishuExpectedSign(ts, secret); sign != want {
		t.Fatalf("sign mismatch: got %q want %q", sign, want)
	}
}

// capture 记录 httptest server 收到的请求。
type capture struct {
	mu     sync.Mutex
	bodies [][]byte
	uris   []string
}

func (c *capture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.bodies)
}

func (c *capture) last() (body, uri string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.bodies) == 0 {
		return "", ""
	}
	return string(c.bodies[len(c.bodies)-1]), c.uris[len(c.uris)-1]
}

func newCaptureServer(t *testing.T, status int) (*httptest.Server, *capture) {
	t.Helper()
	c := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.bodies = append(c.bodies, b)
		c.uris = append(c.uris, r.URL.RequestURI())
		c.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, c
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

func TestDeliverWebhook(t *testing.T) {
	srv, cap := newCaptureServer(t, http.StatusOK)
	payload := map[string]any{"event": EventRunFinished, "run_id": "123", "status": 2}
	ch := &model.NotificationChannel{Type: 1, URL: srv.URL}
	if err := deliver(ch, payload, "标题", "正文"); err != nil {
		t.Fatal(err)
	}
	if cap.count() != 1 {
		t.Fatalf("hits=%d", cap.count())
	}
	body, _ := cap.last()
	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	// webhook 直发原始 payload
	if got["event"] != EventRunFinished || got["run_id"] != "123" || got["status"] != float64(2) {
		t.Fatalf("payload mismatch: %v", got)
	}
}

func TestDeliverDingtalk(t *testing.T) {
	t.Run("无 secret 不加签", func(t *testing.T) {
		srv, cap := newCaptureServer(t, http.StatusOK)
		ch := &model.NotificationChannel{Type: 2, URL: srv.URL}
		if err := deliver(ch, map[string]any{}, "标题", "正文"); err != nil {
			t.Fatal(err)
		}
		body, uri := cap.last()
		if strings.Contains(uri, "timestamp=") || strings.Contains(uri, "sign=") {
			t.Fatalf("unexpected sign params: %s", uri)
		}
		var got struct {
			Msgtype  string            `json:"msgtype"`
			Markdown map[string]string `json:"markdown"`
		}
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatal(err)
		}
		if got.Msgtype != "markdown" {
			t.Fatalf("msgtype=%q", got.Msgtype)
		}
		if got.Markdown["title"] != "标题" || got.Markdown["text"] != "### 标题\n\n正文" {
			t.Fatalf("markdown mismatch: %v", got.Markdown)
		}
	})

	t.Run("带 secret 时 URL 含可复算 timestamp+sign", func(t *testing.T) {
		srv, cap := newCaptureServer(t, http.StatusOK)
		ch := &model.NotificationChannel{Type: 2, URL: srv.URL, Secret: "ding-secret"}
		if err := deliver(ch, map[string]any{}, "t", "x"); err != nil {
			t.Fatal(err)
		}
		_, uri := cap.last()
		u, err := url.Parse(srv.URL + uri)
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		ts := q.Get("timestamp")
		if ts == "" {
			t.Fatalf("missing timestamp in %s", uri)
		}
		if q.Get("sign") != dingExpectedSign(ts, "ding-secret") {
			t.Fatalf("sign mismatch in %s", uri)
		}
		// body 仍为 markdown 结构
		body, _ := cap.last()
		if !strings.Contains(body, `"msgtype":"markdown"`) {
			t.Fatalf("body: %s", body)
		}
	})
}

func TestDeliverFeishu(t *testing.T) {
	t.Run("无 secret body 无 timestamp/sign", func(t *testing.T) {
		srv, cap := newCaptureServer(t, http.StatusOK)
		ch := &model.NotificationChannel{Type: 3, URL: srv.URL}
		if err := deliver(ch, map[string]any{}, "标题", "正文"); err != nil {
			t.Fatal(err)
		}
		body, _ := cap.last()
		var got map[string]any
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatal(err)
		}
		if got["msg_type"] != "text" {
			t.Fatalf("msg_type=%v", got["msg_type"])
		}
		content, ok := got["content"].(map[string]any)
		if !ok || content["text"] != "标题\n正文" {
			t.Fatalf("content mismatch: %v", got["content"])
		}
		if _, has := got["timestamp"]; has {
			t.Fatal("unexpected timestamp")
		}
		if _, has := got["sign"]; has {
			t.Fatal("unexpected sign")
		}
	})

	t.Run("带 secret body 含可复算 timestamp/sign", func(t *testing.T) {
		srv, cap := newCaptureServer(t, http.StatusOK)
		ch := &model.NotificationChannel{Type: 3, URL: srv.URL, Secret: "fs-secret"}
		if err := deliver(ch, map[string]any{}, "标题", "正文"); err != nil {
			t.Fatal(err)
		}
		body, _ := cap.last()
		var got map[string]any
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatal(err)
		}
		ts, ok := got["timestamp"].(string)
		if !ok || ts == "" {
			t.Fatalf("timestamp: %v", got["timestamp"])
		}
		sign, ok := got["sign"].(string)
		if !ok {
			t.Fatalf("sign: %v", got["sign"])
		}
		if sign != feishuExpectedSign(ts, "fs-secret") {
			t.Fatalf("sign mismatch: %s", sign)
		}
		if got["msg_type"] != "text" {
			t.Fatalf("msg_type=%v", got["msg_type"])
		}
	})
}

func TestDeliverNon2xx(t *testing.T) {
	srv, cap := newCaptureServer(t, http.StatusInternalServerError)
	ch := &model.NotificationChannel{Type: 1, URL: srv.URL}
	err := deliver(ch, map[string]any{"a": 1}, "t", "x")
	if err == nil {
		t.Fatal("expected error for 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("err=%v", err)
	}
	if cap.count() != 1 {
		t.Fatalf("hits=%d", cap.count())
	}
}

func TestDeliverUnknownType(t *testing.T) {
	srv, cap := newCaptureServer(t, http.StatusOK)
	ch := &model.NotificationChannel{Type: 9, URL: srv.URL}
	err := deliver(ch, map[string]any{}, "t", "x")
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
	if !strings.Contains(err.Error(), "unknown channel type") {
		t.Fatalf("err=%v", err)
	}
	if cap.count() != 0 {
		t.Fatalf("should not send request, hits=%d", cap.count())
	}
}

func addChannel(t *testing.T, d *gorm.DB, tenantID int64, name, url, events string, enabled bool) {
	t.Helper()
	ch := model.NotificationChannel{
		ID: model.NextID(), TenantID: tenantID, Name: name,
		Type: 1, URL: url, Events: events, Enabled: enabled,
	}
	if err := d.Create(&ch).Error; err != nil {
		t.Fatal(err)
	}
}

func TestRunFinishedDeliveries(t *testing.T) {
	d := openTestDB(t)
	srvA, capA := newCaptureServer(t, http.StatusOK) // 订阅 run_finished
	srvB, capB := newCaptureServer(t, http.StatusOK) // 只订阅 stress_finished
	srvC, capC := newCaptureServer(t, http.StatusOK) // 其他租户
	srvD, capD := newCaptureServer(t, http.StatusOK) // 订阅但 disabled

	now := time.Now()
	run := model.TestRun{
		ID: model.NextID(), TenantID: 1, PlanID: 42, Status: 2,
		TriggeredBy: "tester", Summary: model.JSON(`{"total":1,"passed":1}`),
		StartedAt: now, FinishedAt: &now,
	}
	if err := d.Create(&run).Error; err != nil {
		t.Fatal(err)
	}

	addChannel(t, d, 1, "a", srvA.URL, "run_finished", true)
	addChannel(t, d, 1, "b", srvB.URL, "stress_finished", true)
	addChannel(t, d, 2, "c", srvC.URL, "run_finished", true)
	addChannel(t, d, 1, "d", srvD.URL, "run_finished", false)

	RunFinished(d, run.ID)

	// 发送是异步的：等 A 收到
	waitUntil(t, 2*time.Second, func() bool { return capA.count() == 1 })
	// 给其他（不该有的）投递留出时间窗
	time.Sleep(300 * time.Millisecond)

	if n := capA.count(); n != 1 {
		t.Fatalf("A hits=%d", n)
	}
	if n := capB.count(); n != 0 {
		t.Fatalf("B(stress only) hits=%d", n)
	}
	if n := capC.count(); n != 0 {
		t.Fatalf("C(other tenant) hits=%d", n)
	}
	if n := capD.count(); n != 0 {
		t.Fatalf("D(disabled) hits=%d", n)
	}

	body, _ := capA.last()
	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if got["event"] != EventRunFinished {
		t.Fatalf("event=%v", got["event"])
	}
	if got["run_id"] != strconv.FormatInt(run.ID, 10) {
		t.Fatalf("run_id=%v", got["run_id"])
	}
	if got["plan_id"] != "42" {
		t.Fatalf("plan_id=%v", got["plan_id"])
	}
	if got["status"] != float64(2) {
		t.Fatalf("status=%v", got["status"])
	}
	if got["triggered_by"] != "tester" {
		t.Fatalf("triggered_by=%v", got["triggered_by"])
	}
	summary, ok := got["summary"].(map[string]any)
	if !ok || summary["passed"] != float64(1) {
		t.Fatalf("summary=%v", got["summary"])
	}
}

func TestRunFinishedNoSubscription(t *testing.T) {
	d := openTestDB(t)
	srv, cap := newCaptureServer(t, http.StatusOK)
	// events 为空 = 未订阅任何事件
	addChannel(t, d, 1, "none", srv.URL, "", true)

	now := time.Now()
	run := model.TestRun{ID: model.NextID(), TenantID: 1, PlanID: 1, Status: 3, StartedAt: now, FinishedAt: &now}
	if err := d.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	RunFinished(d, run.ID)
	time.Sleep(300 * time.Millisecond)
	if n := cap.count(); n != 0 {
		t.Fatalf("hits=%d", n)
	}
}

func TestRunFinishedMissingRun(t *testing.T) {
	d := openTestDB(t)
	srv, cap := newCaptureServer(t, http.StatusOK)
	addChannel(t, d, 1, "a", srv.URL, "run_finished", true)
	// run 不存在：应静默返回、不发请求、不 panic
	RunFinished(d, model.NextID())
	time.Sleep(200 * time.Millisecond)
	if n := cap.count(); n != 0 {
		t.Fatalf("hits=%d", n)
	}
}

func TestEventSubscribed(t *testing.T) {
	cases := []struct {
		events, event string
		want          bool
	}{
		{"run_finished", "run_finished", true},
		{"run_finished,stress_finished", "stress_finished", true},
		{"run_finished,stress_finished", "run_finished", true},
		{" stress_finished , run_finished ", "run_finished", true}, // 空格容忍
		{"", "run_finished", false},
		{"run_finished", "stress_finished", false},
		// 子串误配回归：含 "run_finished" 子串但非精确匹配的事件名不得命中
		{"xrun_finished", "run_finished", false},
		{"run_finished_old", "run_finished", false},
		{"stress_finished_v2", "stress_finished", false},
	}
	for _, c := range cases {
		if got := eventSubscribed(c.events, c.event); got != c.want {
			t.Fatalf("eventSubscribed(%q,%q)=%v want %v", c.events, c.event, got, c.want)
		}
	}
}
