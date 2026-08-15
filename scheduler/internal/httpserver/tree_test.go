package httpserver

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/testpilot/testpilot/internal/auth"
	"github.com/testpilot/testpilot/internal/config"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// newTreeTest 构造 app + 已认证 token + 一个项目（直接落库，不走注册流程）。
func newTreeTest(t *testing.T) (*fiber.App, string, int64, *gorm.DB) {
	t.Helper()
	app, d := newTestApp(t, config.Defaults())
	tok, err := auth.IssueToken(config.Defaults().JWTSecret, 1, 1, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	p := model.Project{ID: model.NextID(), TenantID: 1, Name: "tp"}
	if err := d.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	return app, tok, p.ID, d
}

func putJSON(t *testing.T, app *fiber.App, path, token, body string) (int, map[string]any) {
	t.Helper()
	return sendJSON(t, app, fiber.MethodPut, path, token, body)
}

func delJSON(t *testing.T, app *fiber.App, path, token string) (int, map[string]any) {
	t.Helper()
	return sendJSON(t, app, fiber.MethodDelete, path, token, "")
}

// idOf 从响应里取雪花 id（后端统一序列化为字符串）。
func idOf(t *testing.T, out map[string]any, key string) int64 {
	t.Helper()
	raw, ok := out[key]
	if !ok {
		t.Fatalf("response missing %q: %v", key, out)
	}
	switch v := raw.(type) {
	case string:
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			t.Fatalf("bad id %q: %v", v, err)
		}
		return id
	case float64:
		return int64(v)
	default:
		t.Fatalf("unexpected id type %T: %v", raw, raw)
		return 0
	}
}

// mkAPI 建接口（自动挂根/目标目录，返回 api id）。
func mkAPI(t *testing.T, app *fiber.App, tok string, pid int64, parentID int64, uri string) int64 {
	t.Helper()
	body := fmt.Sprintf(`{"project_id":%d,"method":1,"uri":%q,"name":%q}`, pid, uri, uri)
	if parentID != 0 {
		body = fmt.Sprintf(`{"project_id":%d,"method":1,"uri":%q,"name":%q,"parent_id":%d}`, pid, uri, uri, parentID)
	}
	code, out := postJSON(t, app, "/api/v1/apis", tok, body)
	if code != 200 {
		t.Fatalf("create api %s: code=%d out=%v", uri, code, out)
	}
	return idOf(t, out, "id")
}

// nodeOf 查 api 的挂载节点。
func nodeOf(t *testing.T, d *gorm.DB, apiID int64) model.TreeNode {
	t.Helper()
	var n model.TreeNode
	if err := d.Where("node_type = ? AND ref_id = ?", model.NodeTypeHTTPAPI, apiID).First(&n).Error; err != nil {
		t.Fatalf("mount node of api %d not found: %v", apiID, err)
	}
	return n
}

// childrenOrders 返回某父节点下子节点（order asc）的 ref/name 列表，用于断言顺序。
func childrenOrders(t *testing.T, d *gorm.DB, parentID int64) []string {
	t.Helper()
	var ns []model.TreeNode
	if err := d.Where("parent_id = ?", parentID).Order(`"order" asc, id asc`).Find(&ns).Error; err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		if n.NodeType == model.NodeTypeHTTPAPI {
			var a model.HttpApi
			d.First(&a, n.RefID)
			out = append(out, a.URI)
		} else {
			out = append(out, n.Name)
		}
	}
	return out
}

// 新建目录追加到末尾（order 递增）。
func TestCreateFolderAppendsToEnd(t *testing.T) {
	app, tok, pid, d := newTreeTest(t)
	postJSON(t, app, "/api/v1/tree/folders", tok, fmt.Sprintf(`{"project_id":%d,"name":"f1"}`, pid))
	code, out := postJSON(t, app, "/api/v1/tree/folders", tok, fmt.Sprintf(`{"project_id":%d,"name":"f2"}`, pid))
	if code != 200 {
		t.Fatalf("code=%d out=%v", code, out)
	}
	got := childrenOrders(t, d, 0)
	want := []string{"f1", "f2"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("root children = %v, want %v", got, want)
	}
}

// 新建接口追加到根/目录末尾，而不是跳到顶部。
func TestCreateAPIMountsAppendToEnd(t *testing.T) {
	app, tok, pid, d := newTreeTest(t)
	mkAPI(t, app, tok, pid, 0, "/a")
	mkAPI(t, app, tok, pid, 0, "/b")
	got := childrenOrders(t, d, 0)
	if len(got) != 2 || got[0] != "/a" || got[1] != "/b" {
		t.Fatalf("root children = %v, want [/a /b]", got)
	}
	// 指定目录：追加到该目录末尾
	code, out := postJSON(t, app, "/api/v1/tree/folders", tok, fmt.Sprintf(`{"project_id":%d,"name":"F"}`, pid))
	if code != 200 {
		t.Fatalf("create folder: %v", out)
	}
	fid := idOf(t, out, "id")
	mkAPI(t, app, tok, pid, fid, "/f-1")
	mkAPI(t, app, tok, pid, fid, "/f-2")
	got = childrenOrders(t, d, fid)
	if len(got) != 2 || got[0] != "/f-1" || got[1] != "/f-2" {
		t.Fatalf("folder children = %v, want [/f-1 /f-2]", got)
	}
}

