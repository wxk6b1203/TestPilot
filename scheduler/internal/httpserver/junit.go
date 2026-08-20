package httpserver

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	"github.com/testpilot/testpilot/internal/model"
)

// ---- JUnit XML 导出（CI 集成） ----
//
// 设计见 docs/ci-migration-plan.md §JUnit：
//   - 一个 run = 一个 <testsuite>，每个 case_result = 一个 <testcase>；
//   - PASSED 无子节点、FAILED <failure>、SKIPPED <skipped>、RUNNING/未知 <error>；
//   - 失败详情包含 case_result.error 与失败步骤 path/logs 摘要。

type junitTestsuites struct {
	XMLName  xml.Name         `xml:"testsuites"`
	Tests    int              `xml:"tests,attr"`
	Failures int              `xml:"failures,attr"`
	Errors   int              `xml:"errors,attr"`
	Skipped  int              `xml:"skipped,attr"`
	Time     string           `xml:"time,attr"`
	Suites   []junitTestsuite `xml:"testsuite"`
}

type junitTestsuite struct {
	Name      string          `xml:"name,attr"`
	Tests     int             `xml:"tests,attr"`
	Failures  int             `xml:"failures,attr"`
	Errors    int             `xml:"errors,attr"`
	Skipped   int             `xml:"skipped,attr"`
	Time      string          `xml:"time,attr"`
	TestCases []junitTestcase `xml:"testcase"`
}

type junitTestcase struct {
	Name      string       `xml:"name,attr"`
	ClassName string       `xml:"classname,attr"`
	Time      string       `xml:"time,attr"`
	Failure   *junitDetail `xml:"failure,omitempty"`
	Error     *junitDetail `xml:"error,omitempty"`
	Skipped   *junitDetail `xml:"skipped,omitempty"`
}

type junitDetail struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}

// renderJUnit 把一次功能运行渲染为 JUnit XML（纯函数，便于单测）。
func renderJUnit(run *model.TestRun, results []model.TestCaseResult,
	names map[int64]string, stepsByCase map[int64][]model.TestStepResult) []byte {
	suite := junitTestsuite{
		Name:      fmt.Sprintf("testpilot run %d (plan %d)", run.ID, run.PlanID),
		Tests:     len(results),
		TestCases: make([]junitTestcase, 0, len(results)),
	}
	for _, cr := range results {
		tc := junitTestcase{
			Name:      names[cr.CaseID],
			ClassName: "TestPilot.case." + strconv.FormatInt(cr.CaseID, 10),
			Time:      formatJUnitSeconds(cr.DurationMs),
		}
		if tc.Name == "" {
			tc.Name = "case " + strconv.FormatInt(cr.CaseID, 10)
		}
		switch commonv1.CaseStatus(cr.Status) {
		case commonv1.CaseStatus_CASE_STATUS_PASSED:
		case commonv1.CaseStatus_CASE_STATUS_FAILED:
			suite.Failures++
			tc.Failure = &junitDetail{
				Message: "case failed",
				Body:    caseFailureDetail(cr, stepsByCase[cr.ID]),
			}
		case commonv1.CaseStatus_CASE_STATUS_SKIPPED:
			suite.Skipped++
			tc.Skipped = &junitDetail{Message: "skipped"}
		default: // RUNNING / 未终态：CI 场景视为错误而非静默通过
			suite.Errors++
			tc.Error = &junitDetail{
				Message: "case did not finish",
				Body:    fmt.Sprintf("status=%d error=%s", cr.Status, cr.Error),
			}
		}
		suite.TestCases = append(suite.TestCases, tc)
	}

	totalTime := 0.0
	for _, cr := range results {
		totalTime += float64(cr.DurationMs) / 1000
	}
	if totalTime == 0 && run.StartedAt.Unix() > 0 && run.FinishedAt != nil {
		totalTime = run.FinishedAt.Sub(run.StartedAt).Seconds()
	}
	out := junitTestsuites{
		Tests:    suite.Tests,
		Failures: suite.Failures,
		Errors:   suite.Errors,
		Skipped:  suite.Skipped,
		Time:     formatJUnitSeconds(int(totalTime * 1000)),
		Suites:   []junitTestsuite{suite},
	}
	suite.Time = out.Time
	out.Suites[0] = suite

	buf := bytes.NewBuffer(nil)
	enc := xml.NewEncoder(buf)
	enc.Indent("", "  ")
	if err := enc.Encode(out); err != nil {
		// 输入均来自受控字段，失败只会是结构定义错误；返回可诊断内容。
		return []byte(fmt.Sprintf("<error>encode junit: %v</error>", err))
	}
	return append(buf.Bytes(), '\n')
}

