package httpserver

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/audit"
	"github.com/testpilot/testpilot/internal/auth"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// ---- 认证 ----

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) login(ctx fiber.Ctx) error {
	var in loginReq
	if !decode(ctx, &in) {
		return nil
	}
	var u model.User
	if err := s.db.Where("username = ?", in.Username).First(&u).Error; err != nil {
		return writeAppErr(ctx, apperr.Unauthorized(apperr.CodeInvalidCredentials, "invalid username or password"))
	}
	if !auth.CheckPassword(u.PasswordHash, in.Password) {
		return writeAppErr(ctx, apperr.Unauthorized(apperr.CodeInvalidCredentials, "invalid username or password"))
	}
	var m model.TenantMember
	if err := s.db.Where("user_id = ?", u.ID).Order("id asc").First(&m).Error; err != nil {
		return writeAppErr(ctx, apperr.Forbidden(apperr.CodeNoMembership, "user has no tenant membership"))
	}
	token, err := auth.IssueToken(s.cfg.JWTSecret, u.ID, m.TenantID, m.Role, s.cfg.JWTExpireHours)
	if err != nil {
		return writeErr(ctx, fiber.StatusInternalServerError, err.Error())
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{
		"token":     token,
		"user":      u,
		"tenant_id": m.TenantID,
		"role":      m.Role,
	})
}

// registerReq 公开注册请求：注册即自助建租户（创建者为 owner）。
type registerReq struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	TenantName  string `json:"tenant_name"`
}

// register 公开注册：建用户（bcrypt）+ 建租户 + owner 成员，成功即签发 token。
// 由配置开关 registration_enabled 控制（默认关闭 → 403 REGISTRATION_DISABLED）。
func (s *Server) register(ctx fiber.Ctx) error {
	if !s.cfg.RegistrationEnabled {
		return writeAppErr(ctx, apperr.Forbidden(apperr.CodeRegistrationDisabled,
			"registration is disabled by config (registration_enabled)"))
	}
	var in registerReq
	if !decode(ctx, &in) {
		return nil
	}
	username := strings.TrimSpace(in.Username)
	if len(username) < 3 || len(username) > 64 {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam,
			"username must be 3-64 characters"))
	}
	if len(in.Password) < 8 || len(in.Password) > 128 {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam,
			"password must be 8-128 characters"))
	}
	tenantName := strings.TrimSpace(in.TenantName)
	if tenantName == "" {
		tenantName = username
	}

	var u model.User
	var m model.TenantMember
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var n int64
		if err := tx.Model(&model.User{}).Where("username = ?", username).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return apperr.Conflict(apperr.CodeUsernameTaken, "username already taken")
		}
		hash, err := auth.HashPassword(in.Password)
		if err != nil {
			return err
		}
		u = model.User{
			ID: model.NextID(), Username: username,
			DisplayName: in.DisplayName, PasswordHash: hash, Status: 1,
		}
		if err := tx.Create(&u).Error; err != nil {
			return err
		}
		t := model.Tenant{ID: model.NextID(), Name: tenantName, Status: 1}
		if err := tx.Create(&t).Error; err != nil {
			return err
		}
		m = model.TenantMember{ID: model.NextID(), TenantID: t.ID, UserID: u.ID, Role: auth.RoleOwner}
		if err := tx.Create(&m).Error; err != nil {
			return err
		}
		// 注册不经 auth 中间件，审计手动落库（actor=1 human）
		return tx.Create(&model.AuditLog{
			ID: model.NextID(), TenantID: t.ID,
			Actor: 1, ActorID: strconv.FormatInt(u.ID, 10),
			Action: "register", ResourceType: "user",
			ResourceID: strconv.FormatInt(u.ID, 10),
			Detail:     model.JSON(`{"tenant_name":"` + tenantName + `"}`),
		}).Error
	})
	if err != nil {
		return writeAppErr(ctx, apperr.From(err))
	}
	token, err := auth.IssueToken(s.cfg.JWTSecret, u.ID, m.TenantID, m.Role, s.cfg.JWTExpireHours)
	if err != nil {
		return writeErr(ctx, fiber.StatusInternalServerError, err.Error())
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{
		"token":     token,
		"user":      u,
		"tenant_id": m.TenantID,
		"role":      m.Role,
	})
}

func (s *Server) me(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var u model.User
	if err := s.db.First(&u, c.UserID).Error; err != nil {
		return writeErr(ctx, fiber.StatusNotFound, "user not found")
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{
		"user":      u,
		"tenant_id": c.TenantID,
		"role":      c.Role,
	})
}

// ---- 项目 ----

