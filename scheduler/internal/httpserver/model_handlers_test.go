package httpserver

import (
	"fmt"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/testpilot/testpilot/internal/model"
)

// mkModel 建数据模型（自动挂根/目标目录，返回 model id）。
func mkModel(t *testing.T, app *fiber.App, tok string, pid int64, parentID int64, name, schema string) int64 {
	t.Helper()
	parent := ""
	if parentID != 0 {
		parent = fmt.Sprintf(`,"parent_node_id":%d`, parentID)
	}
	code, out := postJSON(t, app, "/api/v1/models", tok, fmt.Sprintf(
		`{"project_id":%d,"name":%q,"schema":%s%s}`, pid, name, schema, parent))
	if code != 200 {
		t.Fatalf("create model %s: code=%d out=%v", name, code, out)
	}
	return idOf(t, out, "id")
}

// 新建数据模型：实体落库（schema 原样回读）+ 目录树挂载。
func TestCreateDataModelMountsTree(t *testing.T) {
	app, tok, pid, d := newTreeTest(t)
	code, fOut := postJSON(t, app, "/api/v1/tree/folders", tok, fmt.Sprintf(`{"project_id":%d,"name":"结构"}`, pid))
	if code != 200 {
		t.Fatalf("create folder: %v", fOut)
	}
	folderID := idOf(t, fOut, "id")
	mid := mkModel(t, app, tok, pid, folderID, "Response",
		`{"type":"object","properties":{"code":{"type":"integer"}}}`)

	// 实体落库 + schema 原样回读（writeJSON 的 ID 字符串化不影响 schema 字段）
	code, out := sendJSON(t, app, "GET", fmt.Sprintf("/api/v1/models/%d", mid), tok, "")
	if code != 200 {
		t.Fatalf("get model: code=%d", code)
	}
	if got := out["name"]; got != "Response" {
		t.Fatalf("name = %v", got)
	}
	gotSchema, ok := out["schema"].(map[string]any)
	if !ok || gotSchema["type"] != "object" {
		t.Fatalf("schema roundtrip failed: %v", out["schema"])
	}
	if _, has := out["schema_text"]; has {
		t.Fatalf("column name schema_text must not leak as json key: %v", out)
	}

	// 树节点挂在目录下且 node_type=7
	var n model.TreeNode
	if err := d.Where("node_type = ? AND ref_id = ?", model.NodeTypeDataMdl, mid).First(&n).Error; err != nil {
		t.Fatalf("mount node missing: %v", err)
	}
	if n.ParentID != folderID {
		t.Fatalf("node parent = %d, want %d", n.ParentID, folderID)
	}
	if n.Name != "Response" {
		t.Fatalf("node name = %q", n.Name)
	}
}

// kind=models 树只出数据模型叶子；kind=api 树不出现数据模型。
func TestTreeKindModels(t *testing.T) {
	app, tok, pid, _ := newTreeTest(t)
	mkAPI(t, app, tok, pid, 0, "/a")
	mid := mkModel(t, app, tok, pid, 0, "PageResponse", `{"type":"object"}`)

	findModel := func(out map[string]any, want bool) bool {
		tree, _ := out["tree"].([]any)
		found := false
		var walk func(nodes []any)
		walk = func(nodes []any) {
			for _, x := range nodes {
				nm, _ := x.(map[string]any)
				if nm == nil {
					continue
				}
				if nm["node_type"] == float64(model.NodeTypeDataMdl) {
					if !want || nm["ref_id"] == fmt.Sprint(mid) {
						found = true
					}
				}
				if kids, ok := nm["children"].([]any); ok {
					walk(kids)
				}
			}
		}
		walk(tree)
		return found
	}

	code, out := sendJSON(t, app, "GET", fmt.Sprintf("/api/v1/tree?project_id=%d&kind=models", pid), tok, "")
	if code != 200 {
		t.Fatalf("tree models: code=%d", code)
	}
	if !findModel(out, true) {
		t.Fatalf("kind=models tree missing model %d: %v", mid, out)
	}

	// kind=api（默认）不应包含数据模型节点
	_, out = sendJSON(t, app, "GET", fmt.Sprintf("/api/v1/tree?project_id=%d", pid), tok, "")
	if findModel(out, false) {
		t.Fatal("kind=api tree must not contain data model nodes")
	}
}

