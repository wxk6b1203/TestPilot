package grpcserver

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	copilotv1 "github.com/testpilot/testpilot/gen/copilot/v1"
	"github.com/testpilot/testpilot/internal/impexp"
	"github.com/testpilot/testpilot/internal/model"
	"github.com/testpilot/testpilot/internal/probe"
	"github.com/testpilot/testpilot/internal/quota"
	"github.com/testpilot/testpilot/internal/runner"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

//go:embed schema.json
var domainSchema string

// CopilotService 实现 CopilotToolService：Copilot 的工具面（不直连 DB 的是 Copilot 侧；
// 本服务在 Scheduler 内，受 RequestContext 租户约束；写/触发类落审计）。
type CopilotService struct {
	copilotv1.UnimplementedCopilotToolServiceServer
	db    *gorm.DB
	run   *runner.Runner
	probe *probe.Hub // nil = 探测功能关闭（probe_enabled=false）
}

func NewCopilotService(db *gorm.DB, run *runner.Runner, probe *probe.Hub) *CopilotService {
	return &CopilotService{db: db, run: run, probe: probe}
}

// ---- 通用工具 ----

func tid(ctx *commonv1.RequestContext) int64 { return ctx.GetTenantId() }

func idStr(v int64) string { return fmt.Sprint(v) }

func mustID(s string) int64 {
	var x int64
	fmt.Sscan(s, &x)
	return x
}

func pageOf(p *commonv1.PageRequest) (int, int) {
	page, size := int(p.GetPage()), int(p.GetPageSize())
	if page <= 0 {
		page = 1
	}
	if size <= 0 || size > 500 {
		size = 50
	}
	return page, size
}

func pageResp(total, page, size int) *commonv1.PageResponse {
	return &commonv1.PageResponse{Total: int32(total), Page: int32(page), PageSize: int32(size)}
}

// audit 写工具调用审计（actor=copilot；HITL 审批人=会话用户）。
func (s *CopilotService) audit(ctx *commonv1.RequestContext, action, resType, resID string, detail any) {
	detailJSON, _ := json.Marshal(detail)
	row := &model.AuditLog{
		ID:           model.NextID(),
		TenantID:     tid(ctx),
		Actor:        2, // copilot
		ActorID:      ctx.GetUserId(),
		Action:       action,
		ResourceType: resType,
		ResourceID:   resID,
		ApprovedBy:   ctx.GetUserId(), // Copilot 层 HITL 审批后才调用写工具
		Detail:       model.JSON(detailJSON),
	}
	s.db.Create(row)
}

// checkAICalls 写/触发 RPC 前置：ai_calls 月度配额（超限 → ResourceExhausted）。
func (s *CopilotService) checkAICalls(ctx *commonv1.RequestContext) error {
	if err := quota.Check(s.db, tid(ctx), quota.MetricAICalls, 1); err != nil {
		return status.Error(codes.ResourceExhausted, err.Error())
	}
	return nil
}

// ---- model → proto 转换 ----

func kvToJSON(kvs []*commonv1.KeyValue) model.JSON {
	if len(kvs) == 0 {
		return nil
	}
	list := make([]map[string]string, 0, len(kvs))
	for _, kv := range kvs {
		list = append(list, map[string]string{"key": kv.GetKey(), "value": kv.GetValue()})
	}
	b, _ := json.Marshal(list)
	return model.JSON(b)
}

func jsonToStruct(raw model.JSON) *structpb.Struct {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	s, err := structpb.NewStruct(m)
	if err != nil {
		return nil
	}
	return s
}