func (s *Server) listProjects(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	return listOf[model.Project](s.db, ctx, func(q *gorm.DB) *gorm.DB {
		return q.Where("tenant_id = ?", c.TenantID)
	})
}

func (s *Server) createProject(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	return createOf(s.db, ctx, func(v *model.Project) { assignIDs(v, c.TenantID) })
}

func (s *Server) getProject(ctx fiber.Ctx) error { return getOf[model.Project](s.db, ctx) }
func (s *Server) updateProject(ctx fiber.Ctx) error {
	return updateOf[model.Project](s.db, ctx)
}
func (s *Server) deleteProject(ctx fiber.Ctx) error {
	return deleteOf[model.Project](s.db, ctx)
}

// ---- 环境 ----

func (s *Server) listEnvironments(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	pid := queryInt(ctx, "project_id")
	return listOf[model.Environment](s.db, ctx, func(q *gorm.DB) *gorm.DB {
		q = q.Where("tenant_id = ?", c.TenantID)
		if pid != 0 {
			q = q.Where("project_id = ?", pid)
		}
		return q
	})
}

func (s *Server) createEnvironment(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	return createOf(s.db, ctx, func(v *model.Environment) { assignIDs(v, c.TenantID) })
}

func (s *Server) updateEnvironment(ctx fiber.Ctx) error {
	return updateOf[model.Environment](s.db, ctx)
}
func (s *Server) deleteEnvironment(ctx fiber.Ctx) error {
	return deleteOf[model.Environment](s.db, ctx)
}

// ---- 变量 ----

func (s *Server) listVariables(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	pid := queryInt(ctx, "project_id")
	envID := queryInt(ctx, "environment_id")
	_, hasEnvFilter := ctx.Queries()["environment_id"]
	filter := func(q *gorm.DB) *gorm.DB {
		q = q.Where("tenant_id = ?", c.TenantID)
		if pid != 0 {
			q = q.Where("project_id = ?", pid)
		}
		if hasEnvFilter {
			q = q.Where("environment_id = ?", envID)
		}
		return q
	}
	// 敏感变量读取审计（REST 返回明文 value，敏感行被读取需留痕）
	var sensitive int64
	filter(s.db.Model(&model.Variable{})).Where("sensitive = ?", true).Count(&sensitive)
	if sensitive > 0 {
		audit.Log(s.db, c.TenantID, c.UserID, "secret_read", "variables", "",
			map[string]any{"project_id": pid, "environment_id": envID, "sensitive_count": sensitive})
	}
	return listOf[model.Variable](s.db, ctx, filter)
}

func (s *Server) createVariable(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	return createOf(s.db, ctx, func(v *model.Variable) { assignIDs(v, c.TenantID) })
}

func (s *Server) updateVariable(ctx fiber.Ctx) error {
	return updateOf[model.Variable](s.db, ctx)
}
func (s *Server) deleteVariable(ctx fiber.Ctx) error {
	return deleteOf[model.Variable](s.db, ctx)
}

// ---- HTTP 接口 ----

func (s *Server) listAPIs(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	pid := queryInt(ctx, "project_id")
	return listOf[model.HttpApi](s.db, ctx, func(q *gorm.DB) *gorm.DB {
		q = q.Where("tenant_id = ?", c.TenantID)
		if pid != 0 {
			q = q.Where("project_id = ?", pid)
		}
		return q
	})
}

func (s *Server) createAPI(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var in struct {
		model.HttpApi
		ParentID int64 `json:"parent_id"` // 可选：目标目录树节点（folder）id，0=挂根
	}
	if !decode(ctx, &in) {
		return nil
	}
	v := in.HttpApi
	assignIDs(&v, c.TenantID)
	// 指定目录时校验并取父路径；未指定挂根（根即普通目录）
	parentPath := ""
	if in.ParentID != 0 {
		p, err := s.nodePath(c.TenantID, in.ParentID)
		if err != nil {
			return writeAppErr(ctx, apperr.From(err))
		}
		parentPath = p
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&v).Error; err != nil {
			return err
		}
		cnt, err := childCount(tx, c.TenantID, in.ParentID) // 追加到目标目录末尾
		if err != nil {
			return err
		}
		name := v.Name
		if name == "" {
			name = fmt.Sprintf("%s %s", httpMethodName(v.Method), v.URI)
		}
		n := &model.TreeNode{
			ID: model.NextID(), TenantID: c.TenantID, ProjectID: v.ProjectID,
			ParentID: in.ParentID, NodeType: model.NodeTypeHTTPAPI, RefID: v.ID, Name: name,
			Order: cnt,
		}
		n.Path = parentPath + fmt.Sprint(n.ID) + "/"
		return tx.Create(n).Error
	})
	if err != nil {
		return writeAppErr(ctx, apperr.Internal(err.Error()))
	}
	return writeJSON(ctx, fiber.StatusOK, &v)
}

