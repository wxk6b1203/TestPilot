// Package probe UI 探测会话调度（Copilot gRPC 工具面 → Worker 命令流）。
//
// 设计：docs/ui-probe-design.md §4.2。职责：
//   - 探测会话注册表（粘性路由：session → worker）
//   - 命令下行经既有 dispatch.Worker.Send 通道；回执经 grpcserver Deliver 配对唤醒
//   - 限额三闸（per-worker / per-tenant）、TTL 回收（Sweep 由 main reaper 周期调用）
//   - Worker 断连 → 相关会话全部失效（PROBE_SESSION_NOT_FOUND，Copilot 引导重新 open）
//
// 简化说明（相对设计 §3 状态机）：会话在注册表中存在即可路由，TTL/断连/关闭
// 直接删除（等价 DEAD）；设计中的 IDLE 中间态不做——重建语义由「重新 open」
// 承担，页面状态本就不保留，实现复杂度不值得。
package probe

import (
	"context"
	"strconv"
	"sync"
	"time"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	workerv1 "github.com/testpilot/testpilot/gen/worker/v1"
	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/dispatch"
	"github.com/testpilot/testpilot/internal/model"
	"google.golang.org/protobuf/types/known/durationpb"
)

const capabilityPlaywright = int32(commonv1.Capability_CAPABILITY_PLAYWRIGHT)

// Config 探测调度参数（来自 scheduler config，见 Defaults）。
type Config struct {
	IdleTTL          time.Duration // 空闲回收
	MaxLifetime      time.Duration // 强制回收
	MaxPerWorker     int
	MaxPerTenant     int
	CmdTimeout       time.Duration // 单命令 pending 等待上限
	SnapshotMaxBytes int32         // 下行快照上限（Worker 另有 32KB 绝对顶）
	EvalMaxBytes     int32         // 下行 eval 结果上限（Worker 另有 8KB 绝对顶）
}

// Session 一个探测会话的元数据（浏览器进程在 Worker 侧）。
type Session struct {
	ID         string
	TenantID   int64
	UserID     string
	WorkerID   string
	BaseURL    string
	CreatedAt  time.Time
	LastActive time.Time
}

type pendingCall struct {
	ch   chan *workerv1.ProbeReply
	kill *time.Timer // 超时兜底：即使 caller 放弃也清理 pending 表
}

// Hub 探测会话调度器。
type Hub struct {
	disp *dispatch.Dispatcher
	cfg  Config

	mu       sync.Mutex
	sessions map[string]*Session
	pending  map[string]*pendingCall
}

// New 构造 Hub。
func New(disp *dispatch.Dispatcher, cfg Config) *Hub {
	return &Hub{
		disp:     disp,
		cfg:      cfg,
		sessions: map[string]*Session{},
		pending:  map[string]*pendingCall{},
	}
}

// Enabled 探测功能是否开启。
func (h *Hub) Enabled() bool { return h != nil && h.cfg.CmdTimeout > 0 }

var (
	errSessionGone = apperr.New(404, apperr.CodeProbeSessionGone, "probe session not found (expired or closed)")
	errNoWorker    = apperr.New(503, apperr.CodeProbeNoWorker, "no online worker with playwright capability")
	errLimit       = apperr.New(429, apperr.CodeProbeLimit, "probe session limit exceeded")
	errDisabled    = apperr.New(503, apperr.CodeProbeDisabled, "probe disabled (probe_enabled=false)")
)