// moveNode：跨目录 + index 精确插入 / 缺省追加末尾。
func TestMoveNodeWithIndex(t *testing.T) {
	app, tok, pid, d := newTreeTest(t)
	mkAPI(t, app, tok, pid, 0, "/a")
	bID := mkAPI(t, app, tok, pid, 0, "/b")
	mkAPI(t, app, tok, pid, 0, "/c")
	_, fOut := postJSON(t, app, "/api/v1/tree/folders", tok, fmt.Sprintf(`{"project_id":%d,"name":"F"}`, pid))
	fid := idOf(t, fOut, "id")

	// B 移入 F，index 0
	idx := 0
	code, out := putJSON(t, app, fmt.Sprintf("/api/v1/tree/nodes/%d/move", nodeOf(t, d, bID).ID), tok,
		fmt.Sprintf(`{"parent_id":%d,"index":%d}`, fid, idx))
	if code != 200 {
		t.Fatalf("move: %v", out)
	}
	if got := childrenOrders(t, d, fid); len(got) != 1 || got[0] != "/b" {
		t.Fatalf("F children = %v, want [/b]", got)
	}
	// C 移入 F，index 0 → 插到 B 前面
	cID := mkAPI(t, app, tok, pid, 0, "/c2")
	code, _ = putJSON(t, app, fmt.Sprintf("/api/v1/tree/nodes/%d/move", nodeOf(t, d, cID).ID), tok,
		fmt.Sprintf(`{"parent_id":%d,"index":0}`, fid))
	if code != 200 {
		t.Fatalf("move c2: code=%d", code)
	}
	if got := childrenOrders(t, d, fid); len(got) != 2 || got[0] != "/c2" || got[1] != "/b" {
		t.Fatalf("F children = %v, want [/c2 /b]", got)
	}
	// A 移入 F，无 index → 追加末尾
	aID := mkAPI(t, app, tok, pid, 0, "/a2")
	code, _ = putJSON(t, app, fmt.Sprintf("/api/v1/tree/nodes/%d/move", nodeOf(t, d, aID).ID), tok,
		fmt.Sprintf(`{"parent_id":%d}`, fid))
	if code != 200 {
		t.Fatalf("move a2: code=%d", code)
	}
	if got := childrenOrders(t, d, fid); len(got) != 3 || got[2] != "/a2" {
		t.Fatalf("F children = %v, want end with /a2", got)
	}
}

// mountAPI：未挂载接口挂到指定位置（index 插入）。
func TestMountAPIWithIndex(t *testing.T) {
	app, tok, pid, d := newTreeTest(t)
	_, fOut := postJSON(t, app, "/api/v1/tree/folders", tok, fmt.Sprintf(`{"project_id":%d,"name":"F"}`, pid))
	fid := idOf(t, fOut, "id")
	mkAPI(t, app, tok, pid, fid, "/x")
	yID := mkAPI(t, app, tok, pid, fid, "/y")
	// 摘挂 y
	code, _ := delJSON(t, app, fmt.Sprintf("/api/v1/tree/nodes/%d", nodeOf(t, d, yID).ID), tok)
	if code != 200 {
		t.Fatalf("unmount: code=%d", code)
	}
	// 挂回 index 0 → 到 /x 前面
	idx := 0
	code, out := postJSON(t, app, "/api/v1/tree/nodes", tok,
		fmt.Sprintf(`{"project_id":%d,"api_id":%d,"parent_id":%d,"index":%d}`, pid, yID, fid, idx))
	if code != 200 {
		t.Fatalf("mount: code=%d out=%v", code, out)
	}
	if got := childrenOrders(t, d, fid); len(got) != 2 || got[0] != "/y" || got[1] != "/x" {
		t.Fatalf("F children = %v, want [/y /x]", got)
	}
}

// deleteAPI：删除接口时级联删除挂载节点（不留孤儿）。
func TestDeleteAPICascadesTreeNode(t *testing.T) {
	app, tok, pid, d := newTreeTest(t)
	aID := mkAPI(t, app, tok, pid, 0, "/gone")
	n := nodeOf(t, d, aID)
	code, out := delJSON(t, app, fmt.Sprintf("/api/v1/apis/%d", aID), tok)
	if code != 200 {
		t.Fatalf("delete api: code=%d out=%v", code, out)
	}
	var cnt int64
	d.Model(&model.TreeNode{}).Where("id = ?", n.ID).Count(&cnt)
	if cnt != 0 {
		t.Fatalf("tree node %d still exists after api delete", n.ID)
	}
}

// reorderTree：同一父节点重排。
func TestReorderTree(t *testing.T) {
	app, tok, pid, d := newTreeTest(t)
	aID := mkAPI(t, app, tok, pid, 0, "/a")
	bID := mkAPI(t, app, tok, pid, 0, "/b")
	na, nb := nodeOf(t, d, aID), nodeOf(t, d, bID)
	code, out := putJSON(t, app, "/api/v1/tree/reorder", tok,
		fmt.Sprintf(`{"parent_id":0,"ids":[%d,%d]}`, nb.ID, na.ID))
	if code != 200 {
		t.Fatalf("reorder: code=%d out=%v", code, out)
	}
	if got := childrenOrders(t, d, 0); len(got) != 2 || got[0] != "/b" || got[1] != "/a" {
		t.Fatalf("root children = %v, want [/b /a]", got)
	}
}