func (s *Server) getAPI(ctx fiber.Ctx) error { return getOf[model.HttpApi](s.db, ctx) }
func (s *Server) updateAPI(ctx fiber.Ctx) error {
	return updateOf[model.HttpApi](s.db, ctx)
}

// deleteAPI 删除接口并级联删除其目录树挂载节点（接口节点无子节点，直接删挂载即可）。
func (s *Server) deleteAPI(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	id, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	var v model.HttpApi
	err := s.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Where("id = ? AND tenant_id = ?", id, c.TenantID).Delete(&v)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return apperr.NotFound(apperr.CodeNotFound, "api not found")
		}
		return tx.Where("tenant_id = ? AND node_type = ? AND ref_id = ?",
			c.TenantID, model.NodeTypeHTTPAPI, id).Delete(&model.TreeNode{}).Error
	})
	if err != nil {
		return writeAppErr(ctx, apperr.From(err))
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"ok": true})
}

// ---- 测试用例 ----

func (s *Server) listCases(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	pid := queryInt(ctx, "project_id")
	return listOf[model.TestCase](s.db, ctx, func(q *gorm.DB) *gorm.DB {
		q = q.Where("tenant_id = ?", c.TenantID)
		if pid != 0 {
			q = q.Where("project_id = ?", pid)
		}
		return q
	})
}

func (s *Server) createCase(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	return createOf(s.db, ctx, func(v *model.TestCase) {
		assignIDs(v, c.TenantID)
		if v.Type == 0 {
			v.Type = int16(commonv1.TestCaseType_TEST_CASE_TYPE_DECLARATIVE)
		}
	})
}

func (s *Server) getCase(ctx fiber.Ctx) error { return getOf[model.TestCase](s.db, ctx) }
func (s *Server) updateCase(ctx fiber.Ctx) error {
	return updateOf[model.TestCase](s.db, ctx)
}
func (s *Server) deleteCase(ctx fiber.Ctx) error {
	return deleteOf[model.TestCase](s.db, ctx)
}

// ---- 测试计划（含 items） ----

type planPayload struct {
	model.TestPlan
	Items []model.TestPlanItem `json:"items"`
}

func (s *Server) listPlans(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	pid := queryInt(ctx, "project_id")
	return listOf[model.TestPlan](s.db, ctx, func(q *gorm.DB) *gorm.DB {
		q = q.Where("tenant_id = ?", c.TenantID)
		if pid != 0 {
			q = q.Where("project_id = ?", pid)
		}
		return q
	})
}

func (s *Server) createPlan(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var in planPayload
	if !decode(ctx, &in) {
		return nil
	}
	assignIDs(&in.TestPlan, c.TenantID)
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&in.TestPlan).Error; err != nil {
			return err
		}
		for i := range in.Items {
			in.Items[i].ID = model.NextID()
			in.Items[i].TenantID = c.TenantID
			in.Items[i].PlanID = in.TestPlan.ID
			if err := tx.Create(&in.Items[i]).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return writeErr(ctx, fiber.StatusInternalServerError, err.Error())
	}
	return writeJSON(ctx, fiber.StatusOK, &in)
}

func (s *Server) getPlan(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	id, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	var p model.TestPlan
	if err := s.db.Where("id = ? AND tenant_id = ?", id, c.TenantID).First(&p).Error; err != nil {
		return writeErr(ctx, fiber.StatusNotFound, "not found")
	}
	items := make([]model.TestPlanItem, 0)
	s.db.Where("plan_id = ? AND tenant_id = ?", id, c.TenantID).Order("\"order\" asc").Find(&items)
	return writeJSON(ctx, fiber.StatusOK, &planPayload{TestPlan: p, Items: items})
}

func (s *Server) updatePlan(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	id, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	var p model.TestPlan
	if err := s.db.Where("id = ? AND tenant_id = ?", id, c.TenantID).First(&p).Error; err != nil {
		return writeErr(ctx, fiber.StatusNotFound, "not found")
	}
	var in planPayload
	if !decode(ctx, &in) {
		return nil
	}
	in.TestPlan.ID = p.ID
	in.TestPlan.TenantID = c.TenantID
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&in.TestPlan).Error; err != nil {
			return err
		}
		if err := tx.Where("plan_id = ? AND tenant_id = ?", p.ID, c.TenantID).
			Delete(&model.TestPlanItem{}).Error; err != nil {
			return err
		}
		for i := range in.Items {
			in.Items[i].ID = model.NextID()
			in.Items[i].TenantID = c.TenantID
			in.Items[i].PlanID = p.ID
			if err := tx.Create(&in.Items[i]).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return writeErr(ctx, fiber.StatusInternalServerError, err.Error())
	}
	return writeJSON(ctx, fiber.StatusOK, &in)
}

