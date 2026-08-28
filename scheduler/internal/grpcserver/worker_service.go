package grpcserver

import (
	"fmt"
	"io"
	"time"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	workerv1 "github.com/testpilot/testpilot/gen/worker/v1"
	"github.com/testpilot/testpilot/internal/dispatch"
	"github.com/testpilot/testpilot/internal/events"
	"github.com/testpilot/testpilot/internal/logging"
	"github.com/testpilot/testpilot/internal/probe"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// WorkerService 实现 testpilot.worker.v1.WorkerService。
type WorkerService struct {
	workerv1.UnimplementedWorkerServiceServer
	Disp  *dispatch.Dispatcher
	Probe *probe.Hub // UI 探测回执分发（nil = 功能关闭）
}

func NewWorkerService(d *dispatch.Dispatcher, probe *probe.Hub) *WorkerService {
	return &WorkerService{Disp: d, Probe: probe}
}

// Connect 处理 Worker 双向流：首帧 register，之后心跳/进度/结果。
func (s *WorkerService) Connect(stream workerv1.WorkerService_ConnectServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	reg := first.GetRegister()
	if reg == nil {
		return status.Error(codes.InvalidArgument, "first event must be register")
	}
	workerID := reg.GetWorkerId()
	if workerID == "" {
		workerID = fmt.Sprintf("w-%d", time.Now().UnixNano())
	}
	w := &dispatch.Worker{
		ID:             workerID,
		Name:           reg.GetWorkerName(),
		Capabilities:   capsToInt32(reg.GetCapabilities()),
		TenantID:       reg.GetTenantId(),
		MaxConcurrency: reg.GetMaxConcurrency(),
		Tags:           reg.GetTags(),
		SDKVersion:     reg.GetSdkVersion(),
		Send:           make(chan *workerv1.SchedulerCommand, 32),
	}
	if err := s.Disp.Register(w); err != nil {
		logging.L.Warnw("worker register rejected", "id", w.ID, "err", err)
		return status.Error(codes.ResourceExhausted, err.Error())
	}
	logging.L.Infow("worker registered", "id", w.ID, "caps", w.Capabilities, "sdk", w.SDKVersion)
	s.Disp.Events().Publish("workers", events.Event{Type: "worker_updated", Data: map[string]any{
		"worker_id": w.ID, "action": "registered",
	}})
	defer func() {
		if s.Probe != nil {
			s.Probe.OnWorkerDisconnect(w.ID) // 探测会话随 Worker 下线
		}
		s.Disp.Unregister(w.ID)
		w.Shutdown() // 关闭信号：派发方/泵协程感知退出（Send 永不 close，防 send-on-closed panic）
		logging.L.Infow("worker disconnected", "id", w.ID)
		s.Disp.Events().Publish("workers", events.Event{Type: "worker_updated", Data: map[string]any{
			"worker_id": w.ID, "action": "disconnected",
		}})
	}()

	// 下行：命令泵到流（closed 感知；Send 不再被 close）
	sendErr := make(chan error, 1)
	go func() {
		for {
			select {
			case cmd := <-w.Send:
				if err := stream.Send(cmd); err != nil {
					sendErr <- err
					return
				}
			case <-w.Closed():
				return
			}
		}
	}()

	// 上行：事件处理（select 模式——被 reaper 剔除时经 w.Closed 退出，避免
	// Recv 在健康连接上无限阻塞导致 goroutine/流泄漏）
	type recvResult struct {
		ev  *workerv1.WorkerEvent
		err error
	}
	recvCh := make(chan recvResult, 1)
	go func() {
		for {
			ev, err := stream.Recv()
			recvCh <- recvResult{ev, err}
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case rr := <-recvCh:
			if rr.err == io.EOF {
				return nil
			}
			if rr.err != nil {
				select {
				case se := <-sendErr:
					return se
				default:
				}
				return rr.err
			}
			s.handleEvent(w, rr.ev)
		case <-w.Closed():
			return status.Error(codes.Aborted, "worker evicted by scheduler")
		}
	}
}

func (s *WorkerService) handleEvent(w *dispatch.Worker, ev *workerv1.WorkerEvent) {
	switch e := ev.Event.(type) {
	case *workerv1.WorkerEvent_Heartbeat:
		s.Disp.SetLoad(w.ID, e.Heartbeat.GetCurrentConcurrency())
	case *workerv1.WorkerEvent_TaskResult:
		if err := s.Disp.HandleTaskResult(w, e.TaskResult); err != nil {
			logging.L.Errorw("handle task result failed", "err", err, "run", e.TaskResult.GetRunId())
		}
	case *workerv1.WorkerEvent_StressMetrics:
		if err := s.Disp.HandleStressMetrics(e.StressMetrics); err != nil {
			logging.L.Errorw("handle stress metrics failed", "err", err, "run", e.StressMetrics.GetRunId())
		}
	case *workerv1.WorkerEvent_StepProgress:
		detail := map[string]any{}
		if e.StepProgress.Detail != nil {
			detail = e.StepProgress.Detail.AsMap()
		}
		s.Disp.Events().Publish("run:"+e.StepProgress.GetRunId(), events.Event{
			Type: "step_progress",
			Data: map[string]any{
				"run_id":         e.StepProgress.GetRunId(),
				"case_result_id": e.StepProgress.GetCaseId(),
				"step_path":      e.StepProgress.GetStepPath(),
				"status":         e.StepProgress.GetStatus(),
				"detail":         detail,
			},
		})
	case *workerv1.WorkerEvent_LogBatch:
		logging.L.Debugw("worker logs", "task", e.LogBatch.GetTaskId(), "lines", len(e.LogBatch.GetLines()))
	case *workerv1.WorkerEvent_Artifact:
		logging.L.Debugw("worker artifact", "kind", e.Artifact.GetKind(), "uri", e.Artifact.GetUri())
	case *workerv1.WorkerEvent_ProbeReply:
		if s.Probe != nil {
			s.Probe.Deliver(e.ProbeReply) // pending 配对唤醒（迟到回执静默丢弃）
		}
	}
}

func capsToInt32(caps []commonv1.Capability) []int32 {
	out := make([]int32, len(caps))
	for i, c := range caps {
		out[i] = int32(c)
	}
	return out
}
