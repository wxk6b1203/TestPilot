package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	"github.com/testpilot/testpilot/internal/dispatch"
	"github.com/testpilot/testpilot/internal/model"
)

func TestNormalizeRefIDs(t *testing.T) {
	got, err := normalizeRefIDs([]string{"10", "2", "10", " 3 ", ""})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"2", "3", "10"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	for _, bad := range []string{"0", "-1", "abc", "1.5"} {
		if _, err := normalizeRefIDs([]string{bad}); err == nil {
			t.Fatalf("%q should be invalid", bad)
		}
	}
}

func TestScanLowCodeAPIRefs(t *testing.T) {
	src := `
from testpilot_sdk import HttpAPI, assert_that

ECHO_HTTP = "12"
ECHO_GRPC = '34'
ECHO_ANY = "56"
ECHO_HTTP_CTOR = "78"

async def run(ctx):
    await ctx.http_api("12").run()
    await ctx.grpc_api('34').run()
    await ctx.api("56").run()
    a = HttpAPI(api_id="78")
    b = GrpcAPI(api_id="90")
    await ctx.http_api(ECHO_HTTP).run()
    await ctx.grpc_api(ECHO_GRPC).run()
    await ctx.api(ECHO_ANY).run()
    c = HttpAPI(api_id=ECHO_HTTP_CTOR)
`
	httpIDs, grpcIDs, anyIDs, uses := scanLowCodeAPIRefs(src)
	if strings.Join(httpIDs, ",") != "12,78" {
		t.Fatalf("http ids=%v", httpIDs)
	}
	if strings.Join(grpcIDs, ",") != "34,90" {
		t.Fatalf("grpc ids=%v", grpcIDs)
	}
	if strings.Join(anyIDs, ",") != "56" {
		t.Fatalf("any ids=%v", anyIDs)
	}
	if uses {
		t.Fatal("should not mark wrappers used without import")
	}
	src2 := "from tp_api_wrappers import Api11\nx = Api11()\n"
	httpIDs, grpcIDs, anyIDs, uses = scanLowCodeAPIRefs(src2)
	if uses == false || strings.Join(anyIDs, ",") != "11" || len(httpIDs) != 0 || len(grpcIDs) != 0 {
		t.Fatalf("wrapper scan: http=%v grpc=%v any=%v uses=%v", httpIDs, grpcIDs, anyIDs, uses)
	}
}

func TestGenerateAPIWrappersSource(t *testing.T) {
	prep := newLowCodeAPIPrep()
	prep.HTTPApis["123"] = &commonv1.HttpApi{
		Id: "123", Method: commonv1.HttpMethod_HTTP_METHOD_POST, Uri: "/users",
	}
	prep.HTTPNames["123"] = "Create User"
	prep.HTTPApis["7"] = &commonv1.HttpApi{
		Id: "7", Method: commonv1.HttpMethod_HTTP_METHOD_GET, Uri: "/class",
	}
	prep.HTTPNames["7"] = "class"
	prep.GrpcApis["456"] = &commonv1.GrpcApi{
		Id: "456", FullService: "testpilot.echo.v1.EchoService", Method: "Echo",
	}
	prep.GrpcNames["456"] = "echo"
	src, err := GenerateAPIWrappersSource(prep)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"from testpilot_sdk import GrpcAPI, HttpAPI",
		"class Api7(HttpAPI):",
		"class Api123(HttpAPI):",
		"class Api456(GrpcAPI):",
		`api_id: str = "123"`,
		`method: str = "POST"`,
		`uri: str = "/users"`,
		`params: dict = {}`,
		`headers: dict = {}`,
		`full_service: str = "testpilot.echo.v1.EchoService"`,
		`request: dict = {}`,
		"CreateUser = Api123",
		"Class = Api7", // 关键字经 PascalCase 后成为合法标识符
		"echo = Api456",
		`__all__ = ["Api7", "Class", "Api123", "CreateUser", "Api456", "echo"]`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("source missing %q:\n%s", want, src)
		}
	}
	// 确定性与空输入
	again, _ := GenerateAPIWrappersSource(prep)
	if again != src {
		t.Fatal("generator should be deterministic")
	}
	if got, _ := GenerateAPIWrappersSource(newLowCodeAPIPrep()); got != "" {
		t.Fatalf("empty prep should generate empty source, got %q", got)
	}
}

