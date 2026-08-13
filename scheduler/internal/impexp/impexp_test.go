package impexp

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/testpilot/testpilot/internal/db"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), "", db.Pool{})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

const openAPIJSON = `{
  "openapi": "3.0.3",
  "info": {"title": "demo", "version": "1.0"},
  "paths": {
    "/users": {
      "get": {"parameters": [{"name": "page", "in": "query"}]},
      "post": {"requestBody": {"content": {"application/json": {"schema": {"type": "object", "properties": {"name": {"type": "string"}}}}}}}
    },
    "/users/{id}": {"delete": {}}
  }
}`

func TestImportOpenAPIJSON(t *testing.T) {
	d := openTestDB(t)
	res, err := ImportOpenAPI(d, 1, 10, []byte(openAPIJSON))
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 3 || res.Skipped != 0 || len(res.APIIDs) != 3 {
		t.Fatalf("got %+v", res)
	}
	// 幂等：再导入应全部跳过
	res2, err := ImportOpenAPI(d, 1, 10, []byte(openAPIJSON))
	if err != nil {
		t.Fatal(err)
	}
	if res2.Created != 0 || res2.Skipped != 3 {
		t.Fatalf("reimport got %+v", res2)
	}
	// 其他项目/租户互不影响
	res3, err := ImportOpenAPI(d, 2, 10, []byte(openAPIJSON))
	if err != nil {
		t.Fatal(err)
	}
	if res3.Created != 3 {
		t.Fatalf("tenant isolation got %+v", res3)
	}
	// 落库内容抽查：GET /users 带 query 参数，POST 带 body
	var get model.HttpApi
	if err := d.Where("project_id = 10 AND method = 1 AND uri = '/users' AND tenant_id = 1").First(&get).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(get.Params), "page") {
		t.Fatalf("params lost: %s", get.Params)
	}
	var post model.HttpApi
	if err := d.Where("project_id = 10 AND method = 2 AND uri = '/users' AND tenant_id = 1").First(&post).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(post.Body), "name") {
		t.Fatalf("body lost: %s", post.Body)
	}
}

func TestImportOpenAPIYAML(t *testing.T) {
	d := openTestDB(t)
	yamlDoc := `
openapi: 3.0.3
info: {title: demo, version: "1.0"}
paths:
  /health:
    get: {}
`
	res, err := ImportOpenAPI(d, 1, 1, []byte(yamlDoc))
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 1 {
		t.Fatalf("got %+v", res)
	}
}

func TestImportOpenAPIBad(t *testing.T) {
	d := openTestDB(t)
	if _, err := ImportOpenAPI(d, 1, 1, []byte(`{"openapi":"3.0.3"}`)); err == nil {
		t.Fatal("no paths should error")
	}
	if _, err := ImportOpenAPI(d, 1, 1, []byte("\x00\x01bad")); err == nil {
		t.Fatal("garbage should error")
	}
}