func caseFailureDetail(cr model.TestCaseResult, steps []model.TestStepResult) string {
	var b strings.Builder
	if cr.Error != "" {
		b.WriteString(cr.Error)
		b.WriteString("\n")
	}
	failedSteps := 0
	for _, st := range steps {
		if commonv1.StepStatus(st.Status) != commonv1.StepStatus_STEP_STATUS_FAILED {
			continue
		}
		failedSteps++
		if b.Len() == 0 {
			b.WriteString("failed steps:\n")
		}
		b.WriteString("- ")
		b.WriteString(st.StepPath)
		if len(st.Logs) > 0 && string(st.Logs) != "null" {
			b.WriteString(": ")
			b.WriteString(compactLogs(st.Logs))
		}
		b.WriteString("\n")
	}
	if b.Len() == 0 {
		b.WriteString("no failure detail")
	}
	return strings.TrimSpace(b.String())
}

func compactLogs(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	if len(s) > 500 {
		s = s[:500] + "..."
	}
	return strings.ReplaceAll(s, "\n", " ")
}

func formatJUnitSeconds(ms int) string {
	return strconv.FormatFloat(float64(ms)/1000, 'f', 3, 64)
}

// runJUnit GET /runs/:id/junit —— 导出 JUnit XML 报告（租户隔离，viewer 可读）。
func (s *Server) runJUnit(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	runID, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	var run model.TestRun
	if err := s.db.Where("id = ? AND tenant_id = ?", runID, c.TenantID).First(&run).Error; err != nil {
		return writeErr(ctx, fiber.StatusNotFound, "not found")
	}

	results := make([]model.TestCaseResult, 0)
	s.db.Where("run_id = ? AND tenant_id = ?", runID, c.TenantID).Order("id asc").Find(&results)

	caseIDs := make([]int64, 0, len(results))
	crIDs := make([]int64, 0, len(results))
	for _, cr := range results {
		caseIDs = append(caseIDs, cr.CaseID)
		crIDs = append(crIDs, cr.ID)
	}
	names := map[int64]string{}
	if len(caseIDs) > 0 {
		var cases []model.TestCase
		s.db.Select("id", "name").Where("id IN ?", caseIDs).Find(&cases)
		for _, tc := range cases {
			names[tc.ID] = tc.Name
		}
	}
	stepsByCase := map[int64][]model.TestStepResult{}
	if len(crIDs) > 0 {
		var steps []model.TestStepResult
		s.db.Where("case_result_id IN ? AND tenant_id = ?", crIDs, c.TenantID).
			Order("id asc").Find(&steps)
		for _, st := range steps {
			stepsByCase[st.CaseResultID] = append(stepsByCase[st.CaseResultID], st)
		}
	}

	xmlBytes := renderJUnit(&run, results, names, stepsByCase)
	ctx.Set(fiber.HeaderContentType, "application/xml; charset=utf-8")
	ctx.Set(fiber.HeaderContentDisposition,
		fmt.Sprintf(`attachment; filename="testpilot-run-%d.xml"`, run.ID))
	ctx.Set(fiber.HeaderCacheControl, "no-store")
	return ctx.Send(xmlBytes)
}