func (s *Server) deletePlan(ctx fiber.Ctx) error {
	return deleteOf[model.TestPlan](s.db, ctx)
}

// ---- 运行 ----

type runReq struct {
	EnvID int64 `json:"env_id"`
}

func (s *Server) runPlan(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	planID, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	var in runReq
	if len(ctx.Body()) > 0 {
		if !decode(ctx, &in) {
			return nil
		}
	}
	runID, err := s.run.Trigger(ctx.Context(), c.TenantID, planID, in.EnvID,
		int16(commonv1.TriggerType_TRIGGER_TYPE_MANUAL), fmt.Sprint(c.UserID))
	if err != nil {
		return writeAppErr(ctx, err)
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"run_id": runID})
}

func (s *Server) listRuns(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	planID := queryInt(ctx, "plan_id")
	return listOf[model.TestRun](s.db, ctx, func(q *gorm.DB) *gorm.DB {
		q = q.Where("tenant_id = ?", c.TenantID)
		if planID != 0 {
			q = q.Where("plan_id = ?", planID)
		} else {
			q = q.Where("plan_id <> 0") // 调试 run（plan_id=0）不进列表，仍可按 id 查看
		}
		return q
	})
}

type stepView struct {
	model.TestStepResult
	Artifacts []model.Artifact `json:"artifacts,omitempty"`
}

type caseView struct {
	model.TestCaseResult
	CaseName string     `json:"case_name"`
	Steps    []stepView `json:"steps"`
}

type runView struct {
	model.TestRun
	Cases []caseView `json:"cases"`
}

func (s *Server) getRun(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	id, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	var run model.TestRun
	if err := s.db.Where("id = ? AND tenant_id = ?", id, c.TenantID).First(&run).Error; err != nil {
		return writeErr(ctx, fiber.StatusNotFound, "not found")
	}
	results := make([]model.TestCaseResult, 0)
	s.db.Where("run_id = ? AND tenant_id = ?", id, c.TenantID).Order("id asc").Find(&results)

	caseIDs := make([]int64, 0, len(results))
	for _, cr := range results {
		caseIDs = append(caseIDs, cr.CaseID)
	}
	names := map[int64]string{}
	if len(caseIDs) > 0 {
		var cases []model.TestCase
		s.db.Select("id", "name").Where("id IN ?", caseIDs).Find(&cases)
		for _, tc := range cases {
			names[tc.ID] = tc.Name
		}
	}

	// 本 run 的全部产物，按 step_result_id 分组
	arts := make([]model.Artifact, 0)
	s.db.Where("run_id = ? AND tenant_id = ?", id, c.TenantID).Find(&arts)
	artsByStep := map[int64][]model.Artifact{}
	for _, a := range arts {
		artsByStep[a.StepResultID] = append(artsByStep[a.StepResultID], a)
	}

	view := runView{TestRun: run, Cases: make([]caseView, 0, len(results))}
	for _, cr := range results {
		steps := make([]model.TestStepResult, 0)
		s.db.Where("case_result_id = ? AND tenant_id = ?", cr.ID, c.TenantID).Order("id asc").Find(&steps)
		sv := make([]stepView, 0, len(steps))
		for _, st := range steps {
			sv = append(sv, stepView{TestStepResult: st, Artifacts: artsByStep[st.ID]})
		}
		view.Cases = append(view.Cases, caseView{
			TestCaseResult: cr,
			CaseName:       names[cr.CaseID],
			Steps:          sv,
		})
	}
	return writeJSON(ctx, fiber.StatusOK, &view)
}

// ---- Worker 在线状态 ----

type workerView struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Capabilities   []int32  `json:"capabilities"`
	TenantID       int64    `json:"tenant_id"`
	Load           int32    `json:"load"`
	MaxConcurrency int32    `json:"max_concurrency"`
	Tags           []string `json:"tags"`
	SDKVersion     string   `json:"sdk_version"`
}

func (s *Server) listWorkers(ctx fiber.Ctx) error {
	ws := s.disp.Workers()
	out := make([]workerView, 0, len(ws))
	for _, x := range ws {
		out = append(out, workerView{
			ID:             x.ID,
			Name:           x.Name,
			Capabilities:   x.Capabilities,
			TenantID:       x.TenantID,
			Load:           x.Load(),
			MaxConcurrency: x.MaxConcurrency,
			Tags:           x.Tags,
			SDKVersion:     x.SDKVersion,
		})
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"items": out, "total": len(out)})
}