func TestParseCurl(t *testing.T) {
	// 显式方法 + header + body
	r, err := ParseCurl(`curl -XPUT 'http://api.local/v1/u/1' -H 'Content-Type: application/json' -H "X-T: a b" -d '{"x":1}'`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Method != "PUT" || r.URL != "http://api.local/v1/u/1" || r.Body != `{"x":1}` {
		t.Fatalf("got %+v", r)
	}
	if len(r.Headers) != 2 || r.Headers[0] != [2]string{"Content-Type", "application/json"} || r.Headers[1] != [2]string{"X-T", "a b"} {
		t.Fatalf("headers: %+v", r.Headers)
	}
	// 无 -X：带 -d 推 POST，否则 GET
	r, _ = ParseCurl("curl http://h/a -d x=1 -d y=2")
	if r.Method != "POST" || r.Body != "x=1&y=2" {
		t.Fatalf("data infer: %+v", r)
	}
	r, _ = ParseCurl("curl http://h/a")
	if r.Method != "GET" {
		t.Fatalf("get infer: %+v", r)
	}
	// -u 生成 Basic 头
	r, err = ParseCurl("curl -u alice:pw http://h/a")
	if err != nil || len(r.Headers) != 1 || r.Headers[0][0] != "Authorization" ||
		!strings.HasPrefix(r.Headers[0][1], "Basic ") {
		t.Fatalf("basic auth: %+v err=%v", r, err)
	}
	// --url 形式
	r, err = ParseCurl("curl --url http://h/b --request PATCH")
	if err != nil || r.URL != "http://h/b" || r.Method != "PATCH" {
		t.Fatalf("--url: %+v err=%v", r, err)
	}
	// 错误路径
	if _, err := ParseCurl("curl"); err == nil {
		t.Fatal("empty should error")
	}
	if _, err := ParseCurl("curl -H 'x: y'"); err == nil {
		t.Fatal("no URL should error")
	}
	if _, err := ParseCurl(`curl 'http://h/unterminated`); err == nil {
		t.Fatal("unterminated quote should error")
	}
}

func TestImportCurl(t *testing.T) {
	d := openTestDB(t)
	id, err := ImportCurl(d, 1, 5, `curl -X POST http://h/login -H 'Content-Type: application/json' -d '{"u":"a"}'`)
	if err != nil {
		t.Fatal(err)
	}
	var api model.HttpApi
	if err := d.Where("id = ?", id).First(&api).Error; err != nil {
		t.Fatal(err)
	}
	if api.Method != 2 || api.URI != "http://h/login" {
		t.Fatalf("got %+v", api)
	}
	if !strings.Contains(string(api.Headers), "Content-Type") || !strings.Contains(string(api.Body), `\"u\":\"a\"`) {
		t.Fatalf("headers/body: %s %s", api.Headers, api.Body)
	}
	// 未知方法拒绝
	if _, err := ImportCurl(d, 1, 5, "curl -X FLY http://h/x"); err == nil {
		t.Fatal("unsupported method should error")
	}
}

func TestExportOpenAPI(t *testing.T) {
	d := openTestDB(t)
	if _, err := ImportOpenAPI(d, 1, 10, []byte(openAPIJSON)); err != nil {
		t.Fatal(err)
	}
	out, err := ExportOpenAPI(d, 1, 10, "我的 API")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("export not valid JSON: %v", err)
	}
	if doc["openapi"] != "3.0.3" {
		t.Fatalf("openapi version: %v", doc["openapi"])
	}
	paths := doc["paths"].(map[string]any)
	if len(paths) != 2 {
		t.Fatalf("paths: %v", paths)
	}
	users := paths["/users"].(map[string]any)
	if users["get"] == nil || users["post"] == nil {
		t.Fatalf("/users ops: %v", users)
	}
	// GET 应带回 query 参数
	getOp := users["get"].(map[string]any)
	params := getOp["parameters"].([]any)
	if len(params) != 1 || params[0].(map[string]any)["name"] != "page" {
		t.Fatalf("get params: %v", params)
	}
	// 其他租户导出为空 paths
	out2, _ := ExportOpenAPI(d, 9, 10, "x")
	var doc2 map[string]any
	_ = json.Unmarshal(out2, &doc2)
	if len(doc2["paths"].(map[string]any)) != 0 {
		t.Fatalf("tenant leak: %s", out2)
	}
}

