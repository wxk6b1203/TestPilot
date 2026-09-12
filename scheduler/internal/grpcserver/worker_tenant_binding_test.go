package grpcserver_test

import (
	"context"
	"net"
	"testing"
	"time"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"

	workerv1 "github.com/testpilot/testpilot/gen/worker/v1"
	"github.com/testpilot/testpilot/internal/dispatch"
	"github.com/testpilot/testpilot/internal/grpcserver"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestParseWorkerTenantTokens(t *testing.T) {
	if got, err := grpcserver.ParseWorkerTenantTokens(""); err != nil || got != nil {
		t.Fatalf("empty spec → nil,nil, got %v,%v", got, err)
	}
	got, err := grpcserver.ParseWorkerTenantTokens(" 7=tok-a , 8=tok-b ")
	if err != nil || got[7] != "tok-a" || got[8] != "tok-b" || len(got) != 2 {
		t.Fatalf("parse got=%v err=%v", got, err)
	}
	for _, bad := range []string{"7", "7=", "=tok", "x=tok", "0=tok", "-1=tok"} {
		if _, err := grpcserver.ParseWorkerTenantTokens(bad); err == nil {
			t.Fatalf("spec %q should fail", bad)
		}
	}
}

// 租户令牌绑定：声明 tenant_id 的 Worker 必须出示该租户令牌；共享令牌只能
// 注册 tenant_id=0（全租户共享 Worker）；未映射租户拒绝（fail-closed）。
func TestWorkerTenantTokenBinding(t *testing.T) {
	const shared = "shared-token"
	tenantTokens, err := grpcserver.ParseWorkerTenantTokens("7=tok-tenant7,8=tok-tenant8")
	if err != nil {
		t.Fatal(err)
	}

	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(grpc.ChainStreamInterceptor(
		grpcserver.WorkerAuthStream(shared, tenantTokens)))
	disp := dispatch.New(openTestDB(t))
	svc := grpcserver.NewWorkerService(disp, nil)
	svc.SharedToken = shared
	svc.TenantTokens = tenantTokens
	workerv1.RegisterWorkerServiceServer(srv, svc)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	cli := workerv1.NewWorkerServiceClient(conn)

	tryRegister := func(workerID, token string, tenant int64) error {
		ctx := metadata.AppendToOutgoingContext(context.Background(), "x-worker-token", token)
		stream, err := cli.Connect(ctx)
		if err != nil {
			return err
		}
		if err := stream.Send(registerEvent(&workerv1.RegisterRequest{
			WorkerId: workerID, TenantId: tenant,
			Capabilities: []commonv1.Capability{},
		})); err != nil {
			return err
		}
		// 拒绝路径：服务端关闭流返回 status；放行路径：Recv 无限期阻塞 → 交给
		// waitFor 查注册表判定成功后由 Cleanup 收尾。
		errC := make(chan error, 1)
		go func() {
			_, err := stream.Recv()
			errC <- err
		}()
		select {
		case err := <-errC:
			return err
		case <-time.After(2 * time.Second):
			_ = stream.CloseSend()
			return nil
		}
	}

	// 租户令牌匹配 → 注册成功
	if err := tryRegister("w7", "tok-tenant7", 7); err != nil {
		t.Fatalf("tenant7 with own token: %v", err)
	}
	waitFor(t, "tenant7 worker", func() bool { return findWorker(disp, "w7") != nil })

	// 共享令牌冒充租户 7 → 拒绝
	err = tryRegister("w7-impersonated", shared, 7)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("shared token for tenant 7: want PermissionDenied, got %v", err)
	}

	// 未映射租户（自带不存在的令牌组合）→ 拒绝
	err = tryRegister("w9", "tok-tenant8", 9)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("unmapped tenant 9: want PermissionDenied, got %v", err)
	}

	// tenant_id=0（共享 Worker）+ 共享令牌 → 成功
	if err := tryRegister("w0", shared, 0); err != nil {
		t.Fatalf("shared worker with shared token: %v", err)
	}
	waitFor(t, "shared worker", func() bool { return findWorker(disp, "w0") != nil })

	// 完全未知的令牌 → 流拦截器 Unauthenticated
	err = tryRegister("wx", "no-such-token", 7)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unknown token: want Unauthenticated, got %v", err)
	}
}
