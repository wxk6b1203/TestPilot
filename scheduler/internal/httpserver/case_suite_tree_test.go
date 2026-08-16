package httpserver

import (
	"fmt"
	"testing"

	"github.com/testpilot/testpilot/internal/model"
)

// TestCaseSuiteTreeMountAndFilter 验证用例/套件可挂到目录，且按 kind 过滤树。
func TestCaseSuiteTreeMountAndFilter(t *testing.T) {
	app, tok, pid, _ := newTreeTest(t)

	// 目录
	code, out := postJSON(t, app, "/api/v1/tree/folders", tok,
		fmt.Sprintf(`{"project_id":%d,"name":"folder"}`, pid))
	if code != 200 {
		t.Fatalf("create folder: %d %v", code, out)
	}
	folderID := idOf(t, out, "id")

	// 用例 + 套件
	code, out = postJSON(t, app, "/api/v1/cases", tok,
		fmt.Sprintf(`{"project_id":%d,"name":"case-a","type":1,"description":"d"}`, pid))
	if code != 200 {
		t.Fatalf("create case: %d %v", code, out)
	}
	caseID := idOf(t, out, "id")
	code, out = postJSON(t, app, "/api/v1/suites", tok,
		fmt.Sprintf(`{"project_id":%d,"name":"suite-a","description":"d","case_ids":[]}`, pid))
	if code != 200 {
		t.Fatalf("create suite: %d %v", code, out)
	}
	suiteID := idOf(t, out, "id")

	// 挂到目录
	for _, body := range []string{
		fmt.Sprintf(`{"project_id":%d,"ref_type":%d,"ref_id":%d,"parent_id":%d}`, pid, model.NodeTypeTestCase, caseID, folderID),
		fmt.Sprintf(`{"project_id":%d,"ref_type":%d,"ref_id":%d,"parent_id":%d}`, pid, model.NodeTypeSuite, suiteID, folderID),
	} {
		code, out = postJSON(t, app, "/api/v1/tree/nodes", tok, body)
		if code != 200 {
			t.Fatalf("mount node: %d %v", code, out)
		}
	}

	// case 树只含用例
	code, out = getJSON(t, app, "/api/v1/tree?project_id="+fmt.Sprint(pid)+"&kind=case", tok)
	if code != 200 {
		t.Fatalf("case tree: %d %v", code, out)
	}
	tree := out["tree"].([]any)
	if len(tree) != 1 {
		t.Fatalf("case tree roots = %d, want 1", len(tree))
	}
	folder := tree[0].(map[string]any)
	if folder["name"] != "folder" {
		t.Fatalf("case tree folder name = %v", folder["name"])
	}
	children := folder["children"].([]any)
	if len(children) != 1 {
		t.Fatalf("case tree children = %d, want 1", len(children))
	}
	leaf := children[0].(map[string]any)
	if leaf["node_type"] != float64(model.NodeTypeTestCase) {
		t.Fatalf("case leaf node_type = %v", leaf["node_type"])
	}

	// suite 树只含套件
	code, out = getJSON(t, app, "/api/v1/tree?project_id="+fmt.Sprint(pid)+"&kind=suite", tok)
	if code != 200 {
		t.Fatalf("suite tree: %d %v", code, out)
	}
	tree = out["tree"].([]any)
	if len(tree) != 1 {
		t.Fatalf("suite tree roots = %d, want 1", len(tree))
	}
	children = tree[0].(map[string]any)["children"].([]any)
	if len(children) != 1 {
		t.Fatalf("suite tree children = %d, want 1", len(children))
	}

	// 默认 api 树应裁剪掉没有接口的空目录
	code, out = getJSON(t, app, "/api/v1/tree?project_id="+fmt.Sprint(pid), tok)
	if code != 200 {
		t.Fatalf("api tree: %d %v", code, out)
	}
	tree = out["tree"].([]any)
	if len(tree) != 0 {
		t.Fatalf("api tree should be empty when folder has no api, got %v", tree)
	}

	// 删除用例会级联摘除树节点，case 树恢复为空
	code, _ = sendJSON(t, app, "DELETE", "/api/v1/cases/"+fmt.Sprint(caseID), tok, "")
	if code != 200 {
		t.Fatalf("delete case: %d", code)
	}
	code, out = getJSON(t, app, "/api/v1/tree?project_id="+fmt.Sprint(pid)+"&kind=case", tok)
	if code != 200 {
		t.Fatalf("case tree after delete: %d %v", code, out)
	}
	if len(out["tree"].([]any)) != 0 {
		t.Fatalf("case tree should be empty after delete, got %v", out["tree"])
	}
	// 套件树不受影响
	code, out = getJSON(t, app, "/api/v1/tree?project_id="+fmt.Sprint(pid)+"&kind=suite", tok)
	if code != 200 {
		t.Fatalf("suite tree after case delete: %d %v", code, out)
	}
	if len(out["tree"].([]any)) != 1 {
		t.Fatalf("suite tree should remain, got %v", out["tree"])
	}
}
