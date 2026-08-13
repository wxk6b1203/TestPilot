package grpcserver_test

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	"github.com/testpilot/testpilot/internal/db"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"gorm.io/gorm"
)

// openTestDB 与 internal/dispatch/dispatch_test.go 同款：离线 sqlite + AutoMigrate + 种子租户。
func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), "", db.Pool{})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// bufConn 起进程内 gRPC server（bufconn，无真实端口/网络），返回客户端连接。
func bufConn(t *testing.T, register func(srv *grpc.Server)) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	register(srv)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.Stop()
		_ = lis.Close()
	})
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// waitFor 轮询直到条件成立（worker 注册/注销/负载更新均在服务端 goroutine 异步生效）。
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// copilotCtx 构造 Copilot 工具调用上下文（tenant/user 经 RequestContext 传入）。
func copilotCtx(tenant int64, user string) *commonv1.RequestContext {
	return &commonv1.RequestContext{TenantId: tenant, UserId: user, Actor: "copilot"}
}
