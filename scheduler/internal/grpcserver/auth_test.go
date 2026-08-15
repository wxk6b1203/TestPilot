package grpcserver_test

import (
	"context"
	"testing"

	copilotv1 "github.com/testpilot/testpilot/gen/copilot/v1"
	"github.com/testpilot/testpilot/internal/auth"
	"github.com/testpilot/testpilot/internal/grpcserver"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const testJWTSecret = "test-secret-0123456789abcdef"

const (
	copilotMethod = "/testpilot.copilot.v1.CopilotToolService/ListProjects"
	otherMethod   = "/testpilot.worker.v1.WorkerService/Connect"
)

// mkToken 用测试密钥签发 JWT（与生产 auth.IssueToken 同一实现）。
func mkToken(t *testing.T, userID, tenantID int64, role int16) string {
	t.Helper()
	tok, err := auth.IssueToken(testJWTSecret, userID, tenantID, role, 1)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestCopilotAuthUnary(t *testing.T) {
	inter := grpcserver.CopilotAuthUnary(testJWTSecret)
	req := &copilotv1.ListProjectsRequest{Ctx: copilotCtx(7, "9")} // UserId 是 JWT uid 的十进制串

	run := func(t *testing.T, ctx context.Context, r any, method string) (called bool, err error) {
		t.Helper()
		called = false
		_, err = inter(ctx, r, &grpc.UnaryServerInfo{FullMethod: method},
			func(ctx context.Context, req any) (any, error) { called = true; return nil, nil })
		return called, err
	}

	t.Run("non copilot method passes through", func(t *testing.T) {
		called, err := run(t, context.Background(), req, otherMethod)
		if err != nil || !called {
			t.Fatalf("want pass-through, err=%v called=%v", err, called)
		}
	})

	t.Run("missing token rejected", func(t *testing.T) {
		_, err := run(t, context.Background(), req, copilotMethod)
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("want Unauthenticated, got %v", err)
		}
	})

	t.Run("invalid token rejected", func(t *testing.T) {
		ctx := metadata.NewIncomingContext(context.Background(),
			metadata.Pairs("authorization", "Bearer not-a-jwt"))
		_, err := run(t, ctx, req, copilotMethod)
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("want Unauthenticated, got %v", err)
		}
	})

	t.Run("valid token but request missing Ctx", func(t *testing.T) {
		ctx := metadata.NewIncomingContext(context.Background(),
			metadata.Pairs("authorization", "Bearer "+mkToken(t, 9, 7, 1)))
		_, err := run(t, ctx, &struct{}{}, copilotMethod)
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("want InvalidArgument, got %v", err)
		}
	})

	t.Run("valid token and matching Ctx passes", func(t *testing.T) {
		ctx := metadata.NewIncomingContext(context.Background(),
			metadata.Pairs("authorization", "Bearer "+mkToken(t, 9, 7, 1)))
		called, err := run(t, ctx, req, copilotMethod)
		if err != nil || !called {
			t.Fatalf("want pass, err=%v called=%v", err, called)
		}
	})

	t.Run("tenant mismatch rejected", func(t *testing.T) {
		ctx := metadata.NewIncomingContext(context.Background(),
			metadata.Pairs("authorization", "Bearer "+mkToken(t, 9, 99, 1)))
		_, err := run(t, ctx, req, copilotMethod)
		if status.Code(err) != codes.PermissionDenied {
			t.Fatalf("want PermissionDenied, got %v", err)
		}
	})

	t.Run("user mismatch rejected", func(t *testing.T) {
		ctx := metadata.NewIncomingContext(context.Background(),
			metadata.Pairs("authorization", "Bearer "+mkToken(t, 42, 7, 1)))
		_, err := run(t, ctx, req, copilotMethod)
		if status.Code(err) != codes.PermissionDenied {
			t.Fatalf("want PermissionDenied, got %v", err)
		}
	})

	t.Run("non-bearer authorization header treated as missing", func(t *testing.T) {
		ctx := metadata.NewIncomingContext(context.Background(),
			metadata.Pairs("authorization", "Basic abc"))
		_, err := run(t, ctx, req, copilotMethod)
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("want Unauthenticated, got %v", err)
		}
	})
}

// fakeServerStream 最小 ServerStream 实现（WorkerAuthStream 只读 Context）。
type fakeServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (f *fakeServerStream) Context() context.Context { return f.ctx }

func TestWorkerAuthStream(t *testing.T) {
	run := func(t *testing.T, tokenCfg, tok string, method string) (called bool, err error) {
		t.Helper()
		inter := grpcserver.WorkerAuthStream(tokenCfg)
		called = false
		var md metadata.MD
		if tok != "" {
			md = metadata.Pairs("x-worker-token", tok)
		}
		ss := &fakeServerStream{ctx: metadata.NewIncomingContext(context.Background(), md)}
		err = inter(nil, ss, &grpc.StreamServerInfo{FullMethod: method},
			func(srv any, s grpc.ServerStream) error { called = true; return nil })
		return called, err
	}

	t.Run("empty configured token rejects everything", func(t *testing.T) {
		called, err := run(t, "", "anything", "/testpilot.worker.v1.WorkerService/Connect")
		if called || status.Code(err) != codes.Unauthenticated {
			t.Fatalf("want reject-all, called=%v err=%v", called, err)
		}
	})

	t.Run("non-connect method passes through", func(t *testing.T) {
		called, err := run(t, "tok123", "", "/testpilot.copilot.v1.CopilotToolService/ListProjects")
		if err != nil || !called {
			t.Fatalf("want pass-through, err=%v called=%v", err, called)
		}
	})

	t.Run("missing token rejected", func(t *testing.T) {
		called, err := run(t, "tok123", "", "/testpilot.worker.v1.WorkerService/Connect")
		if called || status.Code(err) != codes.Unauthenticated {
			t.Fatalf("want Unauthenticated, called=%v err=%v", called, err)
		}
	})

	t.Run("wrong token rejected", func(t *testing.T) {
		called, err := run(t, "tok123", "wrong", "/testpilot.worker.v1.WorkerService/Connect")
		if called || status.Code(err) != codes.Unauthenticated {
			t.Fatalf("want Unauthenticated, called=%v err=%v", called, err)
		}
	})

	t.Run("correct token passes", func(t *testing.T) {
		called, err := run(t, "tok123", "tok123", "/testpilot.worker.v1.WorkerService/Connect")
		if err != nil || !called {
			t.Fatalf("want pass, err=%v called=%v", err, called)
		}
	})
}
