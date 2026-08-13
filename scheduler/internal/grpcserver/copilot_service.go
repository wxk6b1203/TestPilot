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
	"strings"
	"time"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	copilotv1 "github.com/testpilot/testpilot/gen/copilot/v1"
	"github.com/testpilot/testpilot/internal/impexp"
	"github.com/testpilot/testpilot/internal/model"
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
	db  *gorm.DB
	run *runner.Runner
}

func NewCopilotService(db *gorm.DB, run *runner.Runner) *CopilotService {
	return &CopilotService{db: db, run: run}
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
	q := s.db.Model(&model.HttpApi{}).Where("tenant_id = ?", tid(req.GetCtx()))
	if pid := mustID(req.GetProjectId()); pid != 0 {
		q = q.Where("project_id = ?", pid)
	}
	if qstr := strings.TrimSpace(req.GetQuery()); qstr != "" {
		q = q.Where("uri LIKE ?", "%"+qstr+"%")
	}
	var total int64
	q.Count(&total)
	var rows []model.HttpApi
	q.Order("id desc").Offset((page - 1) * size).Limit(size).Find(&rows)
	out := &copilotv1.ListApisResponse{Page: pageResp(int(total), page, size)}
	for i := range rows {
		out.HttpApis = append(out.HttpApis, runner.ToProtoHTTP(&rows[i]))
	}
	return out, nil
}

func (s *CopilotService) GetApi(_ context.Context, req *copilotv1.GetApiRequest) (*copilotv1.GetApiResponse, error) {
	if req.GetKind() == copilotv1.ApiKind_API_KIND_GRPC {
		return nil, status.Error(codes.Unimplemented, "grpc api not supported yet")
	}
	var m model.HttpApi
	if err := s.db.Where("id = ? AND tenant_id = ?", mustID(req.GetApiId()), tid(req.GetCtx())).First(&m).Error; err != nil {
		return nil, status.Error(codes.NotFound, "api not found")
	}
	return &copilotv1.GetApiResponse{Api: &copilotv1.GetApiResponse_Http{Http: runner.ToProtoHTTP(&m)}}, nil
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

// ---- 写工具（HITL 审批后由 Copilot 调用；全部落审计）----

func (s *CopilotService) CreateApi(_ context.Context, req *copilotv1.CreateApiRequest) (*copilotv1.CreateApiResponse, error) {
	if err := s.checkAICalls(req.GetCtx()); err != nil {
		return nil, err
	}
	h := req.GetHttp()
	if h == nil {
		return nil, status.Error(codes.InvalidArgument, "only http api supported")
	}
	m := &model.HttpApi{
		ID:        model.NextID(),
		TenantID:  tid(req.GetCtx()),
		ProjectID: mustID(req.GetProjectId()),
		Method:    int16(h.GetMethod()),
		URI:       h.GetUri(),
		Params:    kvToJSON(h.GetParams()),
		Headers:   kvToJSON(h.GetHeaders()),
	}
	if b := h.GetBody(); b != nil {
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
}

func (s *CopilotService) UpdateApi(_ context.Context, req *copilotv1.UpdateApiRequest) (*copilotv1.UpdateApiResponse, error) {
	if err := s.checkAICalls(req.GetCtx()); err != nil {
		return nil, err
	}
	h := req.GetHttp()
	if h == nil {
		return nil, status.Error(codes.InvalidArgument, "only http api supported")
	}
	var m model.HttpApi
	if err := s.db.Where("id = ? AND tenant_id = ?", mustID(req.GetApiId()), tid(req.GetCtx())).First(&m).Error; err != nil {
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
}

func (s *CopilotService) CreateTestCase(_ context.Context, req *copilotv1.CreateTestCaseRequest) (*copilotv1.CreateTestCaseResponse, error) {
	if err := s.checkAICalls(req.GetCtx()); err != nil {
		return nil, err
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

func (s *CopilotService) CreateTestPlan(_ context.Context, req *copilotv1.CreateTestPlanRequest) (*copilotv1.CreateTestPlanResponse, error) {
	if err := s.checkAICalls(req.GetCtx()); err != nil {
		return nil, err
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
	return nil, status.Error(codes.Unimplemented, "openapi diff is v2 scope")
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