func TestExportCurl(t *testing.T) {
	d := openTestDB(t)
	// 经 ImportOpenAPI/ImportCurl 造数据
	if _, err := ImportOpenAPI(d, 1, 10, []byte(openAPIJSON)); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportCurl(d, 1, 10,
		`curl -X POST http://h/login -H 'Content-Type: application/json' -d '{"u":"a"}'`); err != nil {
		t.Fatal(err)
	}
	out, err := ExportCurl(d, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 { // openAPIJSON 3 条 + curl 导入 1 条
		t.Fatalf("lines=%d: %q", len(lines), out)
	}
	// POST /users 带 body；GET /users 带 query 参数（openapi 导入）
	var usersPost, usersGet string
	for _, l := range lines {
		if !strings.Contains(l, "'/users") { // URL 可能带 ?query
			continue
		}
		if strings.Contains(l, "POST") {
			usersPost = l
		} else if !strings.Contains(l, "-X") {
			usersGet = l
		}
	}
	if usersPost == "" || !strings.Contains(usersPost, `-d '`) {
		t.Fatalf("POST /users body missing: %q", out)
	}
	if usersGet == "" || !strings.Contains(usersGet, "?page=") {
		t.Fatalf("GET /users query param missing: %q", usersGet)
	}
	if strings.Contains(usersGet, "-X GET") {
		t.Fatalf("plain GET should omit -X: %q", usersGet)
	}
	// curl 导入的 POST /login：header 与 JSON body
	var login string
	for _, l := range lines {
		if strings.Contains(l, "/login") {
			login = l
		}
	}
	if !strings.Contains(login, `-X POST`) ||
		!strings.Contains(login, `Content-Type: application/json`) ||
		!strings.Contains(login, `{"u":"a"}`) {
		t.Fatalf("login line: %q", login)
	}
	// 单引号转义
	if _, err := ImportCurl(d, 1, 11, `curl 'http://h/q?a=it'"'"'s'`); err != nil {
		t.Fatal(err)
	}
	out2, _ := ExportCurl(d, 1, 11)
	if !strings.Contains(out2, "it'\\''s'") { // shell 标准转义：it'\''s
		t.Fatalf("quote escape: %q", out2)
	}
	// 租户隔离
	out3, _ := ExportCurl(d, 9, 10)
	if out3 != "" {
		t.Fatalf("tenant leak: %q", out3)
	}
}

// ---- ApplyOpenAPIDiff ----

func apiIDByURI(t *testing.T, d *gorm.DB, projectID int64, uri string, method int16) int64 {
	t.Helper()
	var a model.HttpApi
	if err := d.Where("project_id = ? AND uri = ? AND method = ?", projectID, uri, method).
		First(&a).Error; err != nil {
		t.Fatal(err)
	}
	return a.ID
}

func TestApplyOpenAPIDiffMatrix(t *testing.T) {
	d := openTestDB(t)
	if _, err := ImportOpenAPI(d, 1, 10, []byte(openAPIJSON)); err != nil {
		t.Fatal(err)
	}
	// 新 spec：/users GET 参数重命名（breaking）、/users POST 不变、
	// /users/{id} DELETE 消失（removed）、/ping 新增（added）
	doc := `{"openapi":"3.0.0","paths":{
		"/users": {
			"get": {"parameters": [{"name": "q", "in": "query"}]},
			"post": {"requestBody": {"content": {"application/json": {"schema": {"type": "object", "properties": {"name": {"type": "string"}}}}}}}
		},
		"/ping": {"get": {}}
	}}`
	res, err := ApplyOpenAPIDiff(d, 1, 10, []byte(doc), false)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, df := range res.Diffs {
		kinds[df.Kind]++
	}
	if kinds["added"] != 1 || kinds["breaking"] != 1 || kinds["removed"] != 1 || kinds["changed"] != 0 {
		t.Fatalf("kinds mismatch: %v (diffs=%+v)", kinds, res.Diffs)
	}
	if len(res.UpdatedAPIIDs) != 2 { // 新增 + breaking 更新
		t.Fatalf("updated ids=%v", res.UpdatedAPIIDs)
	}

	// /users GET 行参数已更新；DELETE 行保留
	var users model.HttpApi
	if err := d.Where("project_id = 10 AND uri = '/users' AND method = 1").First(&users).Error; err != nil {
		t.Fatal(err)
	}
	if string(users.Params) != `[{"key":"q","value":""}]` {
		t.Fatalf("params not updated: %s", users.Params)
	}
	if apiIDByURI(t, d, 10, "/users/{id}", 4) == 0 {
		t.Fatal("removed api should be kept")
	}

	// 再跑一轮：removed 行仍保留故重复报告；其余维度稳定无新增变更
	res2, err := ApplyOpenAPIDiff(d, 1, 10, []byte(doc), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Diffs) != 1 || res2.Diffs[0].Kind != "removed" {
		t.Fatalf("second run should only repeat removed, got %+v", res2.Diffs)
	}
}

func TestApplyOpenAPIDiffBodyContentTypeBreaking(t *testing.T) {
	d := openTestDB(t)
	// 存量 POST 为 JSON body；新 spec 改为 form-urlencoded → breaking + body 更新
	if _, err := ImportOpenAPI(d, 1, 10, []byte(`{"openapi":"3.0.0","paths":{
		"/login": {"post": {"requestBody": {"content": {"application/json": {"schema": {"type": "object"}}}}}}}}`)); err != nil {
		t.Fatal(err)
	}
	doc := `{"openapi":"3.0.0","paths":{
		"/login": {"post": {"requestBody": {"content": {"application/x-www-form-urlencoded": {"schema": {"type": "object"}}}}}}}
	}`
	res, err := ApplyOpenAPIDiff(d, 1, 10, []byte(doc), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Diffs) != 1 || res.Diffs[0].Kind != "breaking" {
		t.Fatalf("want breaking, got %+v", res.Diffs)
	}
	var api model.HttpApi
	if err := d.Where("project_id = 10 AND uri = '/login'").First(&api).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(api.Body), `"contentType":3`) { // X_WWW_FORM_URLENCODED
		t.Fatalf("body content_type not updated: %s", api.Body)
	}
}