// 更新与删除：PUT 部分字段覆盖；DELETE 级联删挂载节点（实体软删）。
func TestUpdateDeleteDataModel(t *testing.T) {
	app, tok, pid, d := newTreeTest(t)
	mid := mkModel(t, app, tok, pid, 0, "M1", `{"type":"string"}`)

	code, out := putJSON(t, app, fmt.Sprintf("/api/v1/models/%d", mid), tok,
		`{"name":"M2","schema":{"type":"integer","description":"新说明"}}`)
	if code != 200 {
		t.Fatalf("update model: code=%d out=%v", code, out)
	}
	code, out = sendJSON(t, app, "GET", fmt.Sprintf("/api/v1/models/%d", mid), tok, "")
	if code != 200 {
		t.Fatalf("get model: code=%d", code)
	}
	if out["name"] != "M2" {
		t.Fatalf("name = %v, want M2", out["name"])
	}
	gotSchema, _ := out["schema"].(map[string]any)
	if gotSchema == nil || gotSchema["type"] != "integer" {
		t.Fatalf("schema = %v, want integer type", out["schema"])
	}

	// 删除级联删挂载节点
	code, out = delJSON(t, app, fmt.Sprintf("/api/v1/models/%d", mid), tok)
	if code != 200 {
		t.Fatalf("delete model: code=%d out=%v", code, out)
	}
	var cnt int64
	d.Model(&model.TreeNode{}).Where("node_type = ? AND ref_id = ?", model.NodeTypeDataMdl, mid).Count(&cnt)
	if cnt != 0 {
		t.Fatal("tree node still exists after model delete")
	}
	var m model.DataModel
	if err := d.Unscoped().First(&m, mid).Error; err != nil {
		t.Fatalf("soft-deleted row should still exist (unscoped): %v", err)
	}
	if !m.DeletedAt.Valid {
		t.Fatal("expected soft delete (deleted_at set)")
	}
}

// 接口设计态字段（*_design / request_schema / response_schema）经 PUT /apis/:id 持久化并回读。
func TestHttpApiDesignFieldsRoundtrip(t *testing.T) {
	app, tok, pid, d := newTreeTest(t)
	aID := mkAPI(t, app, tok, pid, 0, "/page")
	code, out := putJSON(t, app, fmt.Sprintf("/api/v1/apis/%d", aID), tok, `{
		"params_design": [
			{"name":"pageNum","type":"integer","required":true,"default":"1","description":"页码"},
			{"name":"pageSize","type":"integer","default":"20","description":"每页大小"}
		],
		"headers_design": [{"name":"X-Trace","type":"string","description":"链路ID"}],
		"cookies_design": [{"name":"sid","type":"string","required":true,"description":"会话"}],
		"request_schema": {"type":"object","properties":{"name":{"type":"string"}},"required":["name"]},
		"response_schema": {"type":"object","properties":{"code":{"type":"integer"}}}
	}`)
	if code != 200 {
		t.Fatalf("update api design: code=%d out=%v", code, out)
	}
	var a model.HttpApi
	if err := d.First(&a, aID).Error; err != nil {
		t.Fatal(err)
	}
	if len(a.ParamsDesign) == 0 || len(a.HeadersDesign) == 0 || len(a.CookiesDesign) == 0 ||
		len(a.RequestSchema) == 0 || len(a.ResponseSchema) == 0 {
		t.Fatalf("design columns not persisted: headers=%s cookies=%s req=%s resp=%s",
			a.HeadersDesign, a.CookiesDesign, a.RequestSchema, a.ResponseSchema)
	}
	// REST 回读：schema 字段直接是 JSON 对象
	code, out = sendJSON(t, app, "GET", fmt.Sprintf("/api/v1/apis/%d", aID), tok, "")
	if code != 200 {
		t.Fatalf("get api: code=%d", code)
	}
	pd, ok := out["params_design"].([]any)
	if !ok || len(pd) != 2 {
		t.Fatalf("params_design roundtrip failed: %v", out["params_design"])
	}
	first, _ := pd[0].(map[string]any)
	if first["name"] != "pageNum" || first["type"] != "integer" || first["required"] != true {
		t.Fatalf("params_design[0] = %v", first)
	}
	if _, ok := out["request_schema"].(map[string]any); !ok {
		t.Fatalf("request_schema roundtrip failed: %v", out["request_schema"])
	}
}
