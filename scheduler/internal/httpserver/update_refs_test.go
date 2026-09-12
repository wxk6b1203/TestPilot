package httpserver

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/testpilot/testpilot/internal/cronsched"
	"github.com/testpilot/testpilot/internal/config"
	"github.com/testpilot/testpilot/internal/db"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// seedCrossTenantRefs 建两个租户各自的 project/env/case，返回其 ID（跨租户引用注入用）。
func seedCrossTenantRefs(t *testing.T, d *gorm.DB) (proj1, proj2, env1, case1, case2 int64) {
	t.Helper()
	proj1, proj2 = model.NextID(), model.NextID()
	env1 = model.NextID()
	case1, case2 = model.NextID(), model.NextID()
	for _, row := range []any{
		&model.Project{ID: proj1, TenantID: 1, Name: "p1"},
		&model.Project{ID: proj2, TenantID: 2, Name: "p2"},
		&model.Environment{ID: env1, TenantID: 1, ProjectID: proj1, Name: "e1", BaseURL: "https://a.test"},
		&model.TestCase{ID: case1, TenantID: 1, ProjectID: proj1,
			Type: 1, Name: "c1", Definition: model.JSON(`{}`)},
		&model.TestCase{ID: case2, TenantID: 2, ProjectID: proj2,
			Type: 1, Name: "c2", Definition: model.JSON(`{}`)},
	} {
		if err := d.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return
}

// TestUpdatePlanMergesNotReplaces 回归：updatePlan 解码到全新 struct 后 Save 全字段写，
// body 未带的字段（project_id/env_id/created_at 等）被清零；且不校验引用归属，
// project_id/env_id 可指向他租户。修复后：合并解码保留未传字段 + validateRefs 拦截。
func TestUpdatePlanMergesNotReplaces(t *testing.T) {
	cfg := config.Defaults()
	cfg.JWTSecret = "test-secret-0123456789abcdef"
	app, d := newTestApp(t, cfg)
	tok := tokenFor(t, d, 1, 1, 1)
	proj1, proj2, env1, _, case2 := seedCrossTenantRefs(t, d)

	// 建 plan（带 project/env），随后只改 name
	code, out := postJSON(t, app, "/api/v1/plans", tok,
		`{"project_id":"`+i64s(proj1)+`","env_id":"`+i64s(env1)+`","name":"plan"}`)
	if code != 200 {
		t.Fatalf("create plan: %d %v", code, out)
	}
	planID := idOf(t, out, "id")
	var before model.TestPlan
	if err := d.First(&before, planID).Error; err != nil {
		t.Fatal(err)
	}

	code, out = putJSON(t, app, "/api/v1/plans/"+i64s(planID), tok, `{"name":"renamed"}`)
	if code != 200 {
		t.Fatalf("update plan: %d %v", code, out)
	}
	var after model.TestPlan
	if err := d.First(&after, planID).Error; err != nil {
		t.Fatal(err)
	}
	if after.Name != "renamed" {
		t.Fatalf("name = %q, want renamed", after.Name)
	}
	if after.ProjectID != proj1 || after.EnvID != env1 {
		t.Fatalf("unspecified refs must be preserved: project=%d env=%d", after.ProjectID, after.EnvID)
	}
	if !after.CreatedAt.Equal(before.CreatedAt) {
		t.Fatalf("created_at changed: %v → %v", before.CreatedAt, after.CreatedAt)
	}

	// 跨租户引用注入：project_id 指向租户 2 的 project → 400 且落库值不变
	code, _ = putJSON(t, app, "/api/v1/plans/"+i64s(planID), tok,
		`{"project_id":"`+i64s(proj2)+`"}`)
	if code != 400 {
		t.Fatalf("cross-tenant project_id should 400, got %d", code)
	}
	var after2 model.TestPlan
	d.First(&after2, planID)
	if after2.ProjectID != proj1 {
		t.Fatalf("cross-tenant project_id must not persist, got %d", after2.ProjectID)
	}

	// items 引用他租户 case → 400
	code, _ = putJSON(t, app, "/api/v1/plans/"+i64s(planID), tok,
		`{"items":[{"ref_type":1,"ref_id":"`+i64s(case2)+`","enabled":true}]}`)
	if code != 400 {
		t.Fatalf("cross-tenant item ref should 400, got %d", code)
	}
}

// TestUpdateSuiteMergesNotReplaces 回归：updateSuite 同样全字段写清零 + 缺引用校验。
func TestUpdateSuiteMergesNotReplaces(t *testing.T) {
	cfg := config.Defaults()
	cfg.JWTSecret = "test-secret-0123456789abcdef"
	app, d := newTestApp(t, cfg)
	tok := tokenFor(t, d, 1, 1, 1)
	proj1, proj2, _, case1, case2 := seedCrossTenantRefs(t, d)

	code, out := postJSON(t, app, "/api/v1/suites", tok,
		`{"project_id":"`+i64s(proj1)+`","name":"suite","case_ids":["`+i64s(case1)+`"]}`)
	if code != 200 {
		t.Fatalf("create suite: %d %v", code, out)
	}
	suiteID := idOf(t, out, "id")
	var before model.TestSuite
	if err := d.First(&before, suiteID).Error; err != nil {
		t.Fatal(err)
	}

	code, out = putJSON(t, app, "/api/v1/suites/"+i64s(suiteID), tok, `{"name":"renamed"}`)
	if code != 200 {
		t.Fatalf("update suite: %d %v", code, out)
	}
	var after model.TestSuite
	if err := d.First(&after, suiteID).Error; err != nil {
		t.Fatal(err)
	}
	if after.Name != "renamed" {
		t.Fatalf("name = %q, want renamed", after.Name)
	}
	if after.ProjectID != proj1 {
		t.Fatalf("project_id must be preserved, got %d", after.ProjectID)
	}
	if !after.CreatedAt.Equal(before.CreatedAt) {
		t.Fatalf("created_at changed: %v → %v", before.CreatedAt, after.CreatedAt)
	}
	// 未传 case_ids：成员保持原样（items 表不受 plan 字段合并影响）
	if got := suiteCaseIDs(d, suiteID); len(got) != 1 || got[0] != case1 {
		t.Fatalf("case_ids = %v, want [%d]", got, case1)
	}

	// 跨租户引用注入
	code, _ = putJSON(t, app, "/api/v1/suites/"+i64s(suiteID), tok,
		`{"project_id":"`+i64s(proj2)+`"}`)
	if code != 400 {
		t.Fatalf("cross-tenant project_id should 400, got %d", code)
	}
	code, _ = putJSON(t, app, "/api/v1/suites/"+i64s(suiteID), tok,
		`{"case_ids":["`+i64s(case2)+`"]}`)
	if code != 400 {
		t.Fatalf("cross-tenant case_ids should 400, got %d", code)
	}
	var after2 model.TestSuite
	d.First(&after2, suiteID)
	if after2.ProjectID != proj1 {
		t.Fatalf("cross-tenant project_id must not persist, got %d", after2.ProjectID)
	}
	if got := suiteCaseIDs(d, suiteID); len(got) != 1 || got[0] != case1 {
		t.Fatalf("rejected update must not touch members, got %v", got)
	}
}

// TestCustomCreateValidatesProjectRef 回归：自定义创建路径（不走 createOf）此前
// 不校验 project_id 归属——可把资源挂到他租户 project 下（跨租户污染）。
func TestCustomCreateValidatesProjectRef(t *testing.T) {
	cfg := config.Defaults()
	cfg.JWTSecret = "test-secret-0123456789abcdef"
	app, d := newTestApp(t, cfg)
	tok := tokenFor(t, d, 1, 1, 1)
	proj1, proj2, _, _, _ := seedCrossTenantRefs(t, d)

	// 各创建端点：他租户 project_id → 400
	cases := []struct{ path, body string }{
		{"/api/v1/apis", `{"project_id":"` + i64s(proj2) + `","method":1,"uri":"/x"}`},
		{"/api/v1/grpc-apis", `{"project_id":"` + i64s(proj2) + `","full_service":"pkg.Svc","method":"Get"}`},
		{"/api/v1/proto-files", `{"project_id":"` + i64s(proj2) + `","filename":"a.proto","content":"syntax"}`},
		{"/api/v1/scripts", `{"project_id":"` + i64s(proj2) + `","content":"print(1)"}`},
		{"/api/v1/tree/folders", `{"project_id":"` + i64s(proj2) + `","name":"f"}`},
	}
	for _, c := range cases {
		code, _ := postJSON(t, app, c.path, tok, c.body)
		if code != 400 {
			t.Fatalf("POST %s with cross-tenant project_id should 400, got %d", c.path, code)
		}
	}

	// 落库零污染：本租户名下不得出现指向他租户 project 的行
	var n int64
	d.Model(&model.HttpApi{}).Where("tenant_id = ? AND project_id = ?", 1, proj2).Count(&n)
	if n != 0 {
		t.Fatalf("http_api rows leaked to foreign project: %d", n)
	}
	d.Model(&model.TreeNode{}).Where("tenant_id = ? AND project_id = ?", 1, proj2).Count(&n)
	if n != 0 {
		t.Fatalf("tree node rows leaked to foreign project: %d", n)
	}

	// mount：挂载实体属本租户，但 project_id 注入他租户 → 400
	code, out := postJSON(t, app, "/api/v1/apis", tok,
		`{"project_id":"`+i64s(proj1)+`","method":1,"uri":"/ok"}`)
	if code != 200 {
		t.Fatalf("create api: %d %v", code, out)
	}
	apiID := idOf(t, out, "id")
	code, _ = postJSON(t, app, "/api/v1/tree/nodes", tok,
		`{"project_id":"`+i64s(proj2)+`","api_id":"`+i64s(apiID)+`"}`)
	if code != 400 {
		t.Fatalf("mount with cross-tenant project_id should 400, got %d", code)
	}

	// 正常引用不受影响：本租户 project 仍可创建
	code, _ = postJSON(t, app, "/api/v1/tree/folders", tok,
		`{"project_id":"`+i64s(proj1)+`","name":"root"}`)
	if code != 200 {
		t.Fatalf("folder with own project should 200, got %d", code)
	}
}

// TestDeletePlanDisablesSchedules 回归：删 Plan 不清理 Schedule——cron 条目按
// 表达式持续触发，fire 因 plan 软删空转告警，重启时 NextRunAt 过期还会 misfire
// 补跑。修复后删计划须级联禁用引用它的 enabled schedule 并摘除 cron 条目。
func TestDeletePlanDisablesSchedules(t *testing.T) {
	cfg := config.Defaults()
	cfg.JWTSecret = "test-secret-0123456789abcdef"
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), "", db.Pool{})
	if err != nil {
		t.Fatal(err)
	}
	cronSched := cronsched.New(d, nil) // 测试不触发 fire，runner 传 nil
	app := New(d, cfg, nil, nil, cronSched, nil).App()
	tok := tokenFor(t, d, 1, 1, 1)

	proj1, _, _, _, _ := seedCrossTenantRefs(t, d)
	plan := model.TestPlan{ID: model.NextID(), TenantID: 1, ProjectID: proj1, Name: "planned"}
	if err := d.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	sc := &model.Schedule{ID: model.NextID(), TenantID: 1, PlanID: plan.ID,
		CronExpr: "0 0 1 1 *", Enabled: true}
	if err := d.Create(sc).Error; err != nil {
		t.Fatal(err)
	}
	cronSched.Sync(sc) // 注册 cron 条目（对齐 createSchedule）

	code, out := delJSON(t, app, "/api/v1/plans/"+i64s(plan.ID), tok)
	if code != 200 {
		t.Fatalf("delete plan: %d %v", code, out)
	}
	// plan 软删、schedule 禁用（fire/Start 均按 enabled 过滤，空转与 misfire 消失）
	var got model.Schedule
	if err := d.First(&got, sc.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Enabled {
		t.Fatal("schedule referencing deleted plan must be disabled")
	}
	var plans int64
	d.Unscoped().Model(&model.TestPlan{}).Where("id = ?", plan.ID).Count(&plans)
	if plans != 1 {
		t.Fatal("plan row should soft-delete, not hard-delete")
	}
	// 再删 → 404
	code, _ = delJSON(t, app, "/api/v1/plans/"+i64s(plan.ID), tok)
	if code != 404 {
		t.Fatalf("second delete should 404, got %d", code)
	}
}

// i64s 雪花 ID 转字符串（body/URL 拼接用；与响应的字符串化对称）。
func i64s(v int64) string {
	return strconv.FormatInt(v, 10)
}
