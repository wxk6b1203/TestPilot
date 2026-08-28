// UI 探测 RPC handlers（v1，docs/ui-probe-design.md §4.2.3）。
// 鉴权由 CopilotAuthUnary 统一处理（JWT ↔ RequestContext 交叉校验）；
// 会话生命周期/命令路由在 internal/probe；本文件只做参数解析、开关校验、审计与错误映射。
package grpcserver

import (
	"context"
	"errors"
	"strings"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	copilotv1 "github.com/testpilot/testpilot/gen/copilot/v1"
	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/model"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// probeErrToStatus 探测域错误 → gRPC status（码前缀见 docs/error-codes.md gRPC 小节）。
func probeErrToStatus(err error) error {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		switch ae.Code {
		case apperr.CodeProbeDisabled:
			return status.Error(codes.FailedPrecondition, "PROBE_DISABLED: "+ae.Message)
		case apperr.CodeProbeNoWorker:
			return status.Error(codes.Unavailable, "PROBE_NO_WORKER: "+ae.Message)
		case apperr.CodeProbeLimit:
			return status.Error(codes.ResourceExhausted, "PROBE_LIMIT: "+ae.Message)
		case apperr.CodeProbeSessionGone:
			return status.Error(codes.NotFound, "PROBE_SESSION_NOT_FOUND: "+ae.Message)
		case apperr.CodeProbeTimeout:
			return status.Error(codes.DeadlineExceeded, "PROBE_TIMEOUT: "+ae.Message)
		}
	}
	return status.Error(codes.Internal, "PROBE_FAILED: "+err.Error())
}

// resolveProbeURL 相对 url 基于环境 base_url 拼接；绝对 url 原样透传。
// env_id 为空且 url 为相对路径时拒绝（探测不做隐式默认环境）。
func (s *CopilotService) resolveProbeURL(rc *commonv1.RequestContext, url, envID string) (string, error) {
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return url, nil
	}
	if url == "" {
		return "", status.Error(codes.InvalidArgument, "PROBE_FAILED: url is required")
	}
	eid := int64(0)
	if envID != "" {
		eid = mustID(envID)
	}
	if eid == 0 {
		return "", status.Error(codes.InvalidArgument,
			"PROBE_FAILED: relative url requires env_id (or use an absolute url)")
	}
	var env model.Environment
	if err := s.db.Where("id = ? AND tenant_id = ?", eid, tid(rc)).First(&env).Error; err != nil {
		return "", status.Error(codes.NotFound, "PROBE_FAILED: environment not found")
	}
	if env.BaseURL == "" {
		return "", status.Error(codes.FailedPrecondition, "PROBE_FAILED: environment has no base_url")
	}
	return strings.TrimSuffix(env.BaseURL, "/") + "/" + strings.TrimLeft(url, "/"), nil
}

func (s *CopilotService) probeReady() bool { return s.probe != nil && s.probe.Enabled() }

// OpenProbe 打开（或复用）探测会话（写类：资源副作用 + 首次触达被测系统，HITL 审批后调用）。
func (s *CopilotService) OpenProbe(ctx context.Context, req *copilotv1.OpenProbeRequest) (*copilotv1.OpenProbeResponse, error) {
	rc := req.GetCtx()
	if !s.probeReady() {
		return nil, status.Error(codes.FailedPrecondition, "PROBE_DISABLED: probe disabled (probe_enabled=false)")
	}
	url, err := s.resolveProbeURL(rc, req.GetUrl(), req.GetEnvId())
	if err != nil {
		return nil, err
	}
	if req.GetSessionId() == "" {
		return nil, status.Error(codes.InvalidArgument, "PROBE_FAILED: session_id is required")
	}
	state, workerID, err := s.probe.Open(ctx, tid(rc), rc.GetUserId(), req.GetSessionId(), url)
	if err != nil {
		return nil, probeErrToStatus(err)
	}
	s.audit(rc, "probe.open", "probe_session", req.GetSessionId(),
		map[string]any{"url": url, "worker_id": workerID})
	return &copilotv1.OpenProbeResponse{
		SessionId:         req.GetSessionId(),
		WorkerId:          workerID,
		FinalUrl:          state.GetFinalUrl(),
		Title:             state.GetTitle(),
		AriaSnapshot:      state.GetAriaSnapshot(),
		SnapshotTruncated: state.GetSnapshotTruncated(),
	}, nil
}

