package grpcserver

import (
	"fmt"
	"io"
	"time"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	workerv1 "github.com/testpilot/testpilot/gen/worker/v1"
	"github.com/testpilot/testpilot/internal/dispatch"
	"github.com/testpilot/testpilot/internal/logging"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// WorkerService 实现 testpilot.worker.v1.WorkerService。
type WorkerService struct {
	workerv1.UnimplementedWorkerServiceServer
	Disp *dispatch.Dispatcher
}

func NewWorkerService(d *dispatch.Dispatcher) *WorkerService {
	return &WorkerService{Disp: d}
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
	defer func() {
		s.Disp.Unregister(w.ID)
		close(w.Send)
		logging.L.Infow("worker disconnected", "id", w.ID)
	}()

	// 下行：命令泵到流
	sendErr := make(chan error, 1)
	go func() {
		for cmd := range w.Send {
			if err := stream.Send(cmd); err != nil {
				sendErr <- err
				return
			}
		}
	}()

	// 上行：事件处理
	for {
		ev, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			select {
			case se := <-sendErr:
				return se
			default:
			}
			return err
		}
		s.handleEvent(w, ev)
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
		logging.L.Debugw("step progress", "task", e.StepProgress.GetTaskId(), "path", e.StepProgress.GetStepPath(), "status", e.StepProgress.GetStatus())
	case *workerv1.WorkerEvent_LogBatch:
		logging.L.Debugw("worker logs", "task", e.LogBatch.GetTaskId(), "lines", len(e.LogBatch.GetLines()))
	case *workerv1.WorkerEvent_Artifact:
		logging.L.Debugw("worker artifact", "kind", e.Artifact.GetKind(), "uri", e.Artifact.GetUri())
	}
}

func capsToInt32(caps []commonv1.Capability) []int32 {
	out := make([]int32, len(caps))
	for i, c := range caps {
		out[i] = int32(c)
	}
	return out
}
