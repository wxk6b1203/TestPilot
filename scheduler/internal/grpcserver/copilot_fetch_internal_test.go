package grpcserver

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// stubPublicResolve 让私网预检「相信」目标是公网，使 httptest 回环服务可用于
// happy path；openAPIDial 同步替换为按端口回连 127.0.0.1（IP 绑定后离线环境
// 到不了 TEST-NET 地址，真实连接仍全程离线）。
func stubPublicResolve(t *testing.T) {
	t.Helper()
	origLookup, origDial := lookupIP, openAPIDial
	lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("203.0.113.10")}}, nil // TEST-NET-3 文档地址
	}
	openAPIDial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		_, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		d := net.Dialer{Timeout: 2 * time.Second}
		return d.DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
	}
	t.Cleanup(func() { lookupIP, openAPIDial = origLookup, origDial })
}

// fakeHostURL 把 httptest 地址改写成假域名（端口保留）：URL host 不能是字面
// 回环 IP——加固后字面私网 IP 在预检即被拒，走域名才能覆盖解析→绑定→拨号链路。
func fakeHostURL(t *testing.T, srv *httptest.Server, path string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return "http://" + net.JoinHostPort("rebind.example", port) + path
}

func TestFetchOpenAPIURL(t *testing.T) {
	doc := `{"openapi":"3.0.3","paths":{"/h":{"get":{}}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(doc))
	}))
	defer srv.Close()

	stubPublicResolve(t)

	raw, err := fetchOpenAPIURL(fakeHostURL(t, srv, "/openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != doc {
		t.Fatalf("body=%s", raw)
	}

	// 上游非 2xx
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	if _, err := fetchOpenAPIURL(fakeHostURL(t, bad, "")); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("non-2xx should error, got %v", err)
	}
}

func TestFetchOpenAPIURLGuards(t *testing.T) {
	// 非法 scheme
	for _, u := range []string{"ftp://h/x", "file:///etc/passwd", "h/x", ""} {
		if _, err := fetchOpenAPIURL(u); err == nil {
			t.Fatalf("url %q should be rejected", u)
		}
	}
	// 环回/私网被防护拦截（字面 IP 判定无 DNS 依赖）
	for _, u := range []string{
		"http://127.0.0.1:8080/x", "http://[::1]/x", "http://10.0.0.1/x",
		"http://192.168.1.1/x", "http://169.254.169.254/latest/meta-data",
	} {
		if _, err := fetchOpenAPIURL(u); err == nil || !strings.Contains(err.Error(), "private/loopback") {
			t.Fatalf("url %q should be private/loopback rejected, got %v", u, err)
		}
	}
}

// TestFetchOpenAPIURLRejectsPrivateRedirect 回归：此前预解析私网后用默认 client
// 跟随重定向且不复查，302 到私网/云 metadata 端点直接放行。现在 CheckRedirect
// 逐跳复检，重定向目标为私网字面 IP 必须被拒（预检与连接均离线完成）。
func TestFetchOpenAPIURLRejectsPrivateRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()

	stubPublicResolve(t)

	_, err := fetchOpenAPIURL(fakeHostURL(t, srv, "/docs"))
	if err == nil || !strings.Contains(err.Error(), "private/loopback") {
		t.Fatalf("redirect to private IP must be rejected, got %v", err)
	}
}

// TestFetchOpenAPIURLDialBindsResolvedIP 回归（DNS rebinding）：预检用的是
// lookupIP 的结果，真实连接必须绑定同一次解析出的公网 IP，而不是拿 host 重新
// 拨号（重查 DNS 可能在预检后改指内网）。替换 openAPIDial 记录实际拨号地址。
func TestFetchOpenAPIURLDialBindsResolvedIP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	origLookup, origDial := lookupIP, openAPIDial
	t.Cleanup(func() { lookupIP, openAPIDial = origLookup, origDial })
	lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("203.0.113.10")}}, nil
	}
	var dialed []string
	openAPIDial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = append(dialed, addr)
		_, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		d := net.Dialer{Timeout: 2 * time.Second}
		return d.DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
	}

	if _, err := fetchOpenAPIURL(fakeHostURL(t, srv, "/docs")); err != nil {
		t.Fatal(err)
	}
	if len(dialed) == 0 {
		t.Fatal("dial never invoked")
	}
	host, _, err := net.SplitHostPort(dialed[0])
	if err != nil {
		t.Fatalf("dialed addr %q: %v", dialed[0], err)
	}
	if host != "203.0.113.10" {
		t.Fatalf("dial must bind pre-resolved public IP, dialed %q", dialed[0])
	}
}