// GetProbeSnapshot 当前页面 ARIA 快照（只读，免审批）。
func (s *CopilotService) GetProbeSnapshot(ctx context.Context, req *copilotv1.GetProbeSnapshotRequest) (*copilotv1.GetProbeSnapshotResponse, error) {
	rc := req.GetCtx()
	if !s.probeReady() {
		return nil, status.Error(codes.FailedPrecondition, "PROBE_DISABLED: probe disabled (probe_enabled=false)")
	}
	state, err := s.probe.Snapshot(ctx, tid(rc), req.GetSessionId(), req.GetRef())
	if err != nil {
		return nil, probeErrToStatus(err)
	}
	return &copilotv1.GetProbeSnapshotResponse{
		FinalUrl:          state.GetFinalUrl(),
		Title:             state.GetTitle(),
		AriaSnapshot:      state.GetAriaSnapshot(),
		SnapshotTruncated: state.GetSnapshotTruncated(),
	}, nil
}

// CloseProbe 关闭探测会话（只读语义：释放资源，幂等）。
func (s *CopilotService) CloseProbe(ctx context.Context, req *copilotv1.CloseProbeRequest) (*copilotv1.CloseProbeResponse, error) {
	rc := req.GetCtx()
	if !s.probeReady() {
		return nil, status.Error(codes.FailedPrecondition, "PROBE_DISABLED: probe disabled (probe_enabled=false)")
	}
	if err := s.probe.Close(tid(rc), req.GetSessionId(), "user"); err != nil {
		return nil, probeErrToStatus(err)
	}
	return &copilotv1.CloseProbeResponse{Ok: true}, nil
}

// ActProbe 执行单步 UI 动作（写类：对被测系统有潜在副作用，HITL 审批后调用）。
func (s *CopilotService) ActProbe(ctx context.Context, req *copilotv1.ActProbeRequest) (*copilotv1.ActProbeResponse, error) {
	rc := req.GetCtx()
	if !s.probeReady() {
		return nil, status.Error(codes.FailedPrecondition, "PROBE_DISABLED: probe disabled (probe_enabled=false)")
	}
	act := req.GetAction()
	if act == nil {
		return nil, status.Error(codes.InvalidArgument, "PROBE_FAILED: action is required")
	}
	state, err := s.probe.Act(ctx, tid(rc), req.GetSessionId(), act)
	if err != nil {
		return nil, probeErrToStatus(err)
	}
	s.audit(rc, "probe.act", "probe_session", req.GetSessionId(),
		map[string]any{"action": act.GetAction().String(), "target": act.GetTarget()})
	return &copilotv1.ActProbeResponse{
		FinalUrl:          state.GetFinalUrl(),
		Title:             state.GetTitle(),
		AriaSnapshot:      state.GetAriaSnapshot(),
		SnapshotTruncated: state.GetSnapshotTruncated(),
	}, nil
}

// EvalProbe 页面上下文执行 JS（写类：等同脚本执行，HITL 审批后调用；审计截断表达式）。
func (s *CopilotService) EvalProbe(ctx context.Context, req *copilotv1.EvalProbeRequest) (*copilotv1.EvalProbeResponse, error) {
	rc := req.GetCtx()
	if !s.probeReady() {
		return nil, status.Error(codes.FailedPrecondition, "PROBE_DISABLED: probe disabled (probe_enabled=false)")
	}
	expr := req.GetExpression()
	if strings.TrimSpace(expr) == "" {
		return nil, status.Error(codes.InvalidArgument, "PROBE_FAILED: expression is required")
	}
	res, err := s.probe.Eval(ctx, tid(rc), req.GetSessionId(), expr)
	if err != nil {
		return nil, probeErrToStatus(err)
	}
	detail := expr
	if len(detail) > 1024 {
		detail = detail[:1024] + "…"
	}
	s.audit(rc, "probe.eval", "probe_session", req.GetSessionId(),
		map[string]any{"expression": detail})
	return &copilotv1.EvalProbeResponse{ResultJson: res.GetResultJson(), ResultTruncated: res.GetResultTruncated()}, nil
}