func TestReadableAliasCollisions(t *testing.T) {
	prep := newLowCodeAPIPrep()
	prep.HTTPApis["1"] = &commonv1.HttpApi{Id: "1", Uri: "/a", Method: commonv1.HttpMethod_HTTP_METHOD_GET}
	prep.HTTPNames["1"] = "User"
	prep.HTTPApis["2"] = &commonv1.HttpApi{Id: "2", Uri: "/b", Method: commonv1.HttpMethod_HTTP_METHOD_GET}
	prep.HTTPNames["2"] = "user"
	prep.HTTPApis["3"] = &commonv1.HttpApi{Id: "3", Uri: "/c", Method: commonv1.HttpMethod_HTTP_METHOD_GET}
	prep.HTTPNames["3"] = "Api2"
	src, err := GenerateAPIWrappersSource(prep)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(src, "User = Api1") != 1 {
		t.Fatalf("alias collision handling:\n%s", src)
	}
	if strings.Contains(src, "user = Api2") && !strings.Contains(src, "user_2 = Api2") {
		t.Fatalf("duplicate alias should get suffix:\n%s", src)
	}
	if !strings.Contains(src, "Api2_2 = Api3") {
		t.Fatalf("alias colliding with stable class should get suffix:\n%s", src)
	}
}