func TestApplyOpenAPIDiffAutoUpdateCases(t *testing.T) {
	d := openTestDB(t)
	if _, err := ImportOpenAPI(d, 1, 10, []byte(openAPIJSON)); err != nil {
		t.Fatal(err)
	}
	apiID := apiIDByURI(t, d, 10, "/users", 1)

	// case A：顶层 + loop 嵌套均引用该 api_id；case B：引用另一接口（不该被碰）
	def := `{"steps": [
		{"name": "top", "type": 1, "api_call": {"api_id": "APIID", "override": {}}},
		{"name": "loop", "type": 6, "loop_step": {"iterator": "i", "count": 2, "body_steps": [
			{"name": "nested", "type": 1, "api_call": {"api_id": "APIID"}}]}}
	]}`
	def = strings.ReplaceAll(def, "APIID", strconv.FormatInt(apiID, 10))
	ca := model.TestCase{ID: model.NextID(), TenantID: 1, ProjectID: 10,
		Type: 1, Name: "affected", Definition: model.JSON(def)}
	cb := model.TestCase{ID: model.NextID(), TenantID: 1, ProjectID: 10,
		Type: 1, Name: "unaffected",
		Definition: model.JSON(`{"steps": [{"name": "x", "type": 1, "api_call": {"api_id": "999"}}]}`)}
	if err := d.Create(&ca).Error; err != nil {
		t.Fatal(err)
	}
	if err := d.Create(&cb).Error; err != nil {
		t.Fatal(err)
	}

	// 参数变化触发 diff + case 内联快照写回
	doc := `{"openapi":"3.0.0","paths":{
		"/users": {
			"get": {"parameters": [{"name": "q", "in": "query"}]},
			"post": {"requestBody": {"content": {"application/json": {"schema": {"type": "object", "properties": {"name": {"type": "string"}}}}}}}
		},
		"/users/{id}": {"delete": {}}
	}}`
	dr, err := ApplyOpenAPIDiff(d, 1, 10, []byte(doc), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(dr.UpdatedCaseIDs) != 1 || dr.UpdatedCaseIDs[0] != ca.ID {
		t.Fatalf("updated case ids=%v, want [%d]", dr.UpdatedCaseIDs, ca.ID)
	}

	var got model.TestCase
	if err := d.First(&got, ca.ID).Error; err != nil {
		t.Fatal(err)
	}
	var defMap map[string]any
	if err := json.Unmarshal([]byte(got.Definition), &defMap); err != nil {
		t.Fatal(err)
	}
	steps := defMap["steps"].([]any)
	top := steps[0].(map[string]any)["api_call"].(map[string]any)
	inline, ok := top["inline"].(map[string]any)
	if !ok {
		t.Fatalf("inline not written: %s", got.Definition)
	}
	if inline["method"] != "HTTP_METHOD_GET" || inline["uri"] != "/users" {
		t.Fatalf("inline mismatch: %v", inline)
	}
	if top["api_id"] != strconv.FormatInt(apiID, 10) {
		t.Fatalf("api_id should be preserved, got %v", top["api_id"])
	}
	if _, ok := top["override"].(map[string]any); !ok {
		t.Fatal("override should be preserved")
	}
	loop := steps[1].(map[string]any)["loop_step"].(map[string]any)
	nested := loop["body_steps"].([]any)[0].(map[string]any)["api_call"].(map[string]any)
	if _, ok := nested["inline"].(map[string]any); !ok {
		t.Fatal("nested api_call inline not written")
	}

	// case B 未被触碰
	var gotB model.TestCase
	if err := d.First(&gotB, cb.ID).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(gotB.Definition), "inline") {
		t.Fatalf("unrelated case should not be touched: %s", gotB.Definition)
	}
}

// ---- Postman 导入导出 ----

