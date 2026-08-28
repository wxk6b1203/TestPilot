package grpcserver_test

import (
	"context"
	"strings"
	"testing"
	"time"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	copilotv1 "github.com/testpilot/testpilot/gen/copilot/v1"
	"github.com/testpilot/testpilot/internal/dispatch"
	"github.com/testpilot/testpilot/internal/grpcserver"
	"github.com/testpilot/testpilot/internal/probe"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func probeClient(t *testing.T, hub *probe.Hub) copilotv1.CopilotToolServiceClient {
	t.Helper()
	d := openTestDB(t)
	var conn *grpc.ClientConn
	conn = bufConn(t, func(srv *grpc.Server) {
		copilotv1.RegisterCopilotToolServiceServer(srv, grpcserver.NewCopilotService(d, nil, hub))
	})
	_ = conn
	return copilotv1.NewCopilotToolServiceClient(conn)
}

func TestOpenProbeDisabled(t *testing.T) {
	cli := probeClient(t, nil) // nil hub = 功能关闭
	_, err := cli.OpenProbe(context.Background(), &copilotv1.OpenProbeRequest{
		Ctx:       &commonv1.RequestContext{TenantId: 1, UserId: "u"},
		SessionId: "s1",
		Url:       "https://aut.test/x",
	})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "PROBE_DISABLED") {
		t.Fatalf("want FailedPrecondition PROBE_DISABLED, got %v", err)
	}
}

func TestOpenProbeNoWorker(t *testing.T) {
	hub := probe.New(nil, probe.Config{
		IdleTTL: time.Minute, MaxLifetime: time.Hour,
		MaxPerWorker: 2, MaxPerTenant: 1, CmdTimeout: time.Second,
		SnapshotMaxBytes: 4096, EvalMaxBytes: 1024,
	})
	cli := probeClient(t, hub)
	_, err := cli.OpenProbe(context.Background(), &copilotv1.OpenProbeRequest{
		Ctx:       &commonv1.RequestContext{TenantId: 1, UserId: "u"},
		SessionId: "s1",
		Url:       "https://aut.test/x",
	})
	if status.Code(err) != codes.Unavailable || !strings.Contains(err.Error(), "PROBE_NO_WORKER") {
		t.Fatalf("want Unavailable PROBE_NO_WORKER, got %v", err)
	}
}

func TestOpenProbeInvalidURL(t *testing.T) {
	// enabled hub 但 dispatcher 为 nil 只影响选点；相对 url 无 env_id 应在解析阶段被拒
	hub := probe.New(nil, probe.Config{CmdTimeout: time.Second, IdleTTL: time.Minute,
		MaxLifetime: time.Hour, MaxPerWorker: 1, MaxPerTenant: 1,
		SnapshotMaxBytes: 4096, EvalMaxBytes: 1024})
	cli := probeClient(t, hub)
	_, err := cli.OpenProbe(context.Background(), &copilotv1.OpenProbeRequest{
		Ctx:       &commonv1.RequestContext{TenantId: 1, UserId: "u"},
		SessionId: "s1",
		Url:       "/relative/path",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument, got %v", err)
	}
	_ = dispatch.New(nil)
}

func TestRunProbeValidation(t *testing.T) {
	hub := probe.New(nil, probe.Config{
		IdleTTL: time.Minute, MaxLifetime: time.Hour,
		MaxPerWorker: 2, MaxPerTenant: 1, CmdTimeout: time.Second,
		SnapshotMaxBytes: 4096, EvalMaxBytes: 1024,
	})
	cli := probeClient(t, hub)
	rc := &commonv1.RequestContext{TenantId: 1, UserId: "u"}

	// 空 source → InvalidArgument（hub 调用前拦截）
	_, err := cli.RunProbe(context.Background(), &copilotv1.RunProbeRequest{
		Ctx: rc, SessionId: "s1", Source: "   "})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty source: want InvalidArgument, got %v", err)
	}

	// 超限 source → InvalidArgument
	big := strings.Repeat("x", 17*1024)
	_, err = cli.RunProbe(context.Background(), &copilotv1.RunProbeRequest{
		Ctx: rc, SessionId: "s1", Source: big})
	if status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "PROBE_LIMIT") {
		t.Fatalf("big source: want InvalidArgument PROBE_LIMIT, got %v", err)
	}

	// 合法 source → 无会话 → NotFound
	_, err = cli.RunProbe(context.Background(), &copilotv1.RunProbeRequest{
		Ctx: rc, SessionId: "nope", Source: "async def run(ctx): pass"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("unknown session: want NotFound, got %v", err)
	}
}