func TestMaterializeLowCodeAPIRefs(t *testing.T) {
	d := openTestDB(t)
	r := New(d, dispatch.New(d))

	httpAPI := model.HttpApi{ID: model.NextID(), TenantID: 1, ProjectID: 11,
		Name: "Create User", Method: int16(commonv1.HttpMethod_HTTP_METHOD_POST), URI: "/users",
		Headers: model.JSON(`[{"key":"X-A","value":"1"}]`)}
	if err := d.Create(&httpAPI).Error; err != nil {
		t.Fatal(err)
	}
	grpcAPI := model.GrpcApi{ID: model.NextID(), TenantID: 1, ProjectID: 11,
		FullService: "testpilot.echo.v1.EchoService", Method: "Echo",
		RequestMessage: model.JSON(`{"message":"hi"}`)}
	if err := d.Create(&grpcAPI).Error; err != nil {
		t.Fatal(err)
	}

	// 显式 refs + ctx.api 静态提取 + wrapper import
	src := fmt.Sprintf(`from tp_api_wrappers import Api%d
async def run(ctx):
    a = Api%d()
    b = await ctx.api("%d").run()
`, grpcAPI.ID, grpcAPI.ID, httpAPI.ID)
	lc := model.TestCase{
		ID: model.NextID(), TenantID: 1, ProjectID: 11,
		Type: int16(commonv1.TestCaseType_TEST_CASE_TYPE_LOWCODE),
		Name: "lc-by-id",
		Definition: model.JSON(fmt.Sprintf(
			`{"source": %q, "entry": "run", "http_api_refs": ["%d"], "grpc_api_refs": ["%d"]}`,
			src, httpAPI.ID, grpcAPI.ID)),
	}
	m, err := r.materializeCaseEx(&lc)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if len(m.HTTPApis) != 1 || m.HTTPApis[fmt.Sprint(httpAPI.ID)] == nil {
		t.Fatalf("http apis=%v", m.HTTPApis)
	}
	if len(m.GrpcApis) != 1 || m.GrpcApis[fmt.Sprint(grpcAPI.ID)] == nil {
		t.Fatalf("grpc apis=%v", m.GrpcApis)
	}
	if got := m.APIWrappersSource; !strings.Contains(got, fmt.Sprintf("class Api%d(HttpAPI):", httpAPI.ID)) ||
		!strings.Contains(got, fmt.Sprintf("class Api%d(GrpcAPI):", grpcAPI.ID)) ||
		!strings.Contains(got, "CreateUser = Api"+fmt.Sprint(httpAPI.ID)) {
		t.Fatalf("wrappers source:\n%s", got)
	}
	// 类字段默认值用于可读性/补全；SDK 按 model_fields_set 只发显式字段，
	// 因此接口快照仍优先（Worker 测试 test_lowcode_api 验证此语义）。
	if !strings.Contains(m.APIWrappersSource, `headers: dict = {"X-A": "1"}`) {
		t.Fatalf("wrappers should embed header defaults:\n%s", m.APIWrappersSource)
	}

	// 纯 ctx.api 无显式 refs：按静态提取解析
	anyLC := model.TestCase{
		ID: model.NextID(), TenantID: 1, ProjectID: 11,
		Type: int16(commonv1.TestCaseType_TEST_CASE_TYPE_LOWCODE),
		Name: "lc-any",
		Definition: model.JSON(fmt.Sprintf(
			`{"source": %q, "entry": "run"}`,
			fmt.Sprintf("async def run(ctx):\n    await ctx.api('%d').run()\n", httpAPI.ID))),
	}
	am, err := r.materializeCaseEx(&anyLC)
	if err != nil {
		t.Fatalf("ctx.api materialize: %v", err)
	}
	if len(am.HTTPApis) != 1 || len(am.GrpcApis) != 0 {
		t.Fatalf("ctx.api resolution: http=%v grpc=%v", am.HTTPApis, am.GrpcApis)
	}

	// 常量形式（ECHO_API = "id"; ctx.http_api(ECHO_API)）同样静态提取，
	// 不依赖 definition 显式 http_api_refs。
	constLC := model.TestCase{
		ID: model.NextID(), TenantID: 1, ProjectID: 11,
		Type: int16(commonv1.TestCaseType_TEST_CASE_TYPE_LOWCODE),
		Name: "lc-const",
		Definition: model.JSON(fmt.Sprintf(
			`{"source": %q, "entry": "run"}`,
			fmt.Sprintf("ECHO_API = %q\n\nasync def run(ctx):\n    await ctx.http_api(ECHO_API).run()\n", fmt.Sprint(httpAPI.ID)))),
	}
	cm, err := r.materializeCaseEx(&constLC)
	if err != nil {
		t.Fatalf("const materialize: %v", err)
	}
	if len(cm.HTTPApis) != 1 || cm.HTTPApis[fmt.Sprint(httpAPI.ID)] == nil {
		t.Fatalf("const ctx.http_api resolution: http=%v", cm.HTTPApis)
	}

	// 缺失 / 非法 / 跨租户
	missingID := model.NextID()
	bad := model.TestCase{
		ID: model.NextID(), TenantID: 1, ProjectID: 11,
		Type:       int16(commonv1.TestCaseType_TEST_CASE_TYPE_LOWCODE),
		Name:       "missing",
		Definition: model.JSON(fmt.Sprintf(`{"source":"", "http_api_refs": ["%d"]}`, missingID)),
	}
	if _, err := r.materializeCaseEx(&bad); err == nil {
		t.Fatal("missing ref should error")
	}
	bad2 := model.TestCase{
		ID: model.NextID(), TenantID: 1, ProjectID: 11,
		Type:       int16(commonv1.TestCaseType_TEST_CASE_TYPE_LOWCODE),
		Name:       "bad-ref",
		Definition: model.JSON(`{"source":"", "http_api_refs": ["nope"]}`),
	}
	if _, err := r.materializeCaseEx(&bad2); err == nil {
		t.Fatal("invalid ref should error")
	}
	foreign := model.HttpApi{ID: model.NextID(), TenantID: 2, ProjectID: 11, Name: "f", URI: "/f"}
	if err := d.Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	bad3 := model.TestCase{
		ID: model.NextID(), TenantID: 1, ProjectID: 11,
		Type:       int16(commonv1.TestCaseType_TEST_CASE_TYPE_LOWCODE),
		Name:       "foreign",
		Definition: model.JSON(fmt.Sprintf(`{"source":"", "http_api_refs": ["%d"]}`, foreign.ID)),
	}
	if _, err := r.materializeCaseEx(&bad3); err == nil {
		t.Fatal("cross-tenant ref should error")
	}
}

func TestMaterializeLowCodeWrappersFallbackAllProject(t *testing.T) {
	d := openTestDB(t)
	r := New(d, dispatch.New(d))
	api := model.HttpApi{ID: model.NextID(), TenantID: 1, ProjectID: 22,
		Name: "User", Method: int16(commonv1.HttpMethod_HTTP_METHOD_GET), URI: "/user"}
	if err := d.Create(&api).Error; err != nil {
		t.Fatal(err)
	}
	// 其他项目的接口不应被兜底包含
	other := model.HttpApi{ID: model.NextID(), TenantID: 1, ProjectID: 23,
		Name: "Secret", Method: int16(commonv1.HttpMethod_HTTP_METHOD_GET), URI: "/secret"}
	if err := d.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	lc := model.TestCase{
		ID: model.NextID(), TenantID: 1, ProjectID: 22,
		Type:       int16(commonv1.TestCaseType_TEST_CASE_TYPE_LOWCODE),
		Name:       "fallback",
		Definition: model.JSON(`{"source": "from tp_api_wrappers import User\nasync def run(ctx):\n    pass\n", "entry": "run"}`),
	}
	m, err := r.materializeCaseEx(&lc)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.HTTPApis) != 1 {
		t.Fatalf("fallback should include only project apis, got %v", m.HTTPApis)
	}
	if _, ok := m.HTTPApis[fmt.Sprint(api.ID)]; !ok {
		t.Fatalf("expected api %d, got %v", api.ID, m.HTTPApis)
	}
}

