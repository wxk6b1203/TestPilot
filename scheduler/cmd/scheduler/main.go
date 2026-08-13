package main

import (
	"context"
	"net"

	"github.com/gofiber/fiber/v3"
	copilotv1 "github.com/testpilot/testpilot/gen/copilot/v1"
	workerv1 "github.com/testpilot/testpilot/gen/worker/v1"
	"github.com/testpilot/testpilot/internal/config"
	"github.com/testpilot/testpilot/internal/cronsched"
	"github.com/testpilot/testpilot/internal/db"
	"github.com/testpilot/testpilot/internal/dispatch"
	"github.com/testpilot/testpilot/internal/grpcserver"
	"github.com/testpilot/testpilot/internal/httpserver"
	"github.com/testpilot/testpilot/internal/logging"
	"github.com/testpilot/testpilot/internal/retention"
	"github.com/testpilot/testpilot/internal/runner"
	"github.com/testpilot/testpilot/internal/tracing"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	cfg := config.Load()
	logging.Init(cfg.LogLevel, cfg.LogFormat)
	defer logging.Sync()

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

	disp := dispatch.New(gormDB)
	run := runner.New(gormDB, disp)
	cron := cronsched.New(gormDB, run)
	cron.Start()
	defer cron.Stop()
	retention.Start(gormDB, cfg.ArtifactDir, cfg.RetentionDays, cfg.RetentionIntervalMin)

	// gRPC（Worker 双向流 + Copilot 工具面同端口）
	gs := grpc.NewServer(grpc.StatsHandler(otelgrpc.NewServerHandler()))
	workerv1.RegisterWorkerServiceServer(gs, grpcserver.NewWorkerService(disp))
	copilotv1.RegisterCopilotToolServiceServer(gs, grpcserver.NewCopilotService(gormDB, run))
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
	app := httpserver.New(gormDB, cfg, disp, run, cron).App()
	logging.L.Infow("http listening", "addr", cfg.HTTPAddr)
	if err := app.Listen(cfg.HTTPAddr, fiber.ListenConfig{DisableStartupMessage: true}); err != nil {
		logging.L.Fatalw("http serve failed", "err", err)
	}
}