// Open 打开（或复用）探测会话并导航到 url（绝对地址，base_url 由 handler 解析）。
// 返回最终页面状态与承载 Worker。
func (h *Hub) Open(ctx context.Context, tenantID int64, userID, sessionID, url string) (*workerv1.ProbeState, string, error) {
	if !h.Enabled() {
		return nil, "", errDisabled
	}
	h.mu.Lock()
	if s, ok := h.sessions[sessionID]; ok {
		if s.TenantID != tenantID {
			h.mu.Unlock()
			return nil, "", errSessionGone // 不泄漏会话存在性
		}
	} else {
		if err := h.checkLimitsLocked(tenantID); err != nil {
			h.mu.Unlock()
			return nil, "", err
		}
		worker, err := h.pickWorkerLocked(tenantID)
		if err != nil {
			h.mu.Unlock()
			return nil, "", err
		}
		h.sessions[sessionID] = &Session{
			ID: sessionID, TenantID: tenantID, UserID: userID,
			WorkerID: worker, BaseURL: url,
			CreatedAt: time.Now(), LastActive: time.Now(),
		}
	}
	h.mu.Unlock()

	cmd := &workerv1.ProbeCommand{SessionId: sessionID, TenantId: tenantID}
	cmd.Op = &workerv1.ProbeCommand_Open{Open: &workerv1.ProbeOpen{
		Url:              url,
		SnapshotMaxBytes: h.cfg.SnapshotMaxBytes,
		Record:           false, // 探测不录 HAR/tracing
	}}
	reply, err := h.roundTrip(ctx, sessionID, cmd)
	if err != nil {
		return nil, "", err
	}
	state := reply.GetState()
	if state == nil {
		return nil, "", probeFailure(reply)
	}
	h.mu.Lock()
	if s := h.sessions[sessionID]; s != nil {
		s.LastActive = time.Now()
	}
	h.mu.Unlock()
	return state, h.workerOf(sessionID), nil
}

// Act 在会话上执行单步 UI 动作，返回动作后的页面快照（一步一反馈）。
func (h *Hub) Act(ctx context.Context, tenantID int64, sessionID string, act *commonv1.UiActionStep) (*workerv1.ProbeState, error) {
	s, err := h.require(tenantID, sessionID)
	if err != nil {
		return nil, err
	}
	cmd := &workerv1.ProbeCommand{SessionId: sessionID, TenantId: s.TenantID}
	cmd.Op = &workerv1.ProbeCommand_Act{Act: act}
	reply, err := h.roundTrip(ctx, sessionID, cmd)
	if err != nil {
		return nil, err
	}
	state := reply.GetState()
	if state == nil {
		return nil, probeFailure(reply)
	}
	return state, nil
}

// Snapshot 获取当前页面 ARIA 快照（ref 子树为 v1.x 预留，v1 仅全页）。
func (h *Hub) Snapshot(ctx context.Context, tenantID int64, sessionID, ref string) (*workerv1.ProbeState, error) {
	s, err := h.require(tenantID, sessionID)
	if err != nil {
		return nil, err
	}
	cmd := &workerv1.ProbeCommand{SessionId: sessionID, TenantId: s.TenantID}
	cmd.Op = &workerv1.ProbeCommand_Snapshot{Snapshot: &workerv1.ProbeSnapshot{
		Ref: ref, SnapshotMaxBytes: h.cfg.SnapshotMaxBytes}}
	reply, err := h.roundTrip(ctx, sessionID, cmd)
	if err != nil {
		return nil, err
	}
	state := reply.GetState()
	if state == nil {
		return nil, probeFailure(reply)
	}
	return state, nil
}

// Eval 在页面上下文执行 JS 表达式。
func (h *Hub) Eval(ctx context.Context, tenantID int64, sessionID, expression string) (*workerv1.ProbeEvalResult, error) {
	s, err := h.require(tenantID, sessionID)
	if err != nil {
		return nil, err
	}
	cmd := &workerv1.ProbeCommand{SessionId: sessionID, TenantId: s.TenantID}
	cmd.Op = &workerv1.ProbeCommand_Eval{Eval: &workerv1.ProbeEval{
		Expression: expression, ResultMaxBytes: h.cfg.EvalMaxBytes}}
	reply, err := h.roundTrip(ctx, sessionID, cmd)
	if err != nil {
		return nil, err
	}
	res := reply.GetEval()
	if res == nil {
		return nil, probeFailure(reply)
	}
	return res, nil
}