func runSummary(raw model.JSON) *commonv1.RunSummary {
	if len(raw) == 0 {
		return nil
	}
	var m struct {
		Total   int32 `json:"total"`
		Passed  int32 `json:"passed"`
		Failed  int32 `json:"failed"`
		Skipped int32 `json:"skipped"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return &commonv1.RunSummary{Total: m.Total, Passed: m.Passed, Failed: m.Failed, Skipped: m.Skipped}
}

func runToProto(r *model.TestRun) *commonv1.TestRun {
	out := &commonv1.TestRun{
		Id:          idStr(r.ID),
		TenantId:    r.TenantID,
		PlanId:      idStr(r.PlanID),
		EnvId:       idStr(r.EnvID),
		Status:      commonv1.RunStatus(r.Status),
		Trigger:     commonv1.TriggerType(r.Trigger),
		TriggeredBy: r.TriggeredBy,
		StartedAt:   timestamppb.New(r.StartedAt),
		Summary:     runSummary(r.Summary),
	}
	if r.FinishedAt != nil {
		out.FinishedAt = timestamppb.New(*r.FinishedAt)
	}
	return out
}

func caseResultToProto(cr *model.TestCaseResult) *commonv1.TestCaseResult {
	return &commonv1.TestCaseResult{
		Id:       idStr(cr.ID),
		RunId:    idStr(cr.RunID),
		CaseId:   idStr(cr.CaseID),
		Status:   commonv1.CaseStatus(cr.Status),
		Duration: durationpb.New(time.Duration(cr.DurationMs) * time.Millisecond),
		Error:    cr.Error,
	}
}

func stepResultToProto(sr *model.TestStepResult) *commonv1.TestStepResult {
	var logs []string
	json.Unmarshal(sr.Logs, &logs)
	return &commonv1.TestStepResult{
		Id:           idStr(sr.ID),
		CaseResultId: idStr(sr.CaseResultID),
		StepPath:     sr.StepPath,
		Status:       commonv1.StepStatus(sr.Status),
		Duration:     durationpb.New(time.Duration(sr.DurationMs) * time.Millisecond),
		Request:      jsonToStruct(sr.Request),
		Response:     jsonToStruct(sr.Response),
		Logs:         logs,
	}
}

// ---- 只读工具 ----

func (s *CopilotService) ListProjects(_ context.Context, req *copilotv1.ListProjectsRequest) (*copilotv1.ListProjectsResponse, error) {
	page, size := pageOf(req.GetPage())
	q := s.db.Model(&model.Project{}).Where("tenant_id = ?", tid(req.GetCtx()))
	if qstr := strings.TrimSpace(req.GetQuery()); qstr != "" {
		q = q.Where("name LIKE ?", "%"+qstr+"%")
	}
	var total int64
	q.Count(&total)
	var rows []model.Project
	q.Order("id desc").Offset((page - 1) * size).Limit(size).Find(&rows)
	out := &copilotv1.ListProjectsResponse{Page: pageResp(int(total), page, size)}
	for _, p := range rows {
		out.Projects = append(out.Projects, &commonv1.Project{
			Id: idStr(p.ID), TenantId: p.TenantID, Name: p.Name, Description: p.Description,
			CreatedAt: timestamppb.New(p.CreatedAt), UpdatedAt: timestamppb.New(p.UpdatedAt),
		})
	}
	return out, nil
}

func (s *CopilotService) ListApis(_ context.Context, req *copilotv1.ListApisRequest) (*copilotv1.ListApisResponse, error) {
	page, size := pageOf(req.GetPage())
	tenant := tid(req.GetCtx())
	pid := mustID(req.GetProjectId())
	qstr := strings.TrimSpace(req.GetQuery())
	out := &copilotv1.ListApisResponse{}

	httpQ := s.db.Model(&model.HttpApi{}).Where("tenant_id = ?", tenant)
	grpcQ := s.db.Model(&model.GrpcApi{}).Where("tenant_id = ?", tenant)
	if pid != 0 {
		httpQ = httpQ.Where("project_id = ?", pid)
		grpcQ = grpcQ.Where("project_id = ?", pid)
	}
	if qstr != "" {
		httpQ = httpQ.Where("uri LIKE ?", "%"+qstr+"%")
		grpcQ = grpcQ.Where("(full_service LIKE ? OR method LIKE ?)", "%"+qstr+"%", "%"+qstr+"%")
	}
	var httpTotal, grpcTotal int64
	httpQ.Count(&httpTotal)
	grpcQ.Count(&grpcTotal)
	out.Page = pageResp(int(httpTotal+grpcTotal), page, size)

	var rows []model.HttpApi
	httpQ.Order("id desc").Offset((page - 1) * size).Limit(size).Find(&rows)
	for i := range rows {
		out.HttpApis = append(out.HttpApis, runner.ToProtoHTTP(&rows[i]))
	}
	var grows []model.GrpcApi
	grpcQ.Order("id desc").Offset((page - 1) * size).Limit(size).Find(&grows)
	for i := range grows {
		out.GrpcApis = append(out.GrpcApis, runner.ToProtoGrpc(&grows[i]))
	}
	return out, nil
}

func (s *CopilotService) GetApi(_ context.Context, req *copilotv1.GetApiRequest) (*copilotv1.GetApiResponse, error) {
	tenant := tid(req.GetCtx())
	apiID := mustID(req.GetApiId())
	// kind 未指定时先按 HTTP 查，未命中再回退 gRPC（兼容旧调用方）。
	if req.GetKind() != copilotv1.ApiKind_API_KIND_GRPC {
		var m model.HttpApi
		if err := s.db.Where("id = ? AND tenant_id = ?", apiID, tenant).First(&m).Error; err == nil {
			return &copilotv1.GetApiResponse{Api: &copilotv1.GetApiResponse_Http{Http: runner.ToProtoHTTP(&m)}}, nil
		} else if req.GetKind() == copilotv1.ApiKind_API_KIND_HTTP {
			return nil, status.Error(codes.NotFound, "api not found")
		}
	}
	var m model.GrpcApi
	if err := s.db.Where("id = ? AND tenant_id = ?", apiID, tenant).First(&m).Error; err != nil {
		return nil, status.Error(codes.NotFound, "api not found")
	}
	return &copilotv1.GetApiResponse{Api: &copilotv1.GetApiResponse_Grpc{Grpc: runner.ToProtoGrpc(&m)}}, nil
}

func (s *CopilotService) ListEnvironments(_ context.Context, req *copilotv1.ListEnvironmentsRequest) (*copilotv1.ListEnvironmentsResponse, error) {
	q := s.db.Where("tenant_id = ?", tid(req.GetCtx()))
	if pid := mustID(req.GetProjectId()); pid != 0 {
		q = q.Where("project_id = ?", pid)
	}
	var rows []model.Environment
	q.Order("id asc").Find(&rows)
	out := &copilotv1.ListEnvironmentsResponse{}
	for i := range rows {
		out.Environments = append(out.Environments, runner.ToProtoEnvironment(&rows[i]))
	}
	return out, nil
}

func (s *CopilotService) ListTestCases(_ context.Context, req *copilotv1.ListTestCasesRequest) (*copilotv1.ListTestCasesResponse, error) {
	page, size := pageOf(req.GetPage())
	q := s.db.Model(&model.TestCase{}).Where("tenant_id = ?", tid(req.GetCtx()))
	if pid := mustID(req.GetProjectId()); pid != 0 {
		q = q.Where("project_id = ?", pid)
	}
	if qstr := strings.TrimSpace(req.GetQuery()); qstr != "" {
		q = q.Where("name LIKE ?", "%"+qstr+"%")
	}
	var total int64
	q.Count(&total)
	var rows []model.TestCase
	q.Order("id desc").Offset((page - 1) * size).Limit(size).Find(&rows)
	out := &copilotv1.ListTestCasesResponse{Page: pageResp(int(total), page, size)}
	for i := range rows {
		out.Cases = append(out.Cases, runner.ToProtoCase(&rows[i]))
	}
	return out, nil
}

func (s *CopilotService) GetTestCase(_ context.Context, req *copilotv1.GetTestCaseRequest) (*copilotv1.GetTestCaseResponse, error) {
	var m model.TestCase
	if err := s.db.Where("id = ? AND tenant_id = ?", mustID(req.GetCaseId()), tid(req.GetCtx())).First(&m).Error; err != nil {
		return nil, status.Error(codes.NotFound, "case not found")
	}
	return &copilotv1.GetTestCaseResponse{Case: runner.ToProtoCase(&m)}, nil
}

func (s *CopilotService) QuerySchema(_ context.Context, req *copilotv1.QuerySchemaRequest) (*copilotv1.QuerySchemaResponse, error) {
	return &copilotv1.QuerySchemaResponse{SchemaJson: domainSchema, Version: "v1"}, nil
}

func (s *CopilotService) ListRuns(_ context.Context, req *copilotv1.ListRunsRequest) (*copilotv1.ListRunsResponse, error) {
	page, size := pageOf(req.GetPage())
	q := s.db.Model(&model.TestRun{}).Where("tenant_id = ?", tid(req.GetCtx()))
	if pid := mustID(req.GetProjectId()); pid != 0 {
		q = q.Where("plan_id IN (?)", s.db.Model(&model.TestPlan{}).Select("id").
			Where("project_id = ? AND tenant_id = ?", pid, tid(req.GetCtx())))
	}
	if planID := mustID(req.GetPlanId()); planID != 0 {
		q = q.Where("plan_id = ?", planID)
	}
	if req.GetStatus() != commonv1.RunStatus_RUN_STATUS_UNSPECIFIED {
		q = q.Where("status = ?", int16(req.GetStatus()))
	}
	var total int64
	q.Count(&total)
	var rows []model.TestRun
	q.Order("id desc").Offset((page - 1) * size).Limit(size).Find(&rows)
	out := &copilotv1.ListRunsResponse{Page: pageResp(int(total), page, size)}
	for i := range rows {
		out.Runs = append(out.Runs, runToProto(&rows[i]))
	}
	return out, nil
}

func (s *CopilotService) GetRun(_ context.Context, req *copilotv1.GetRunRequest) (*copilotv1.GetRunResponse, error) {
	var run model.TestRun
	if err := s.db.Where("id = ? AND tenant_id = ?", mustID(req.GetRunId()), tid(req.GetCtx())).First(&run).Error; err != nil {
		return nil, status.Error(codes.NotFound, "run not found")
	}
	out := &copilotv1.GetRunResponse{Run: runToProto(&run)}
	var crs []model.TestCaseResult
	s.db.Where("run_id = ? AND tenant_id = ?", run.ID, run.TenantID).Order("id asc").Find(&crs)
	for i := range crs {
		out.CaseResults = append(out.CaseResults, caseResultToProto(&crs[i]))
	}
	if req.GetIncludeSteps() && len(crs) > 0 {
		ids := make([]int64, 0, len(crs))
		for _, cr := range crs {
			ids = append(ids, cr.ID)
		}
		var steps []model.TestStepResult
		s.db.Where("case_result_id IN ? AND tenant_id = ?", ids, run.TenantID).Order("id asc").Find(&steps)
		for i := range steps {
			out.StepResults = append(out.StepResults, stepResultToProto(&steps[i]))
		}
	}
	return out, nil
}

func (s *CopilotService) QueryCoverage(_ context.Context, req *copilotv1.QueryCoverageRequest) (*copilotv1.QueryCoverageResponse, error) {
	t := tid(req.GetCtx())
	pid := mustID(req.GetProjectId())
	var apis []model.HttpApi
	s.db.Select("id", "method", "uri").Where("tenant_id = ? AND project_id = ?", t, pid).Find(&apis)
	var cases []model.TestCase
	s.db.Select("id", "definition").Where("tenant_id = ? AND project_id = ?", t, pid).Find(&cases)

	// 覆盖判定：接口 (method+uri) 出现在任一用例 definition 的 api_call.inline 中
	type apiKey struct {
		Method int16
		URI    string
	}
	covered := map[apiKey]bool{}
	for _, c := range cases {
		dc := &commonv1.DeclarativeCase{}
		if err := protojson.Unmarshal([]byte(c.Definition), dc); err != nil {
			continue
		}
		var walk func(steps []*commonv1.TestStep)
		walk = func(steps []*commonv1.TestStep) {
			for _, st := range steps {
				if ac := st.GetApiCall(); ac != nil && ac.GetInline() != nil {
					covered[apiKey{int16(ac.GetInline().GetMethod()), ac.GetInline().GetUri()}] = true
				}
				walk(st.GetIfStep().GetThenSteps())
				walk(st.GetIfStep().GetElseSteps())
				walk(st.GetLoopStep().GetBodySteps())
				if st.GetRetryStep().GetBodyStep() != nil {
					walk([]*commonv1.TestStep{st.GetRetryStep().GetBodyStep()})
				}
			}
		}
		walk(dc.GetSteps())
	}
	out := &copilotv1.QueryCoverageResponse{TotalApis: int32(len(apis))}
	for _, a := range apis {
		if covered[apiKey{a.Method, a.URI}] {
			out.CoveredApis++
		} else {
			out.UncoveredApiIds = append(out.UncoveredApiIds, idStr(a.ID))
		}
	}
	if out.TotalApis > 0 {
		out.CoverageRatio = float64(out.CoveredApis) / float64(out.TotalApis)
	}
	return out, nil
}

// ensureProjectTenant 校验 project 属于该租户（写面防跨租户孤儿行：
// LLM 提供的 project_id 可指向他租户项目，挂进去即污染隔离视图）。
func (s *CopilotService) ensureProjectTenant(rc *commonv1.RequestContext, projectID int64) error {
	var n int64
	if err := s.db.Model(&model.Project{}).Where("id = ? AND tenant_id = ?", projectID, rc.GetTenantId()).
		Count(&n).Error; err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	if n == 0 {
		return status.Error(codes.NotFound, "project not found in tenant")
	}
	return nil
}

// ---- 写工具（HITL 审批后由 Copilot 调用；全部落审计）----

func (s *CopilotService) CreateApi(_ context.Context, req *copilotv1.CreateApiRequest) (*copilotv1.CreateApiResponse, error) {
	if err := s.checkAICalls(req.GetCtx()); err != nil {
		return nil, err
	}
	if pid := mustID(req.GetProjectId()); pid != 0 {
		if err := s.ensureProjectTenant(req.GetCtx(), pid); err != nil {
			return nil, err
		}
	}
	switch spec := req.GetApi().(type) {
	case *copilotv1.CreateApiRequest_Http:
		m := &model.HttpApi{
			ID:        model.NextID(),
			TenantID:  tid(req.GetCtx()),
			ProjectID: mustID(req.GetProjectId()),
			Method:    int16(spec.Http.GetMethod()),
			URI:       spec.Http.GetUri(),
			Params:    kvToJSON(spec.Http.GetParams()),
			Headers:   kvToJSON(spec.Http.GetHeaders()),
		}
		if b := spec.Http.GetBody(); b != nil {
			if raw, err := protojson.Marshal(b); err == nil {
				m.Body = model.JSON(raw)
			}
		}
		if m.ProjectID == 0 || m.URI == "" {
			return nil, status.Error(codes.InvalidArgument, "project_id and uri required")
		}
		if err := s.db.Create(m).Error; err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		s.audit(req.GetCtx(), "create", "http_api", idStr(m.ID), map[string]any{"method": m.Method, "uri": m.URI})
		return &copilotv1.CreateApiResponse{ApiId: idStr(m.ID)}, nil

	case *copilotv1.CreateApiRequest_Grpc:
		g := spec.Grpc
		m := &model.GrpcApi{
			ID:          model.NextID(),
			TenantID:    tid(req.GetCtx()),
			ProjectID:   mustID(req.GetProjectId()),
			FullService: g.GetFullService(),
			Method:      g.GetMethod(),
			Metadata:    kvToJSON(g.GetMetadata()),
		}
		if g.GetRequestMessage() != nil {
			if raw, err := protojson.Marshal(g.GetRequestMessage()); err == nil {
				m.RequestMessage = model.JSON(raw)
			}
		}
		if d := g.GetDeadline(); d != nil {
			m.DeadlineMs = int(d.AsDuration().Milliseconds())
		}
		if m.ProjectID == 0 || m.FullService == "" || m.Method == "" {
			return nil, status.Error(codes.InvalidArgument, "project_id, full_service and method required")
		}
		if err := s.db.Create(m).Error; err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		s.audit(req.GetCtx(), "create", "grpc_api", idStr(m.ID),
			map[string]any{"service": m.FullService, "method": m.Method})
		return &copilotv1.CreateApiResponse{ApiId: idStr(m.ID)}, nil

	default:
		return nil, status.Error(codes.InvalidArgument, "http or grpc api payload required")
	}
}

func (s *CopilotService) UpdateApi(_ context.Context, req *copilotv1.UpdateApiRequest) (*copilotv1.UpdateApiResponse, error) {
	if err := s.checkAICalls(req.GetCtx()); err != nil {
		return nil, err
	}
	tenant := tid(req.GetCtx())
	apiID := mustID(req.GetApiId())
	switch spec := req.GetApi().(type) {
	case *copilotv1.UpdateApiRequest_Http:
		h := spec.Http
		var m model.HttpApi
		if err := s.db.Where("id = ? AND tenant_id = ?", apiID, tenant).First(&m).Error; err != nil {
			return nil, status.Error(codes.NotFound, "api not found")
		}
		m.Method = int16(h.GetMethod())
		m.URI = h.GetUri()
		m.Params = kvToJSON(h.GetParams())
		m.Headers = kvToJSON(h.GetHeaders())
		if b := h.GetBody(); b != nil {
			if raw, err := protojson.Marshal(b); err == nil {
				m.Body = model.JSON(raw)
			}
		}
		if err := s.db.Save(&m).Error; err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		s.audit(req.GetCtx(), "update", "http_api", idStr(m.ID), map[string]any{"method": m.Method, "uri": m.URI})
		return &copilotv1.UpdateApiResponse{ApiId: idStr(m.ID)}, nil

	case *copilotv1.UpdateApiRequest_Grpc:
		g := spec.Grpc
		var m model.GrpcApi
		if err := s.db.Where("id = ? AND tenant_id = ?", apiID, tenant).First(&m).Error; err != nil {
			return nil, status.Error(codes.NotFound, "api not found")
		}
		m.FullService = g.GetFullService()
		m.Method = g.GetMethod()
		m.Metadata = kvToJSON(g.GetMetadata())
		if g.GetRequestMessage() != nil {
			if raw, err := protojson.Marshal(g.GetRequestMessage()); err == nil {
				m.RequestMessage = model.JSON(raw)
			}
		}
		if d := g.GetDeadline(); d != nil {
			m.DeadlineMs = int(d.AsDuration().Milliseconds())
		}
		if t := g.GetTlsSettings(); t != nil {
			if raw, err := protojson.Marshal(t); err == nil {
				m.TlsSettings = model.JSON(raw)
			}
		}
		if err := s.db.Save(&m).Error; err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		s.audit(req.GetCtx(), "update", "grpc_api", idStr(m.ID),
			map[string]any{"service": m.FullService, "method": m.Method})
		return &copilotv1.UpdateApiResponse{ApiId: idStr(m.ID)}, nil

	default:
		return nil, status.Error(codes.InvalidArgument, "http or grpc api payload required")
	}
}

func (s *CopilotService) CreateTestCase(_ context.Context, req *copilotv1.CreateTestCaseRequest) (*copilotv1.CreateTestCaseResponse, error) {
	if err := s.checkAICalls(req.GetCtx()); err != nil {
		return nil, err
	}
	if pid := mustID(req.GetProjectId()); pid != 0 {
		if err := s.ensureProjectTenant(req.GetCtx(), pid); err != nil {
			return nil, err
		}
	}
	c := req.GetCase()
	if c == nil {
		return nil, status.Error(codes.InvalidArgument, "case required")
	}
	m := &model.TestCase{
		ID:          model.NextID(),
		TenantID:    tid(req.GetCtx()),
		ProjectID:   mustID(req.GetProjectId()),
		Type:        int16(c.GetType()),
		Name:        c.GetName(),
		Description: c.GetDescription(),
		CreatedBy:   2, // copilot
	}
	switch d := c.GetDefinition().(type) {
	case *commonv1.TestCase_Declarative:
		raw, err := protojson.Marshal(d.Declarative)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid declarative definition")
		}
		m.Definition = model.JSON(raw)
	case *commonv1.TestCase_Lowcode:
		raw, err := protojson.Marshal(d.Lowcode)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid lowcode definition")
		}
		m.Definition = model.JSON(raw)
	default:
		return nil, status.Error(codes.InvalidArgument, "case definition required")
	}
	if m.ProjectID == 0 || m.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "project_id and name required")
	}
	if err := s.db.Create(m).Error; err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	s.audit(req.GetCtx(), "create", "test_case", idStr(m.ID), map[string]any{"name": m.Name, "type": m.Type})
	return &copilotv1.CreateTestCaseResponse{CaseId: idStr(m.ID)}, nil
}

func (s *CopilotService) UpdateTestCase(_ context.Context, req *copilotv1.UpdateTestCaseRequest) (*copilotv1.UpdateTestCaseResponse, error) {
	if err := s.checkAICalls(req.GetCtx()); err != nil {
		return nil, err
	}
	caseID := mustID(req.GetCaseId())
	if caseID == 0 {
		return nil, status.Error(codes.InvalidArgument, "case_id required")
	}
	c := req.GetCase()
	if c == nil {
		return nil, status.Error(codes.InvalidArgument, "case required")
	}
	tenant := tid(req.GetCtx())
	var m model.TestCase
	if err := s.db.Where("id = ? AND tenant_id = ?", caseID, tenant).First(&m).Error; err != nil {
		return nil, status.Error(codes.NotFound, "case not found")
	}
	if c.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "case name required")
	}
	// Copilot 工具侧总是把当前 type 随合并后的完整定义传回；
	// 显式不一致按错误处理，避免静默改变用例类型导致 definition 解析歧义。
	if t := c.GetType(); t != commonv1.TestCaseType_TEST_CASE_TYPE_UNSPECIFIED &&
		int16(t) != m.Type {
		return nil, status.Error(codes.InvalidArgument,
			"changing test case type is not supported; create a new case instead")
	}
	var raw []byte
	switch d := c.GetDefinition().(type) {
	case *commonv1.TestCase_Declarative:
		if m.Type != int16(commonv1.TestCaseType_TEST_CASE_TYPE_DECLARATIVE) {
			return nil, status.Error(codes.InvalidArgument, "definition kind does not match existing case type")
		}
		var err error
		raw, err = protojson.Marshal(d.Declarative)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid declarative definition")
		}
	case *commonv1.TestCase_Lowcode:
		if m.Type != int16(commonv1.TestCaseType_TEST_CASE_TYPE_LOWCODE) {
			return nil, status.Error(codes.InvalidArgument, "definition kind does not match existing case type")
		}
		var err error
		raw, err = protojson.Marshal(d.Lowcode)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid lowcode definition")
		}
	default:
		return nil, status.Error(codes.InvalidArgument, "case definition required")
	}
	m.Name = c.GetName()
	m.Description = c.GetDescription()
	m.Definition = model.JSON(raw)
	if err := s.db.Save(&m).Error; err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	s.audit(req.GetCtx(), "update", "test_case", idStr(m.ID),
		map[string]any{"name": m.Name, "type": m.Type})
	return &copilotv1.UpdateTestCaseResponse{CaseId: idStr(m.ID)}, nil
}

func (s *CopilotService) CreateTestPlan(_ context.Context, req *copilotv1.CreateTestPlanRequest) (*copilotv1.CreateTestPlanResponse, error) {
	if err := s.checkAICalls(req.GetCtx()); err != nil {
		return nil, err
	}
	if pid := mustID(req.GetProjectId()); pid != 0 {
		if err := s.ensureProjectTenant(req.GetCtx(), pid); err != nil {
			return nil, err
		}
	}
	p := req.GetPlan()
	if p == nil {
		return nil, status.Error(codes.InvalidArgument, "plan required")
	}
	m := &model.TestPlan{
		ID:          model.NextID(),
		TenantID:    tid(req.GetCtx()),
		ProjectID:   mustID(req.GetProjectId()),
		EnvID:       mustID(p.GetEnvId()),
		Name:        p.GetName(),
		Concurrency: int(p.GetConcurrency()),
		TimeoutMs:   int(p.GetTimeout().AsDuration().Milliseconds()),
	}
	if m.ProjectID == 0 || m.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "project_id and name required")
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(m).Error; err != nil {
			return err
		}
		for i, item := range p.GetItems() {
			row := &model.TestPlanItem{
				ID:       model.NextID(),
				TenantID: m.TenantID,
				PlanID:   m.ID,
				Enabled:  item.GetEnabled(),
				Order:    i + 1,
			}
			switch ref := item.GetRef().(type) {
			case *commonv1.PlanItem_CaseId:
				row.RefType = 1
				row.RefID = mustID(ref.CaseId)
			case *commonv1.PlanItem_SuiteId:
				row.RefType = 2
				row.RefID = mustID(ref.SuiteId)
			}
			if row.RefID == 0 {
				continue
			}
			if err := tx.Create(row).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	s.audit(req.GetCtx(), "create", "test_plan", idStr(m.ID), map[string]any{"name": m.Name, "items": len(p.GetItems())})
	return &copilotv1.CreateTestPlanResponse{PlanId: idStr(m.ID)}, nil
}

func (s *CopilotService) ImportOpenApi(_ context.Context, req *copilotv1.ImportOpenApiRequest) (*copilotv1.ImportOpenApiResponse, error) {
	if err := s.checkAICalls(req.GetCtx()); err != nil {
		return nil, err
	}
	doc := req.GetOpenapiDocument()
	source := "document"
	if doc == "" {
		if u := req.GetOpenapiUrl(); u != "" {
			raw, err := fetchOpenAPIURL(u)
			if err != nil {
				return nil, status.Error(codes.InvalidArgument, "fetch openapi_url: "+err.Error())
			}
			doc = string(raw)
			source = u
		}
	}
	if doc == "" {
		return nil, status.Error(codes.InvalidArgument, "openapi_document or openapi_url required")
	}
	projectID := mustID(req.GetProjectId())
	if projectID == 0 {
		return nil, status.Error(codes.InvalidArgument, "project_id required")
	}
	res, err := impexp.ImportOpenAPI(s.db, tid(req.GetCtx()), projectID, []byte(doc))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "import failed: "+err.Error())
	}
	out := &copilotv1.ImportOpenApiResponse{ImportedCount: int32(len(res.APIIDs))}
	for _, id := range res.APIIDs {
		out.ApiIds = append(out.ApiIds, idStr(id))
	}
	s.audit(req.GetCtx(), "import_openapi", "http_api", "", map[string]any{"project_id": projectID, "count": len(res.APIIDs), "source": source})
	return out, nil
}

// lookupIP 域名解析（测试可替换，隔离私网防护与真实连接）。
var lookupIP = net.DefaultResolver.LookupIPAddr

// fetchOpenAPIURL 拉取 OpenAPI 文档（15s 超时、≤16MB）。SSRF 防护：拒绝
// 解析到环回/私网/链路本地地址的 host（Copilot 可被诱导访问内网元数据端点）。
func fetchOpenAPIURL(rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, fmt.Errorf("invalid url (http/https only)")
	}
	ips, err := lookupIP(context.Background(), u.Hostname())
	if err != nil {
		return nil, fmt.Errorf("resolve host: %w", err)
	}
	for _, ipa := range ips {
		ip := ipa.IP
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return nil, fmt.Errorf("host %s resolves to private/loopback address", u.Hostname())
		}
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("upstream returned %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
}

func (s *CopilotService) ApplyOpenApiDiff(_ context.Context, req *copilotv1.ApplyOpenApiDiffRequest) (*copilotv1.ApplyOpenApiDiffResponse, error) {
	if err := s.checkAICalls(req.GetCtx()); err != nil {
		return nil, err
	}
	if req.GetOpenapiDocument() == "" {
		return nil, status.Error(codes.InvalidArgument, "openapi_document required")
	}
	projectID := mustID(req.GetProjectId())
	if projectID == 0 {
		return nil, status.Error(codes.InvalidArgument, "project_id required")
	}
	res, err := impexp.ApplyOpenAPIDiff(s.db, tid(req.GetCtx()), projectID,
		[]byte(req.GetOpenapiDocument()), req.GetAutoUpdateCases())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "apply diff failed: "+err.Error())
	}
	out := &copilotv1.ApplyOpenApiDiffResponse{}
	for _, d := range res.Diffs {
		out.Diffs = append(out.Diffs, &copilotv1.DiffEntry{ApiId: idStr(d.APIID), Kind: d.Kind, Summary: d.Summary})
	}
	for _, id := range res.UpdatedAPIIDs {
		out.UpdatedApiIds = append(out.UpdatedApiIds, idStr(id))
	}
	for _, id := range res.UpdatedCaseIDs {
		out.UpdatedCaseIds = append(out.UpdatedCaseIds, idStr(id))
	}
	s.audit(req.GetCtx(), "apply_openapi_diff", "http_api", "", map[string]any{
		"project_id": projectID, "diffs": len(res.Diffs),
		"auto_update_cases": req.GetAutoUpdateCases()})
	return out, nil
}

// ---- 触发工具 ----

func (s *CopilotService) TriggerRun(ctx context.Context, req *copilotv1.TriggerRunRequest) (*copilotv1.TriggerRunResponse, error) {
	if err := s.checkAICalls(req.GetCtx()); err != nil {
		return nil, err
	}
	runID, err := s.run.Trigger(ctx, tid(req.GetCtx()), mustID(req.GetPlanId()), mustID(req.GetEnvId()),
		int16(commonv1.TriggerType_TRIGGER_TYPE_MANUAL), "copilot:"+req.GetCtx().GetUserId())
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	s.audit(req.GetCtx(), "run", "test_plan", req.GetPlanId(), map[string]any{"run_id": runID})
	return &copilotv1.TriggerRunResponse{RunId: idStr(runID)}, nil
}

func (s *CopilotService) TriggerStress(ctx context.Context, req *copilotv1.TriggerStressRequest) (*copilotv1.TriggerStressResponse, error) {
	if err := s.checkAICalls(req.GetCtx()); err != nil {
		return nil, err
	}
	runID, err := s.run.TriggerStress(ctx, tid(req.GetCtx()), mustID(req.GetStressPlanId()), mustID(req.GetEnvId()),
		"copilot:"+req.GetCtx().GetUserId())
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	s.audit(req.GetCtx(), "run", "stress_plan", req.GetStressPlanId(), map[string]any{"run_id": runID})
	return &copilotv1.TriggerStressResponse{RunId: idStr(runID)}, nil
}

// ---- 本轮新增工具：创建项目 / 接口目录问答 / 变量引用检查 ----

func (s *CopilotService) CreateProject(_ context.Context, req *copilotv1.CreateProjectRequest) (*copilotv1.CreateProjectResponse, error) {
	if err := s.checkAICalls(req.GetCtx()); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.GetName())
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "name required")
	}
	p := &model.Project{
		ID:          model.NextID(),
		TenantID:    tid(req.GetCtx()),
		Name:        name,
		Description: req.GetDescription(),
	}
	if req.GetConfig() != nil {
		if raw, err := protojson.Marshal(req.GetConfig()); err == nil {
			p.Config = model.JSON(raw)
		}
	}
	if err := s.db.Create(p).Error; err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	s.audit(req.GetCtx(), "create", "project", idStr(p.ID), map[string]any{"name": name})
	return &copilotv1.CreateProjectResponse{ProjectId: idStr(p.ID)}, nil
}

// QueryApiDirectory 返回项目接口目录树（folder + http/grpc 叶子）的扁平化条目，
// 含人读路径；支持 query 过滤与 parent_node_id 子树限定。
func (s *CopilotService) QueryApiDirectory(_ context.Context, req *copilotv1.QueryApiDirectoryRequest) (*copilotv1.QueryApiDirectoryResponse, error) {
	tenant := tid(req.GetCtx())
	pid := mustID(req.GetProjectId())
	if pid <= 0 {
		return nil, status.Error(codes.InvalidArgument, "project_id required")
	}
	var n int64
	if err := s.db.Model(&model.Project{}).Where("id = ? AND tenant_id = ?", pid, tenant).Count(&n).Error; err != nil || n == 0 {
		return nil, status.Error(codes.NotFound, "project not found in tenant")
	}

	var nodes []model.TreeNode
	if err := s.db.Where("tenant_id = ? AND project_id = ?", tenant, pid).Order("\"order\" asc, id asc").Find(&nodes).Error; err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if len(nodes) == 0 {
		return &copilotv1.QueryApiDirectoryResponse{Entries: []*copilotv1.ApiDirectoryEntry{}, Summary: "0 nodes, 0 http apis, 0 grpc apis"}, nil
	}

	httpByID := map[int64]model.HttpApi{}
	grpcByID := map[int64]model.GrpcApi{}
	httpIDs := []int64{}
	grpcIDs := []int64{}
	for _, nd := range nodes {
		switch nd.NodeType {
		case model.NodeTypeHTTPAPI:
			httpIDs = append(httpIDs, nd.RefID)
		case model.NodeTypeGRPCAPI:
			grpcIDs = append(grpcIDs, nd.RefID)
		}
	}
	if len(httpIDs) > 0 {
		var rows []model.HttpApi
		s.db.Where("tenant_id = ? AND id IN ?", tenant, httpIDs).Find(&rows)
		for _, r := range rows {
			httpByID[r.ID] = r
		}
	}
	if len(grpcIDs) > 0 {
		var rows []model.GrpcApi
		s.db.Where("tenant_id = ? AND id IN ?", tenant, grpcIDs).Find(&rows)
		for _, r := range rows {
			grpcByID[r.ID] = r
		}
	}

	nodeByID := map[int64]*model.TreeNode{}
	childrenByParent := map[int64][]*model.TreeNode{}
	parentNameByID := map[int64]string{}
	for i := range nodes {
		nd := &nodes[i]
		nodeByID[nd.ID] = nd
		childrenByParent[nd.ParentID] = append(childrenByParent[nd.ParentID], nd)
	}
	for id, nd := range nodeByID {
		if p, ok := nodeByID[nd.ParentID]; ok {
			parentNameByID[id] = p.Name
		}
	}
	pathCache := map[int64]string{}
	var namePath func(int64, int) string
	namePath = func(id int64, depth int) string {
		if depth > 64 {
			return "/…"
		}
		if p, ok := pathCache[id]; ok {
			return p
		}
		nd := nodeByID[id]
		if nd == nil {
			return ""
		}
		path := "/" + nd.Name
		if nd.ParentID != 0 {
			if p := namePath(nd.ParentID, depth+1); p != "" {
				path = strings.TrimSuffix(p, "/") + "/" + nd.Name
			}
		}
		pathCache[id] = path
		return path
	}

	makeEntry := func(nd *model.TreeNode) *copilotv1.ApiDirectoryEntry {
		e := &copilotv1.ApiDirectoryEntry{
			NodeId: idStr(nd.ID), ParentId: idStr(nd.ParentID), ParentName: parentNameByID[nd.ID],
			NodeType: int32(nd.NodeType), Name: nd.Name, Path: namePath(nd.ID, 0),
		}
		if nd.RefID != 0 {
			e.RefId = idStr(nd.RefID)
		}
		switch nd.NodeType {
		case model.NodeTypeHTTPAPI:
			if a, ok := httpByID[nd.RefID]; ok {
				if e.Name == "" {
					if a.Name != "" {
						e.Name = a.Name
					} else {
						e.Name = fmt.Sprintf("%s %s", commonv1.HttpMethod_name[int32(a.Method)], a.URI)
					}
				}
				e.Method = int32(a.Method)
				e.Uri = a.URI
			}
		case model.NodeTypeGRPCAPI:
			if g, ok := grpcByID[nd.RefID]; ok {
				e.Name = g.Method
				e.FullService = g.FullService
				e.RpcMethod = g.Method
			}
		}
		return e
	}

	roots := childrenByParent[0]
	if raw := req.GetParentNodeId(); raw != "" {
		nd := nodeByID[mustID(raw)]
		if nd == nil {
			return nil, status.Error(codes.NotFound, "parent node not found")
		}
		roots = []*model.TreeNode{nd}
	}

	query := strings.ToLower(strings.TrimSpace(req.GetQuery()))
	match := func(e *copilotv1.ApiDirectoryEntry) bool {
		if query == "" {
			return true
		}
		return strings.Contains(strings.ToLower(e.GetName()), query) ||
			strings.Contains(strings.ToLower(e.GetUri()), query) ||
			strings.Contains(strings.ToLower(e.GetFullService()), query) ||
			strings.Contains(strings.ToLower(e.GetRpcMethod()), query)
	}

	entries := []*copilotv1.ApiDirectoryEntry{}
	var walk func(nd *model.TreeNode) bool
	walk = func(nd *model.TreeNode) bool {
		e := makeEntry(nd)
		childKept := false
		for _, c := range childrenByParent[nd.ID] {
			if walk(c) {
				childKept = true
			}
		}
		if match(e) || childKept {
			entries = append(entries, e)
			return true
		}
		return false
	}
	for _, root := range roots {
		walk(root)
	}
	// 目录祖先补齐（匹配接口时父目录一并返回，给 LLM 完整路径上下文）。
	ancestors := map[int64]bool{}
	for _, e := range entries {
		cur := mustID(e.GetNodeId())
		for depth := 0; depth < 64; depth++ {
			nd := nodeByID[cur]
			if nd == nil || nd.ParentID == 0 {
				break
			}
			ancestors[nd.ParentID] = true
			cur = nd.ParentID
		}
	}
	for id := range ancestors {
		if nd := nodeByID[id]; nd != nil {
			entries = append(entries, makeEntry(nd))
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].GetPath() < entries[j].GetPath() })
	uniq := entries[:0]
	seenIDs := map[string]bool{}
	for _, e := range entries {
		if seenIDs[e.GetNodeId()] {
			continue
		}
		seenIDs[e.GetNodeId()] = true
		uniq = append(uniq, e)
	}
	summary := fmt.Sprintf("%d nodes, %d http apis, %d grpc apis", len(nodes), len(httpByID), len(grpcByID))
	return &copilotv1.QueryApiDirectoryResponse{Entries: uniq, Total: int32(len(uniq)), Summary: summary}, nil
}

var _templatePattern = regexp.MustCompile(`\{\{\s*(.+?)\s*\}\}`)

var _exprKeywords = map[string]bool{
	"and": true, "or": true, "not": true, "in": true,
	"True": true, "False": true, "None": true,
}

func rootIdentifiers(expr string) []string {
	out := []string{}
	seen := map[string]bool{}
	prevDot := false
	i := 0
	for i < len(expr) {
		c := expr[i]
		if c == '"' || c == '\'' {
			quote := c
			i++
			for i < len(expr) {
				if expr[i] == '\\' {
					i += 2
					continue
				}
				if expr[i] == quote {
					i++
					break
				}
				i++
			}
			prevDot = false
			continue
		}
		if c == '.' {
			prevDot = true
			i++
			continue
		}
		if c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			j := i
			for j < len(expr) && (expr[j] == '_' || (expr[j] >= 'A' && expr[j] <= 'Z') ||
				(expr[j] >= 'a' && expr[j] <= 'z') || (expr[j] >= '0' && expr[j] <= '9')) {
				j++
			}
			tok := expr[i:j]
			if !prevDot && !_exprKeywords[tok] && !seen[tok] {
				seen[tok] = true
				out = append(out, tok)
			}
			i = j
			prevDot = false
			continue
		}
		prevDot = false
		i++
	}
	return out
}

// CheckVariableRefs 扫描项目接口与用例中的 {{expr}} 模板，报告未定义根变量。
func (s *CopilotService) CheckVariableRefs(_ context.Context, req *copilotv1.CheckVariableRefsRequest) (*copilotv1.CheckVariableRefsResponse, error) {
	tenant := tid(req.GetCtx())
	pid := mustID(req.GetProjectId())
	if pid <= 0 {
		return nil, status.Error(codes.InvalidArgument, "project_id required")
	}
	var n int64
	if err := s.db.Model(&model.Project{}).Where("id = ? AND tenant_id = ?", pid, tenant).Count(&n).Error; err != nil || n == 0 {
		return nil, status.Error(codes.NotFound, "project not found in tenant")
	}

	var variables []model.Variable
	vq := s.db.Where("tenant_id = ? AND project_id = ?", tenant, pid)
	if raw := req.GetEnvironmentId(); raw != "" {
		vq = vq.Where("(environment_id = 0 OR environment_id = ?)", mustID(raw))
	}
	vq.Find(&variables)
	defined := map[string]bool{}
	definedNames := []string{}
	for _, v := range variables {
		if !defined[v.Key] {
			defined[v.Key] = true
			definedNames = append(definedNames, v.Key)
		}
	}
	reserved := map[string]bool{"vars": true, "response": true, "base_url": true}

	issues := []*copilotv1.VariableRefIssue{}
	issueSeen := map[string]bool{}
	addIssues := func(location, field, raw string) {
		if !strings.Contains(raw, "{{") {
			return
		}
		for _, m := range _templatePattern.FindAllStringSubmatch(raw, -1) {
			expr := m[1]
			for _, name := range rootIdentifiers(expr) {
				if reserved[name] || defined[name] {
					continue
				}
				key := location + "|" + field + "|" + name
				if issueSeen[key] {
					continue
				}
				issueSeen[key] = true
				if len(expr) > 200 {
					expr = expr[:200] + "…"
				}
				issues = append(issues, &copilotv1.VariableRefIssue{
					Location: location, Field: field, Variable: name, Expression: expr,
				})
			}
		}
	}
	scanJSON := func(location, field string, raw model.JSON) {
		if len(raw) > 0 {
			addIssues(location, field, string(raw))
		}
	}

	var apis []model.HttpApi
	s.db.Where("tenant_id = ? AND project_id = ?", tenant, pid).Find(&apis)
	for _, a := range apis {
		loc := "http_api:" + idStr(a.ID)
		addIssues(loc, "uri", a.URI)
		scanJSON(loc, "params", a.Params)
		scanJSON(loc, "headers", a.Headers)
		scanJSON(loc, "cookies", a.Cookies)
		scanJSON(loc, "body", a.Body)
		scanJSON(loc, "pre_scripts", a.PreScripts)
		scanJSON(loc, "post_scripts", a.PostScripts)
	}

	var cases []model.TestCase
	s.db.Where("tenant_id = ? AND project_id = ?", tenant, pid).Find(&cases)
	for _, c := range cases {
		loc := "case:" + idStr(c.ID)
		scanJSON(loc, "definition", c.Definition)
	}
	return &copilotv1.CheckVariableRefsResponse{
		DefinedVariables: definedNames,
		Issues:           issues,
		ScannedApis:      int32(len(apis)),
		ScannedCases:     int32(len(cases)),
	}, nil
}
