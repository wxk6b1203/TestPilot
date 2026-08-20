package httpserver

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/testpilot/testpilot/internal/auth"
	"github.com/testpilot/testpilot/internal/config"
	"github.com/testpilot/testpilot/internal/model"
)

func TestRenderJUnit(t *testing.T) {
	now := time.Now()
	run := &model.TestRun{ID: 1001, TenantID: 1, PlanID: 7, Status: 3, StartedAt: now, FinishedAt: &now}
	results := []model.TestCaseResult{
		{ID: 11, CaseID: 101, Status: 2, DurationMs: 1200},                         // passed
		{ID: 12, CaseID: 102, Status: 3, DurationMs: 3400, Error: "assert failed"}, // failed
		{ID: 13, CaseID: 103, Status: 4, DurationMs: 0},                            // skipped
		{ID: 14, CaseID: 104, Status: 1, DurationMs: 10},                           // still running -> error
	}
	names := map[int64]string{101: "登录", 102: "下单", 103: "清理"}
	steps := map[int64][]model.TestStepResult{
		12: {
			{StepPath: "steps[0]", Status: 3, Logs: model.JSON(`["want 200 got 500"]`)},
			{StepPath: "steps[1]", Status: 1},
		},
	}
	out := renderJUnit(run, results, names, steps)
	var doc junitTestsuites
	if err := xml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("xml: %v\n%s", err, out)
	}
	if doc.Tests != 4 || doc.Failures != 1 || doc.Errors != 1 || doc.Skipped != 1 {
		t.Fatalf("counts: %+v", doc)
	}
	suite := doc.Suites[0]
	if len(suite.TestCases) != 4 {
		t.Fatalf("testcases=%d", len(suite.TestCases))
	}
	tc := suite.TestCases[1]
	if tc.Name != "下单" || tc.Failure == nil {
		t.Fatalf("failed testcase=%+v", tc)
	}
	if !strings.Contains(tc.Failure.Body, "assert failed") ||
		!strings.Contains(tc.Failure.Body, "steps[0]") {
		t.Fatalf("failure body=%q", tc.Failure.Body)
	}
	if suite.TestCases[2].Skipped == nil {
		t.Fatal("skip marker missing")
	}
	if suite.TestCases[3].Error == nil {
		t.Fatal("running case should be error")
	}
}

func TestRunJUnitEndpoint(t *testing.T) {
	app, d := newTestApp(t, config.Defaults())
	tok, err := auth.IssueToken(config.Defaults().JWTSecret, 1, 1, 1, 1)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	run := model.TestRun{ID: model.NextID(), TenantID: 1, PlanID: 9, Status: 2,
		Summary: model.JSON(`{"total":2,"passed":1,"failed":1}`), StartedAt: now, FinishedAt: &now}
	if err := d.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	tc1 := model.TestCase{ID: model.NextID(), TenantID: 1, ProjectID: 1, Name: "pass case", Type: 1}
	tc2 := model.TestCase{ID: model.NextID(), TenantID: 1, ProjectID: 1, Name: "fail case", Type: 1}
	if err := d.Create(&tc1).Error; err != nil {
		t.Fatal(err)
	}
	if err := d.Create(&tc2).Error; err != nil {
		t.Fatal(err)
	}
	crs := []model.TestCaseResult{
		{ID: model.NextID(), TenantID: 1, RunID: run.ID, CaseID: tc1.ID, Status: 2, DurationMs: 500},
		{ID: model.NextID(), TenantID: 1, RunID: run.ID, CaseID: tc2.ID, Status: 3, DurationMs: 800, Error: "boom"},
	}
	if err := d.Create(&crs).Error; err != nil {
		t.Fatal(err)
	}
	step := model.TestStepResult{ID: model.NextID(), TenantID: 1, CaseResultID: crs[1].ID,
		StepPath: "steps[0]", Status: 3, Logs: model.JSON(`["bad"]`)}
	if err := d.Create(&step).Error; err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/v1/runs/"+fmt.Sprint(run.ID)+"/junit", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/xml") {
		t.Fatalf("content-type=%s", ct)
	}
	if !strings.Contains(string(body), "pass case") || !strings.Contains(string(body), "fail case") {
		t.Fatalf("body=%s", body)
	}
	if !strings.Contains(resp.Header.Get("Content-Disposition"), "testpilot-run-") {
		t.Fatalf("disposition=%s", resp.Header.Get("Content-Disposition"))
	}

	// 租户隔离：租户 2 的 token 看不到
	tok2, _ := auth.IssueToken(config.Defaults().JWTSecret, 1, 2, 1, 1)
	req2 := httptest.NewRequest("GET", "/api/v1/runs/"+fmt.Sprint(run.ID)+"/junit", nil)
	req2.Header.Set("Authorization", "Bearer "+tok2)
	resp2, err := app.Test(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 404 {
		t.Fatalf("tenant isolation status=%d, want 404", resp2.StatusCode)
	}
}
