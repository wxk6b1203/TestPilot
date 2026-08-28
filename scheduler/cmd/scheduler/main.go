package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v3"
	copilotv1 "github.com/testpilot/testpilot/gen/copilot/v1"
	workerv1 "github.com/testpilot/testpilot/gen/worker/v1"
	"github.com/testpilot/testpilot/internal/artifactstore"
	"github.com/testpilot/testpilot/internal/config"
	"github.com/testpilot/testpilot/internal/copilottrash"
	"github.com/testpilot/testpilot/internal/cronsched"
	"github.com/testpilot/testpilot/internal/db"
	"github.com/testpilot/testpilot/internal/dispatch"
	"github.com/testpilot/testpilot/internal/grpcserver"
	"github.com/testpilot/testpilot/internal/httpserver"
	"github.com/testpilot/testpilot/internal/logging"
	"github.com/testpilot/testpilot/internal/model"
	"github.com/testpilot/testpilot/internal/probe"
	"github.com/testpilot/testpilot/internal/retention"
	"github.com/testpilot/testpilot/internal/runner"
	"github.com/testpilot/testpilot/internal/tracing"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
)

func main() {
	cfg := config.Load()
	if err := model.SetSnowflakeNode(cfg.SnowflakeNode); err != nil {
		logging.L.Fatalw("snowflake_node invalid", "node", cfg.SnowflakeNode, "err", err)
	}
	logging.Init(cfg.LogLevel, cfg.LogFormat)
	defer logging.Sync()

	// C1：JWT 默认/弱密钥直接拒绝启动，防止生产裸奔可被伪造任意身份 token
	if cfg.JWTSecret == "" || cfg.JWTSecret == "dev-secret-change-me" || len(cfg.JWTSecret) < 16 {
		logging.L.Fatalw("jwt_secret 未配置或为默认/弱值：请设置强随机密钥（>=16 字符，TP_JWT_SECRET）")
	}

	shutdownTrace := tracing.Init("testpilot-scheduler", cfg.OTelExporter, cfg.OTelEndpoint)
	defer shutdownTrace(context.Background())

	gormDB, err := db.Open(cfg.DBPath, cfg.DBDSN, db.Pool{
		MaxOpenConns:       cfg.DBMaxOpenConns,
		MaxIdleConns:       cfg.DBMaxIdleConns,
		ConnMaxLifetimeMin: cfg.DBConnMaxLifetimeMin,
	})
	if err != nil {
		logging.L.Fatalw("open db failed", "err", err)
	}
	logging.L.Infow("db ready", "path", cfg.DBPath, "dsn_set", cfg.DBDSN != "")

	// A3：启动恢复——终结进程重启遗留的 RUNNING run（防止永久卡死 + cron overlap 卡住）
	// 多实例部署必须关闭（TP_RECOVER_INTERRUPTED=false），否则第二实例会误杀在跑 run。
	if cfg.RecoverInterrupted {
		runner.RecoverInterruptedRuns(gormDB)
	}

	disp := dispatch.New(gormDB)
	run := runner.New(gormDB, disp)
	cron := cronsched.New(gormDB, run)
	cron.Start()
	defer cron.Stop()

	// UI 探测（v1）：probe_enabled=false 时不构造 Hub，RPC 统一返回 PROBE_DISABLED
	var probeHub *probe.Hub
	if cfg.ProbeEnabled {
		probeHub = probe.New(disp, probe.Config{
			IdleTTL:          time.Duration(cfg.ProbeSessionIdleTTLMin) * time.Minute,
			MaxLifetime:      time.Duration(cfg.ProbeSessionMaxLifetimeMin) * time.Minute,
			MaxPerWorker:     cfg.ProbeMaxSessionsPerWorker,
			MaxPerTenant:     cfg.ProbeMaxSessionsPerTenant,
			CmdTimeout:       time.Duration(cfg.ProbeCmdTimeoutSec) * time.Second,
			SnapshotMaxBytes: int32(cfg.ProbeSnapshotMaxBytes),
			EvalMaxBytes:     int32(cfg.ProbeEvalMaxBytes),
		})
		stopProbe := probeStartSweeper(probeHub)
		defer close(stopProbe)
	}

	// A3：后台回收——失联 Worker 剔除（心跳超时）+ 超时 run 终结
	stopReapers := runner.StartReapers(gormDB, disp)
	defer stopReapers()

	// 产物后端（local 默认 / s3 对象存储）
	artifacts, err := artifactstore.New(cfg)
	if err != nil {
		logging.L.Fatalw("artifact store init failed", "err", err)
	}
	disp.SetArtifactIngest(artifacts, cfg.ArtifactDir)
	retention.Start(gormDB, artifacts, cfg.RetentionDays, cfg.RetentionIntervalMin)
	copilottrash.Start(gormDB, cfg.CopilotTrashDays)

	// gRPC（Worker 双向流 + Copilot 工具面同端口）
	// A3：keepalive 让网络分区/静默断连的流在 ~40s 内被服务端发现（Recv 报错 → 连接清理）
	gs := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    30 * time.Second,
			Timeout: 10 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: true,
		}),
		// A1：gRPC 认证——Worker 流校验 x-worker-token；Copilot 工具面校验 JWT
		grpc.ChainUnaryInterceptor(grpcserver.CopilotAuthUnary(cfg.JWTSecret)),
		grpc.ChainStreamInterceptor(grpcserver.WorkerAuthStream(cfg.WorkerToken)),
	)
	workerv1.RegisterWorkerServiceServer(gs, grpcserver.NewWorkerService(disp, probeHub))
	copilotv1.RegisterCopilotToolServiceServer(gs, grpcserver.NewCopilotService(gormDB, run, probeHub))
	reflection.Register(gs)

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		logging.L.Fatalw("grpc listen failed", "addr", cfg.GRPCAddr, "err", err)
	}
	go func() {
		logging.L.Infow("grpc listening", "addr", cfg.GRPCAddr)
		if err := gs.Serve(lis); err != nil {
			logging.L.Fatalw("grpc serve failed", "err", err)
		}
	}()

	// REST + 前端托管（fiber v3 / fasthttp）
	app := httpserver.New(gormDB, cfg, disp, run, cron, artifacts).App()
	go func() {
		logging.L.Infow("http listening", "addr", cfg.HTTPAddr)
		if err := app.Listen(cfg.HTTPAddr, fiber.ListenConfig{DisableStartupMessage: true}); err != nil &&
			!errors.Is(err, http.ErrServerClosed) {
			logging.L.Fatalw("http serve failed", "err", err)
		}
	}()

	// 优雅停机：SIGINT/SIGTERM → 停收新请求 → 先通知 Worker 断开流
	// （否则双向流不结束，GracefulStop 永久阻塞；reaper 的 stop 是 defer，排在
	// GracefulStop 之后执行，不能依赖它结束 Worker 流）→ GracefulStop（等在途
	// 任务落库，带超时兜底，超时强制 Stop）→ 退出
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	logging.L.Infow("shutting down", "addr", cfg.HTTPAddr)
	if err := app.Shutdown(); err != nil {
		logging.L.Warnw("http shutdown failed", "err", err)
	}
	for _, w := range disp.Workers() {
		w.Shutdown() // 结束双向流，让 GracefulStop 能返回
	}
	gracefulDone := make(chan struct{})
	go func() {
		gs.GracefulStop()
		close(gracefulDone)
	}()
	select {
	case <-gracefulDone:
	case <-time.After(15 * time.Second):
		logging.L.Warnw("grpc graceful stop timed out, forcing stop")
		gs.Stop()
		<-gracefulDone
	}
	logging.L.Infow("shutdown complete")
}

// probeStartSweeper 探测会话 TTL 回收循环（复用 reaper 节奏；返回 stop 函数）。
func probeStartSweeper(hub *probe.Hub) chan struct{} {
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				hub.Sweep()
			case <-stop:
				return
			}
		}
	}()
	return stop
}