func TestMaterializeLowCodeBinaryRefFromAPISnapshot(t *testing.T) {
	d := openTestDB(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "body.bin"), []byte("lowcode-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	art := model.Artifact{ID: model.NextID(), TenantID: 1, RunID: 1, Kind: model.ArtifactKindLog, URI: "body.bin", Size: 12}
	if err := d.Create(&art).Error; err != nil {
		t.Fatal(err)
	}
	disp := dispatch.New(d)
	disp.SetArtifactIngest(nil, root)
	r := New(d, disp)
	api := model.HttpApi{ID: model.NextID(), TenantID: 1, ProjectID: 33,
		Name: "Upload", Method: int16(commonv1.HttpMethod_HTTP_METHOD_POST), URI: "/upload",
		Body: model.JSON(fmt.Sprintf(`{"contentType": 6, "binary_ref": "artifact:%d"}`, art.ID))}
	if err := d.Create(&api).Error; err != nil {
		t.Fatal(err)
	}
	lc := model.TestCase{
		ID: model.NextID(), TenantID: 1, ProjectID: 33,
		Type:       int16(commonv1.TestCaseType_TEST_CASE_TYPE_LOWCODE),
		Name:       "binary",
		Definition: model.JSON(fmt.Sprintf(`{"source":"", "http_api_refs": ["%d"]}`, api.ID)),
	}
	m, err := r.materializeCaseEx(&lc)
	if err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("artifact:%d", art.ID)
	if string(m.InlineFiles[key]) != "lowcode-bytes" {
		t.Fatalf("inline file not resolved: %q", m.InlineFiles[key])
	}
}

func TestPreviewAPIWrappers(t *testing.T) {
	d := openTestDB(t)
	r := New(d, dispatch.New(d))
	api := model.HttpApi{ID: model.NextID(), TenantID: 1, ProjectID: 44,
		Name: "Ping", Method: int16(commonv1.HttpMethod_HTTP_METHOD_GET), URI: "/ping"}
	if err := d.Create(&api).Error; err != nil {
		t.Fatal(err)
	}
	src, count, err := r.PreviewAPIWrappers(1, 44, []string{fmt.Sprint(api.ID)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || !strings.Contains(src, "Ping = Api"+fmt.Sprint(api.ID)) {
		t.Fatalf("preview count=%d source:\n%s", count, src)
	}
	if _, _, err := r.PreviewAPIWrappers(1, 44, []string{"bad"}, nil); err == nil {
		t.Fatal("invalid preview id should error")
	}
}

func TestGenerateAPIWrappersStub(t *testing.T) {
	prep := newLowCodeAPIPrep()
	prep.HTTPApis["123"] = &commonv1.HttpApi{
		Id: "123", Method: commonv1.HttpMethod_HTTP_METHOD_POST, Uri: "/users",
	}
	prep.HTTPNames["123"] = "CreateUser"
	prep.GrpcApis["456"] = &commonv1.GrpcApi{
		Id: "456", FullService: "testpilot.echo.v1.EchoService", Method: "Echo",
	}
	prep.GrpcNames["456"] = "Echo"
	src, err := GenerateAPIWrappersStub(prep)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"IDE completion stub",
		"from typing import Any",
		"class Response:",
		"class GrpcResponse:",
		"class Api123:",
		"async def run(self, *, body: Any = ..., headers: dict[str, str] | None = ...",
		"-> Response: ...",
		"class Api456:",
		"async def run(self, *, request: dict[str, Any] | None = ...",
		"-> GrpcResponse: ...",
		"CreateUser = Api123",
		"Echo = Api456",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("stub missing %q:\n%s", want, src)
		}
	}
	if got, _ := GenerateAPIWrappersStub(newLowCodeAPIPrep()); got != "" {
		t.Fatalf("empty prep should generate empty stub, got %q", got)
	}
}