// Run 在会话常驻沙箱中执行一段 Python（v2 run_py；约定入口 async def run(ctx)）。
func (h *Hub) Run(ctx context.Context, tenantID int64, sessionID, source string) (*workerv1.ProbeRunResult, error) {
	s, err := h.require(tenantID, sessionID)
	if err != nil {
		return nil, err
	}
	cmd := &workerv1.ProbeCommand{SessionId: sessionID, TenantId: s.TenantID}
	cmd.Op = &workerv1.ProbeCommand_Run{Run: &workerv1.ProbeRun{
		Source: source, ResultMaxBytes: h.cfg.EvalMaxBytes,
	}}
	reply, err := h.roundTrip(ctx, sessionID, cmd)
	if err != nil {
		return nil, err
	}
	res := reply.GetRunResult()
	if res == nil {
		return nil, probeFailure(reply)
	}
	return res, nil
}

// Close 关闭会话（幂等：会话不存在视为已释放）。
func (h *Hub) Close(tenantID int64, sessionID, reason string) error {
	h.mu.Lock()
	s, ok := h.sessions[sessionID]
	if !ok {
		h.mu.Unlock()
		return nil // 幂等
	}
	if s.TenantID != tenantID {
		h.mu.Unlock()
		return errSessionGone
	}
	delete(h.sessions, sessionID)
	workerID := s.WorkerID
	h.mu.Unlock()

	cmd := &workerv1.ProbeCommand{SessionId: sessionID, TenantId: tenantID}
	cmd.Op = &workerv1.ProbeCommand_Close{Close: &workerv1.ProbeClose{Reason: reason}}
	ctx, cancel := context.WithTimeout(context.Background(), h.cfg.CmdTimeout)
	defer cancel()
	_, _ = h.roundTrip(ctx, sessionID, cmd) // 尽力而为；失败也无妨（Worker TTL 兜底）
	_ = workerID
	return nil
}

// Deliver 接收 Worker 上行的探测回执（grpcserver 收流循环调用），按 request_id 唤醒等待方。
// 迟到回执（pending 已清理）静默丢弃——caller 已超时返回。
func (h *Hub) Deliver(reply *workerv1.ProbeReply) {
	h.mu.Lock()
	p, ok := h.pending[reply.GetRequestId()]
	if ok {
		delete(h.pending, reply.GetRequestId())
		if p.kill != nil {
			p.kill.Stop()
		}
	}
	h.mu.Unlock()
	if ok {
		p.ch <- reply
	}
}

// OnWorkerDisconnect Worker 断连：其承载的探测会话全部失效。
func (h *Hub) OnWorkerDisconnect(workerID string) {
	h.mu.Lock()
	for id, s := range h.sessions {
		if s.WorkerID == workerID {
			delete(h.sessions, id)
		}
	}
	h.mu.Unlock()
}

// Sweep TTL 回收（main reaper 周期调用）：空闲超限发 close(ttl) 后删除；
// 生命周期超限直接删除（浏览器由 Worker 侧会话上限/进程回收兜底）。
func (h *Hub) Sweep() {
	now := time.Now()
	h.mu.Lock()
	expired := make([]*Session, 0, 2)
	for id, s := range h.sessions {
		if now.Sub(s.LastActive) > h.cfg.IdleTTL || now.Sub(s.CreatedAt) > h.cfg.MaxLifetime {
			expired = append(expired, s)
			delete(h.sessions, id)
		}
	}
	h.mu.Unlock()
	for _, s := range expired {
		cmd := &workerv1.ProbeCommand{SessionId: s.ID, TenantId: s.TenantID}
		cmd.Op = &workerv1.ProbeCommand_Close{Close: &workerv1.ProbeClose{Reason: "ttl"}}
		ctx, cancel := context.WithTimeout(context.Background(), h.cfg.CmdTimeout)
		_, _ = h.roundTrip(ctx, s.ID, cmd)
		cancel()
	}
}

// Sessions 当前会话数（metrics/测试用）。
func (h *Hub) Sessions() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.sessions)
}

// ---- 内部 ----

func (h *Hub) require(tenantID int64, sessionID string) (*Session, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.sessions[sessionID]
	if !ok || s.TenantID != tenantID {
		return nil, errSessionGone
	}
	s.LastActive = time.Now()
	return s, nil
}

func (h *Hub) workerOf(sessionID string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessions[sessionID].WorkerID
}

