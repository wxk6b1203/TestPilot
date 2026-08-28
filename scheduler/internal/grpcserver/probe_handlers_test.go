package grpcserver_test

import (
	"context"
	"strings"
	"testing"
	"time"

	copilotv1 "github.com/testpilot/testpilot/gen/copilot/v1"
	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
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
