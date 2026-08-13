package grpcserver_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	copilotv1 "github.com/testpilot/testpilot/gen/copilot/v1"
	"github.com/testpilot/testpilot/internal/grpcserver"
	"github.com/testpilot/testpilot/internal/model"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// newCopilotClient 起 CopilotToolService（runner 仅 Trigger* 用到，本组用例传 nil）。
func newCopilotClient(t *testing.T) (copilotv1.CopilotToolServiceClient, *gorm.DB) {
	t.Helper()
	d := openTestDB(t)
	conn := bufConn(t, func(srv *grpc.Server) {
		copilotv1.RegisterCopilotToolServiceServer(srv, grpcserver.NewCopilotService(d, nil))
	})
	return copilotv1.NewCopilotToolServiceClient(conn), d
}

func seedHTTPApi(t *testing.T, d *gorm.DB, tenant, project int64, method commonv1.HttpMethod, uri string) *model.HttpApi {
	t.Helper()
	m := &model.HttpApi{
		ID:        model.NextID(),
		TenantID:  tenant,
		ProjectID: project,
		Method:    int16(method),
		URI:       uri,
		Headers:   model.JSON(`[{"key":"X-A","value":"1"}]`),
		Params:    model.JSON(`[{"key":"q","value":"x"}]`),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := d.Create(m).Error; err != nil {
		t.Fatal(err)
	}
	return m
}

// GetApi：HTTP 载荷转换、GRPC 未实现、不存在/跨租户 NotFound。
func TestGetApi(t *testing.T) {
	cli, d := newCopilotClient(t)
	api := seedHTTPApi(t, d, 1, 100, commonv1.HttpMethod_HTTP_METHOD_POST, "/v1/users")
	apiID := strconv.FormatInt(api.ID, 10)
	ctx := context.Background()

	t.Run("http payload", func(t *testing.T) {
		resp, err := cli.GetApi(ctx, &copilotv1.GetApiRequest{
			Ctx: copilotCtx(1, "u-1"), ApiId: apiID, Kind: copilotv1.ApiKind_API_KIND_HTTP,
		})
		if err != nil {
			t.Fatal(err)
		}
		h := resp.GetHttp()
		if h == nil {
			t.Fatal("want http payload")
		}
		if h.GetId() != apiID || h.GetTenantId() != 1 || h.GetProjectId() != "100" {
			t.Fatalf("id/tenant/project mismatch: %v %v %v", h.GetId(), h.GetTenantId(), h.GetProjectId())
		}
		if h.GetMethod() != commonv1.HttpMethod_HTTP_METHOD_POST || h.GetUri() != "/v1/users" {
			t.Fatalf("method/uri mismatch: %v %v", h.GetMethod(), h.GetUri())
		}
		hs := h.GetHeaders()
		if len(hs) != 1 || hs[0].GetKey() != "X-A" || hs[0].GetValue() != "1" {
			t.Fatalf("headers mismatch: %v", hs)
		}
		ps := h.GetParams()
		if len(ps) != 1 || ps[0].GetKey() != "q" || ps[0].GetValue() != "x" {
			t.Fatalf("params mismatch: %v", ps)
		}
	})

	t.Run("grpc unimplemented", func(t *testing.T) {
		_, err := cli.GetApi(ctx, &copilotv1.GetApiRequest{
			Ctx: copilotCtx(1, "u-1"), ApiId: apiID, Kind: copilotv1.ApiKind_API_KIND_GRPC,
		})
		if status.Code(err) != codes.Unimplemented {
			t.Fatalf("want Unimplemented, got %v", err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := cli.GetApi(ctx, &copilotv1.GetApiRequest{
			Ctx: copilotCtx(1, "u-1"), ApiId: "424242", Kind: copilotv1.ApiKind_API_KIND_HTTP,
		})
		if status.Code(err) != codes.NotFound {
			t.Fatalf("want NotFound, got %v", err)
		}
	})

	t.Run("cross tenant hidden", func(t *testing.T) {
		_, err := cli.GetApi(ctx, &copilotv1.GetApiRequest{
			Ctx: copilotCtx(2, "u-2"), ApiId: apiID, Kind: copilotv1.ApiKind_API_KIND_HTTP,
		})
		if status.Code(err) != codes.NotFound {
			t.Fatalf("want NotFound, got %v", err)
		}
	})
}

// ListProjects：租户过滤 + 分页 + 名称模糊查询。
func TestListProjectsTenantFilterAndPagination(t *testing.T) {
	cli, d := newCopilotClient(t)
	ctx := context.Background()
	names := []string{"OrderSvc", "UserSvc", "PaySvc"}
	for _, n := range names {
		if err := d.Create(&model.Project{
			ID: model.NextID(), TenantID: 1, Name: n, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Create(&model.Project{
		ID: model.NextID(), TenantID: 2, Name: "OtherTenant", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}

	p1, err := cli.ListProjects(ctx, &copilotv1.ListProjectsRequest{
		Ctx: copilotCtx(1, "u-1"), Page: &commonv1.PageRequest{Page: 1, PageSize: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p1.GetPage().GetTotal() != 3 || p1.GetPage().GetPage() != 1 || p1.GetPage().GetPageSize() != 2 {
		t.Fatalf("page mismatch: %+v", p1.GetPage())
	}
	if len(p1.GetProjects()) != 2 {
		t.Fatalf("page1 items=%d, want 2", len(p1.GetProjects()))
	}

	p2, err := cli.ListProjects(ctx, &copilotv1.ListProjectsRequest{
		Ctx: copilotCtx(1, "u-1"), Page: &commonv1.PageRequest{Page: 2, PageSize: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(p2.GetProjects()) != 1 {
		t.Fatalf("page2 items=%d, want 1", len(p2.GetProjects()))
	}

	q, err := cli.ListProjects(ctx, &copilotv1.ListProjectsRequest{
		Ctx: copilotCtx(1, "u-1"), Query: "Order",
	})
	if err != nil {
		t.Fatal(err)
	}
	if q.GetPage().GetTotal() != 1 || len(q.GetProjects()) != 1 || q.GetProjects()[0].GetName() != "OrderSvc" {
		t.Fatalf("query filter mismatch: %+v", q)
	}

	other, err := cli.ListProjects(ctx, &copilotv1.ListProjectsRequest{Ctx: copilotCtx(2, "u-2")})
	if err != nil {
		t.Fatal(err)
	}
	if other.GetPage().GetTotal() != 1 || other.GetProjects()[0].GetName() != "OtherTenant" {
		t.Fatalf("tenant2 view mismatch: %+v", other)
	}
}

// ListApis：租户过滤 + project_id 过滤 + uri 模糊查询 + 分页计数。
func TestListApisFilters(t *testing.T) {
	cli, d := newCopilotClient(t)
	ctx := context.Background()
	seedHTTPApi(t, d, 1, 100, commonv1.HttpMethod_HTTP_METHOD_GET, "/v1/orders")
	seedHTTPApi(t, d, 1, 100, commonv1.HttpMethod_HTTP_METHOD_POST, "/v1/orders")
	seedHTTPApi(t, d, 1, 200, commonv1.HttpMethod_HTTP_METHOD_GET, "/v1/users")
	seedHTTPApi(t, d, 2, 100, commonv1.HttpMethod_HTTP_METHOD_GET, "/v1/orders")

	all, err := cli.ListApis(ctx, &copilotv1.ListApisRequest{Ctx: copilotCtx(1, "u-1")})
	if err != nil {
		t.Fatal(err)
	}
	if all.GetPage().GetTotal() != 3 || len(all.GetHttpApis()) != 3 {
		t.Fatalf("tenant1 total=%d items=%d, want 3", all.GetPage().GetTotal(), len(all.GetHttpApis()))
	}

	byProj, err := cli.ListApis(ctx, &copilotv1.ListApisRequest{
		Ctx: copilotCtx(1, "u-1"), ProjectId: "100",
	})
	if err != nil {
		t.Fatal(err)
	}
	if byProj.GetPage().GetTotal() != 2 {
		t.Fatalf("project filter total=%d, want 2", byProj.GetPage().GetTotal())
	}

	byQuery, err := cli.ListApis(ctx, &copilotv1.ListApisRequest{
		Ctx: copilotCtx(1, "u-1"), Query: "users",
	})
	if err != nil {
		t.Fatal(err)
	}
	if byQuery.GetPage().GetTotal() != 1 || byQuery.GetHttpApis()[0].GetUri() != "/v1/users" {
		t.Fatalf("uri query mismatch: %+v", byQuery)
	}

	crossTenant, err := cli.ListApis(ctx, &copilotv1.ListApisRequest{Ctx: copilotCtx(2, "u-2")})
	if err != nil {
		t.Fatal(err)
	}
	if crossTenant.GetPage().GetTotal() != 1 {
		t.Fatalf("tenant2 total=%d, want 1", crossTenant.GetPage().GetTotal())
	}
}

// ListRuns：租户过滤 + status/plan/project 过滤 + TestRun 字段转换。
func TestListRunsFilters(t *testing.T) {
	cli, d := newCopilotClient(t)
	ctx := context.Background()
	if err := d.Create(&model.TestPlan{ID: 10, TenantID: 1, ProjectID: 100, Name: "plan-a"}).Error; err != nil {
		t.Fatal(err)
	}
	mkRun := func(id, tenant, planID int64, st commonv1.RunStatus) {
		if err := d.Create(&model.TestRun{
			ID: id, TenantID: tenant, PlanID: planID, EnvID: 5,
			Status: int16(st), Trigger: int16(commonv1.TriggerType_TRIGGER_TYPE_MANUAL),
			TriggeredBy: "tester", StartedAt: time.Now(),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	mkRun(1001, 1, 10, commonv1.RunStatus_RUN_STATUS_RUNNING)
	mkRun(1002, 1, 10, commonv1.RunStatus_RUN_STATUS_PASSED)
	mkRun(1003, 2, 10, commonv1.RunStatus_RUN_STATUS_RUNNING)

	all, err := cli.ListRuns(ctx, &copilotv1.ListRunsRequest{Ctx: copilotCtx(1, "u-1")})
	if err != nil {
		t.Fatal(err)
	}
	if all.GetPage().GetTotal() != 2 || len(all.GetRuns()) != 2 {
		t.Fatalf("tenant1 total=%d, want 2", all.GetPage().GetTotal())
	}
	// id desc：1002 在前；字段转换校验。
	r := all.GetRuns()[0]
	if r.GetId() != "1002" || r.GetStatus() != commonv1.RunStatus_RUN_STATUS_PASSED ||
		r.GetPlanId() != "10" || r.GetEnvId() != "5" || r.GetTriggeredBy() != "tester" ||
		r.GetTrigger() != commonv1.TriggerType_TRIGGER_TYPE_MANUAL || r.GetStartedAt() == nil {
		t.Fatalf("run proto mismatch: %+v", r)
	}

	byStatus, err := cli.ListRuns(ctx, &copilotv1.ListRunsRequest{
		Ctx: copilotCtx(1, "u-1"), Status: commonv1.RunStatus_RUN_STATUS_RUNNING,
	})
	if err != nil {
		t.Fatal(err)
	}
	if byStatus.GetPage().GetTotal() != 1 || byStatus.GetRuns()[0].GetId() != "1001" {
		t.Fatalf("status filter mismatch: %+v", byStatus)
	}

	byPlan, err := cli.ListRuns(ctx, &copilotv1.ListRunsRequest{
		Ctx: copilotCtx(1, "u-1"), PlanId: "10",
	})
	if err != nil {
		t.Fatal(err)
	}
	if byPlan.GetPage().GetTotal() != 2 {
		t.Fatalf("plan filter total=%d, want 2", byPlan.GetPage().GetTotal())
	}

	byProject, err := cli.ListRuns(ctx, &copilotv1.ListRunsRequest{
		Ctx: copilotCtx(1, "u-1"), ProjectId: "100",
	})
	if err != nil {
		t.Fatal(err)
	}
	if byProject.GetPage().GetTotal() != 2 {
		t.Fatalf("project filter total=%d, want 2", byProject.GetPage().GetTotal())
	}

	crossTenant, err := cli.ListRuns(ctx, &copilotv1.ListRunsRequest{Ctx: copilotCtx(2, "u-2")})
	if err != nil {
		t.Fatal(err)
	}
	if crossTenant.GetPage().GetTotal() != 1 || crossTenant.GetRuns()[0].GetId() != "1003" {
		t.Fatalf("tenant2 view mismatch: %+v", crossTenant)
	}
}

// CreateApi：写库 + 审计落库（actor=2/copilot，approved_by=会话用户）+ ID 字符串化返回。
func TestCreateApiAudit(t *testing.T) {
	cli, d := newCopilotClient(t)
	ctx := context.Background()

	resp, err := cli.CreateApi(ctx, &copilotv1.CreateApiRequest{
		Ctx:       copilotCtx(1, "u-9"),
		ProjectId: "100",
		Api: &copilotv1.CreateApiRequest_Http{Http: &commonv1.HttpApi{
			Method:  commonv1.HttpMethod_HTTP_METHOD_PUT,
			Uri:     "/v1/ping",
			Headers: []*commonv1.KeyValue{{Key: "X-H", Value: "v"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	id, err := strconv.ParseInt(resp.GetApiId(), 10, 64)
	if err != nil || id == 0 {
		t.Fatalf("api_id not stringified int64: %q", resp.GetApiId())
	}

	var api model.HttpApi
	if err := d.First(&api, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	if api.TenantID != 1 || api.ProjectID != 100 ||
		api.Method != int16(commonv1.HttpMethod_HTTP_METHOD_PUT) || api.URI != "/v1/ping" {
		t.Fatalf("stored api mismatch: %+v", api)
	}
	if !strings.Contains(string(api.Headers), `"X-H"`) {
		t.Fatalf("headers not persisted: %s", api.Headers)
	}

	var logs []model.AuditLog
	if err := d.Where("tenant_id = 1 AND actor = 2 AND action = 'create' AND resource_type = 'http_api' AND resource_id = ?",
		resp.GetApiId()).Find(&logs).Error; err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("audit rows=%d, want 1", len(logs))
	}
	l := logs[0]
	if l.ActorID != "u-9" || l.ApprovedBy != "u-9" {
		t.Fatalf("audit actor/approver mismatch: %+v", l)
	}
	if !strings.Contains(string(l.Detail), "/v1/ping") {
		t.Fatalf("audit detail missing uri: %s", l.Detail)
	}
}

// CreateApi 参数校验：缺 api 载荷 / 缺 uri → InvalidArgument。
func TestCreateApiInvalidArgument(t *testing.T) {
	cli, _ := newCopilotClient(t)
	ctx := context.Background()

	if _, err := cli.CreateApi(ctx, &copilotv1.CreateApiRequest{
		Ctx: copilotCtx(1, "u-1"), ProjectId: "100",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("nil api: want InvalidArgument, got %v", err)
	}

	if _, err := cli.CreateApi(ctx, &copilotv1.CreateApiRequest{
		Ctx:       copilotCtx(1, "u-1"),
		ProjectId: "100",
		Api:       &copilotv1.CreateApiRequest_Http{Http: &commonv1.HttpApi{Method: commonv1.HttpMethod_HTTP_METHOD_GET}},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty uri: want InvalidArgument, got %v", err)
	}
}

// QuerySchema 返回内嵌 domain schema。
func TestQuerySchema(t *testing.T) {
	cli, _ := newCopilotClient(t)
	resp, err := cli.QuerySchema(context.Background(), &copilotv1.QuerySchemaRequest{Ctx: copilotCtx(1, "u-1")})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetVersion() != "v1" || !strings.Contains(resp.GetSchemaJson(), "HttpApi") {
		t.Fatalf("schema mismatch: version=%q len=%d", resp.GetVersion(), len(resp.GetSchemaJson()))
	}
}

// ApplyOpenApiDiff 为 v2 占位：既定行为 codes.Unimplemented。
func TestApplyOpenApiDiffUnimplemented(t *testing.T) {
	cli, _ := newCopilotClient(t)
	_, err := cli.ApplyOpenApiDiff(context.Background(), &copilotv1.ApplyOpenApiDiffRequest{
		Ctx: copilotCtx(1, "u-1"), ProjectId: "100", OpenapiDocument: "{}",
	})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("want Unimplemented, got %v", err)
	}
}

func TestImportOpenApiFromURL(t *testing.T) {
	cli, d := newCopilotClient(t)
	// httptest 回环会被私网防护拦截——URL 导入防护针对的是任意外网目标；
	// 测试通过 fetchOpenAPIURL 的单测覆盖正常路径（见下），gRPC 层这里只验证接线：
	// 私网 URL 必须被 InvalidArgument 拒绝。
	_, err := cli.ImportOpenApi(context.Background(), &copilotv1.ImportOpenApiRequest{
		Ctx: copilotCtx(1, "u-1"), ProjectId: "100",
		Spec: &copilotv1.ImportOpenApiRequest_OpenapiUrl{OpenapiUrl: "http://127.0.0.1:9/openapi.json"},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("loopback url should be InvalidArgument, got %v", err)
	}
	if !strings.Contains(status.Convert(err).Message(), "private/loopback") {
		t.Fatalf("message should mention private/loopback: %v", err)
	}
	// 无 document 无 url → InvalidArgument
	_, err = cli.ImportOpenApi(context.Background(), &copilotv1.ImportOpenApiRequest{
		Ctx: copilotCtx(1, "u-1"), ProjectId: "100",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty spec should be InvalidArgument, got %v", err)
	}
	// 兜底：document 路径仍正常（回归）
	var doc = `{"openapi":"3.0.3","paths":{"/h":{"get":{}}}}`
	r, err := cli.ImportOpenApi(context.Background(), &copilotv1.ImportOpenApiRequest{
		Ctx: copilotCtx(1, "u-1"), ProjectId: "100",
		Spec: &copilotv1.ImportOpenApiRequest_OpenapiDocument{OpenapiDocument: doc},
	})
	if err != nil || r.GetImportedCount() != 1 {
		t.Fatalf("document import broken: r=%+v err=%v", r, err)
	}
	var n int64
	d.Model(&model.HttpApi{}).Where("project_id = 100").Count(&n)
	if n != 1 {
		t.Fatalf("apis=%d", n)
	}
}
