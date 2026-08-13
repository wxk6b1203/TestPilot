package grpcserver

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchOpenAPIURL(t *testing.T) {
	doc := `{"openapi":"3.0.3","paths":{"/h":{"get":{}}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(doc))
	}))
	defer srv.Close()

	// 私网防护经 lookupIP 判定：替换为"声称公网"，使 httptest 回环可用于 happy path
	// （真实连接仍走 URL 里的 127.0.0.1，全程离线）。
	orig := lookupIP
	lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("203.0.113.10")}}, nil // TEST-NET-3 文档地址
	}
	defer func() { lookupIP = orig }()

	raw, err := fetchOpenAPIURL(srv.URL + "/openapi.json")
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
	if _, err := fetchOpenAPIURL(bad.URL); err == nil || !strings.Contains(err.Error(), "500") {
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
	// 环回/私网被防护拦截（字面 IP 解析无 DNS 依赖）
	for _, u := range []string{
		"http://127.0.0.1:8080/x", "http://[::1]/x", "http://10.0.0.1/x",
		"http://192.168.1.1/x", "http://169.254.169.254/latest/meta-data",
	} {
		if _, err := fetchOpenAPIURL(u); err == nil || !strings.Contains(err.Error(), "private/loopback") {
			t.Fatalf("url %q should be private/loopback rejected, got %v", u, err)
		}
	}
}
