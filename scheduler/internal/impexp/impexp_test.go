package impexp

import (
	"encoding/json"
	"path/filepath"
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
