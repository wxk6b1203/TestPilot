// Package metrics Prometheus 指标：/metrics 暴露 + HTTP 中间件 + 领域计数。
//
// 命名一律 testpilot_ 前缀；标签保持低基数（路由模板而非实际路径）。
// 业务计数由 dispatch/quota/notify 在关键路径打点；worker 池 gauge 由
// dispatcher 在注册/注销/负载变更后刷新（读当下真实值，无漂移）。
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// HTTPRequestsInFlight / HTTPRequests / HTTPDuration 由 HTTPMiddleware 打点。
	HTTPRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "testpilot", Subsystem: "http", Name: "requests_total",
		Help: "REST 请求总数（route=ServeMux 模板）。",
	}, []string{"route", "method", "code"})
	HTTPDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "testpilot", Subsystem: "http", Name: "request_duration_seconds",
		Help:    "REST 请求耗时。",
		Buckets: prometheus.DefBuckets,
	}, []string{"route"})

	WorkersOnline = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "testpilot", Name: "workers_online",
		Help: "当前在线 Worker 数。",
	})
	WorkerLoadSum = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "testpilot", Name: "worker_load_sum",
		Help: "全部 Worker 负载（在跑任务数）合计。",
	})
	DispatchTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "testpilot", Name: "dispatch_total",
		Help: "任务派发结果（ok/no_worker）。",
	}, []string{"result"})

	RunsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "testpilot", Name: "runs_total",
		Help: "功能测试运行收尾计数（status=passed/failed，trigger=manual/scheduled）。",
	}, []string{"status", "trigger"})
	RunDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "testpilot", Name: "run_duration_seconds",
		Help:    "功能测试运行时长（收尾时观测）。",
		Buckets: []float64{1, 5, 10, 30, 60, 120, 300, 600, 1800},
	}, []string{"status"})
	StressRunsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "testpilot", Name: "stress_runs_total",
		Help: "压测运行收尾计数。",
	}, []string{"status"})

	QuotaRejections = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "testpilot", Name: "quota_rejections_total",
		Help: "配额拒绝计数（按 metric）。",
	}, []string{"metric"})
	ArtifactsDropped = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "testpilot", Name: "artifacts_dropped_total",
		Help: "因 artifact_bytes 配额丢弃的产物数。",
	})

	Notifications = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "testpilot", Name: "notifications_total",
		Help: "通知发送计数（type=webhook/dingtalk/feishu，result=ok/error/disabled）。",
	}, []string{"type", "result"})
)

// Handler 暴露 /metrics。
func Handler() http.Handler { return promhttp.Handler() }

// HTTPMiddleware 打点 REST 请求计数与耗时。
// Next 返回后 c.Route() 指向终端路由（use 中间件自身路径为 "/"），标签用模板而非实际路径。
func HTTPMiddleware(c fiber.Ctx) error {
	start := time.Now()
	err := c.Next()
	route := "unmatched"
	if r := c.Route(); r != nil && r.Path != "" && r.Path != "/" {
		route = r.Path
	}
	HTTPRequests.WithLabelValues(route, c.Method(), strconv.Itoa(c.Response().StatusCode())).Inc()
	HTTPDuration.WithLabelValues(route).Observe(time.Since(start).Seconds())
	return err
}

// ---- 领域打点辅助（状态/触发枚举 → 低基数字符串） ----

// RunStatusName 1=RUNNING（不打点） 2=passed 3=failed 4=canceled。
func RunStatusName(s int16) string {
	switch s {
	case 2:
		return "passed"
	case 3:
		return "failed"
	case 4:
		return "canceled"
	default:
		return "other"
	}
}

// TriggerName 1=manual 2=scheduled 3=ci。
func TriggerName(t int16) string {
	switch t {
	case 2:
		return "scheduled"
	case 3:
		return "ci"
	default:
		return "manual"
	}
}

// ChannelTypeName 通知渠道类型。
func ChannelTypeName(t int16) string {
	switch t {
	case 2:
		return "dingtalk"
	case 3:
		return "feishu"
	default:
		return "webhook"
	}
}
