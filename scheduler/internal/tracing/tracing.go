// Package tracing OpenTelemetry 链路：三进程统一 trace_id（design 14）。
//
// 环境变量：
//
//	TP_OTEL_EXPORTER  "" 关闭（默认）| "stdout" 开发调试 | "otlp" 发 Collector
//	TP_OTEL_ENDPOINT  otlp gRPC 地址（默认 127.0.0.1:4317，dev 一律 insecure）
//
// 传播：Scheduler REST 入口由下方 fiber 中间件起 span、gRPC 由 otelgrpc 起 span；
// 派发给 Worker 时 traceparent 写入 TaskAssignment（Worker 侧提取续链）；
// Copilot 经 gRPC metadata 注入。关闭时 InjectTraceparent 返回 ""，全链路零开销。
package tracing

import (
	"context"
	"os"

	"github.com/gofiber/fiber/v3"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

func init() {
	// 传播器始终安装：即使 SDK 关闭，注入/提取逻辑也安全（返回空 carrier）。
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{}))
}

// Init 按环境初始化 TracerProvider；返回 shutdown（main defer 调用）。关闭时为 no-op。
func Init(serviceName string) func(context.Context) {
	switch os.Getenv("TP_OTEL_EXPORTER") {
	case "stdout", "otlp":
	default:
		return func(context.Context) {}
	}

	// NewSchemaless：避免与 resource.Default() 的 semconv schema URL 冲突
	// （冲突会让 Merge 返回 error 并丢掉 service.name，回退成 unknown_service）。
	res, err := resource.Merge(resource.Default(),
		resource.NewSchemaless(semconv.ServiceName(serviceName)))
	if err != nil {
		res = resource.Default()
	}
	if err != nil {
		res = resource.Default()
	}

	var exp sdktrace.SpanExporter
	if os.Getenv("TP_OTEL_EXPORTER") == "stdout" {
		exp, err = stdouttrace.New()
	} else {
		endpoint := os.Getenv("TP_OTEL_ENDPOINT")
		if endpoint == "" {
			endpoint = "127.0.0.1:4317"
		}
		exp, err = otlptracegrpc.New(context.Background(),
			otlptracegrpc.WithEndpoint(endpoint), otlptracegrpc.WithInsecure())
	}
	if err != nil {
		return func(context.Context) {}
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(exp),
	)
	otel.SetTracerProvider(tp)
	return func(ctx context.Context) { _ = tp.Shutdown(ctx) }
}

// InjectTraceparent 提取 ctx 当前 span 的 W3C traceparent（无有效 span → ""）。
func InjectTraceparent(ctx context.Context) string {
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	return carrier["traceparent"]
}

// TraceID 日志关联用（无有效 span → ""）。
func TraceID(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.HasTraceID() {
		return ""
	}
	return sc.TraceID().String()
}

// Middleware 起 HTTP server span：提取入站 traceparent（REST 侧通常无父），
// span ctx 经 c.SetContext 供下游（auth/runner.Trigger 等）续链；Next 后回填
// 路由模板与状态码。SDK 关闭时 Start 返回 no-op span，零开销。
func Middleware() fiber.Handler {
	tracer := otel.Tracer("testpilot/scheduler")
	return func(c fiber.Ctx) error {
		ctx := otel.GetTextMapPropagator().Extract(c.Context(), headerCarrier{c})
		ctx, span := tracer.Start(ctx, "scheduler.http", trace.WithSpanKind(trace.SpanKindServer))
		c.SetContext(ctx)
		defer span.End()
		err := c.Next()
		route := "unmatched"
		if r := c.Route(); r != nil && r.Path != "" && r.Path != "/" {
			route = r.Path
		}
		status := c.Response().StatusCode()
		span.SetName(c.Method() + " " + route)
		span.SetAttributes(
			attribute.String("http.request.method", c.Method()),
			attribute.String("http.route", route),
			attribute.Int("http.response.status_code", status),
		)
		if status >= fiber.StatusInternalServerError {
			span.SetStatus(codes.Error, "")
		}
		return err
	}
}

// headerCarrier 仅供 Extract：propagator 按其关心的 key（traceparent/tracestate/baggage）
// 逐个 Get，无需实现 Set/Keys。
type headerCarrier struct{ c fiber.Ctx }

func (h headerCarrier) Get(key string) string { return h.c.Get(key) }
func (h headerCarrier) Set(string, string)    {}
func (h headerCarrier) Keys() []string        { return nil }