func (h *Hub) checkLimitsLocked(tenantID int64) error {
	perTenant := 0
	for _, s := range h.sessions {
		if s.TenantID == tenantID {
			perTenant++
		}
	}
	if perTenant >= h.cfg.MaxPerTenant {
		return errLimit
	}
	return nil
}

// pickWorkerLocked 选择承载 Worker：PLAYWRIGHT 能力 + 非压测 + 租户匹配，取最低负载。
// per-worker 限额在选定后校验（因为按 WorkerID 计数）。
func (h *Hub) pickWorkerLocked(tenantID int64) (string, error) {
	if h.disp == nil {
		return "", errNoWorker
	}
	cands := h.disp.Workers()
	var best *dispatch.Worker
	for _, w := range cands {
		ok := false
		for _, c := range w.Capabilities {
			if c == capabilityPlaywright {
				ok = true
				break
			}
		}
		if !ok || w.InStress() {
			continue
		}
		if w.TenantID != 0 && w.TenantID != tenantID {
			continue
		}
		if best == nil || w.Load() < best.Load() {
			// per-worker 会话数校验：候选满了就跳过
			if h.countOnLocked(w.ID) >= h.cfg.MaxPerWorker {
				continue
			}
			best = w
		}
	}
	if best == nil {
		return "", errNoWorker
	}
	return best.ID, nil
}

func (h *Hub) countOnLocked(workerID string) int {
	n := 0
	for _, s := range h.sessions {
		if s.WorkerID == workerID {
			n++
		}
	}
	return n
}

// roundTrip 发命令 + 等回执（pending 配对 + 超时）。
func (h *Hub) roundTrip(ctx context.Context, sessionID string, cmd *workerv1.ProbeCommand) (*workerv1.ProbeReply, error) {
	h.mu.Lock()
	s, ok := h.sessions[sessionID]
	if !ok {
		h.mu.Unlock()
		return nil, errSessionGone
	}
	w := h.findWorkerLocked(s.WorkerID)
	if w == nil {
		delete(h.sessions, sessionID)
		h.mu.Unlock()
		return nil, errSessionGone // Worker 已断连，会话随之下线
	}
	cmd.RequestId = "p" + strconv.FormatInt(model.NextID(), 36)
	ch := make(chan *workerv1.ProbeReply, 1)
	p := &pendingCall{ch: ch}
	h.pending[cmd.RequestId] = p
	timeout := time.NewTimer(h.cfg.CmdTimeout)
	p.kill = timeout
	h.mu.Unlock()

	defer timeout.Stop()
	defer func() {
		h.mu.Lock()
		delete(h.pending, cmd.RequestId)
		h.mu.Unlock()
	}()

	cmd.Timeout = durationpb.New(h.cfg.CmdTimeout)
	select {
	case w.Send <- &workerv1.SchedulerCommand{Command: &workerv1.SchedulerCommand_Probe{Probe: cmd}}:
	case <-w.Closed():
		return nil, errSessionGone
	case <-ctx.Done():
		return nil, errSessionGone
	}

	select {
	case reply := <-ch:
		return reply, nil
	case <-timeout.C:
		return nil, apperr.New(504, apperr.CodeProbeTimeout, "probe command timeout")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (h *Hub) findWorkerLocked(id string) *dispatch.Worker {
	for _, w := range h.disp.Workers() {
		if w.ID == id {
			return w
		}
	}
	return nil
}

// probeFailure 把 Worker 失败回执转成 apperr（gRPC handler 层映射为 status error）。
func probeFailure(reply *workerv1.ProbeReply) error {
	f := reply.GetFailure()
	if f == nil {
		return apperr.New(500, apperr.CodeProbeFailed, "unexpected probe reply payload")
	}
	switch f.GetCode() {
	case apperr.CodeProbeTimeout:
		return apperr.New(504, apperr.CodeProbeTimeout, f.GetMessage())
	case apperr.CodeProbeSessionGone:
		return errSessionGone
	case apperr.CodeProbeLimit:
		return errLimit
	default:
		return apperr.New(500, apperr.CodeProbeFailed, f.GetMessage())
	}
}