const postmanJSON = `{
  "info": {"name": "demo", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
  "item": [
    {"name": "users folder", "item": [
      {"name": "list users", "request": {
        "method": "GET",
        "url": {"raw": "https://api.example.com/users?page=2"},
        "header": [{"key": "X-Token", "value": "t1"}, {"key": "Host", "value": "ignored"}]}},
      {"name": "create user", "request": {
        "method": "POST",
        "url": {"raw": "https://api.example.com/users"},
        "header": [{"key": "Content-Type", "value": "application/json"}],
        "body": {"mode": "raw", "raw": "{\"name\": \"neo\"}"}}}
    ]},
    {"name": "ping (path form)", "request": {
      "method": "GET",
      "url": {"protocol": "https", "host": ["api.example.com"], "path": ["ping"]}}}
  ]
}`

func TestImportPostman(t *testing.T) {
	d := openTestDB(t)
	res, err := ImportPostman(d, 1, 10, []byte(postmanJSON))
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 3 || res.Skipped != 0 {
		t.Fatalf("got %+v", res)
	}

	// 幂等：重复导入全部跳过
	res2, err := ImportPostman(d, 1, 10, []byte(postmanJSON))
	if err != nil {
		t.Fatal(err)
	}
	if res2.Created != 0 || res2.Skipped != 3 {
		t.Fatalf("reimport got %+v", res2)
	}

	// 租户隔离
	res3, err := ImportPostman(d, 2, 10, []byte(postmanJSON))
	if err != nil {
		t.Fatal(err)
	}
	if res3.Created != 3 {
		t.Fatalf("tenant isolation got %+v", res3)
	}

	// 内容抽查：GET /users（query 拆分 + header 落库 + Host 忽略）
	var users model.HttpApi
	if err := d.Where("tenant_id = 1 AND project_id = 10 AND uri = '/users' AND method = 1").
		First(&users).Error; err != nil {
		t.Fatal(err)
	}
	if string(users.Params) != `[{"key":"page","value":"2"}]` {
		t.Fatalf("params: %s", users.Params)
	}
	if string(users.Headers) != `[{"key":"X-Token","value":"t1"}]` {
		t.Fatalf("headers: %s", users.Headers)
	}
	// POST /users body（JSON 探测）
	var post model.HttpApi
	if err := d.Where("tenant_id = 1 AND project_id = 10 AND uri = '/users' AND method = 2").
		First(&post).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(post.Body), `"contentType":4`) ||
		!strings.Contains(string(post.Body), `\"name\": \"neo\"`) { // 列内 JSON 转义存储
		t.Fatalf("body: %s", post.Body)
	}
	// path 数组形态
	var ping model.HttpApi
	if err := d.Where("tenant_id = 1 AND project_id = 10 AND uri = '/ping'").
		First(&ping).Error; err != nil {
		t.Fatal(err)
	}

	// 非法文档
	if _, err := ImportPostman(d, 1, 10, []byte(`{"info":{}}`)); err == nil {
		t.Fatal("missing item should error")
	}
	if _, err := ImportPostman(d, 1, 10, []byte(`not json`)); err == nil {
		t.Fatal("invalid json should error")
	}
}

func TestExportPostmanRoundtrip(t *testing.T) {
	d := openTestDB(t)
	if _, err := ImportPostman(d, 1, 10, []byte(postmanJSON)); err != nil {
		t.Fatal(err)
	}
	doc, err := ExportPostman(d, 1, 10, "roundtrip")
	if err != nil {
		t.Fatal(err)
	}
	var col map[string]any
	if err := json.Unmarshal(doc, &col); err != nil {
		t.Fatalf("export not valid json: %v", err)
	}
	if col["info"].(map[string]any)["name"] != "roundtrip" {
		t.Fatalf("title missing: %v", col["info"])
	}
	items := col["item"].([]any)
	if len(items) != 3 {
		t.Fatalf("items=%d, want 3", len(items))
	}
	// 首条应有 {{base_url}} 占位与 query
	first := items[0].(map[string]any)
	if !strings.Contains(first["request"].(map[string]any)["url"].(map[string]any)["raw"].(string), "{{base_url}}") {
		t.Fatalf("base_url placeholder missing: %v", first)
	}
	// 导出→导入 roundtrip：换项目导入应全量重建
	res, err := ImportPostman(d, 1, 11, doc)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 3 {
		t.Fatalf("roundtrip import: %+v", res)
	}
}
