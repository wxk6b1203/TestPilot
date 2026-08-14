package httpserver

import (
	"github.com/gofiber/fiber/v3"
	"strconv"

	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/audit"
	"github.com/testpilot/testpilot/internal/auth"
	"github.com/testpilot/testpilot/internal/model"
)

// ---- 租户成员管理（admin+）与租户切换 ----

type memberView struct {
	UserID      int64  `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Role        int16  `json:"role"`
	RoleName    string `json:"role_name"`
}

func (s *Server) listMembers(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var ms []model.TenantMember
	s.db.Where("tenant_id = ?", c.TenantID).Order("id asc").Find(&ms)
	out := make([]memberView, 0, len(ms))
	for _, m := range ms {
		var u model.User
		if err := s.db.First(&u, m.UserID).Error; err != nil {
			continue
		}
		out = append(out, memberView{UserID: u.ID, Username: u.Username, DisplayName: u.DisplayName,
			Email: u.Email, Role: m.Role, RoleName: auth.RoleName(m.Role)})
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"items": out, "total": len(out)})
}

type memberReq struct {
	Username string `json:"username"`
	Password string `json:"password"` // 用户不存在时以其创建（可选，默认随机不可用密码外的固定值）
	Email    string `json:"email"`
	Role     int16  `json:"role"`
}

func (s *Server) addMember(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var in memberReq
	if !decode(ctx, &in) {
		return nil
	}
	if in.Username == "" || in.Role < auth.RoleOwner || in.Role > auth.RoleViewer {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "username 必填，role ∈ [1..4]"))
	}
	var u model.User
	err := s.db.Where("username = ?", in.Username).First(&u).Error
	if err != nil {
		// 用户不存在 → 创建（默认密码 changeme123，要求首登修改——v1 简化）
		pw := in.Password
		if pw == "" {
			pw = "changeme123"
		}
		hash, herr := auth.HashPassword(pw)
		if herr != nil {
			return writeAppErr(ctx, apperr.Internal(herr.Error()))
		}
		u = model.User{ID: model.NextID(), Username: in.Username, Email: in.Email,
			PasswordHash: string(hash), DisplayName: in.Username, Status: 1}
		if cerr := s.db.Create(&u).Error; cerr != nil {
			return writeAppErr(ctx, apperr.Internal(cerr.Error()))
		}
	}
	var cnt int64
	s.db.Model(&model.TenantMember{}).Where("tenant_id = ? AND user_id = ?", c.TenantID, u.ID).Count(&cnt)
	if cnt > 0 {
		return writeAppErr(ctx, apperr.Conflict(apperr.CodeAlreadyExists, "user already a member"))
	}
	m := &model.TenantMember{ID: model.NextID(), TenantID: c.TenantID, UserID: u.ID, Role: in.Role}
	if err := s.db.Create(m).Error; err != nil {
		return writeAppErr(ctx, apperr.Internal(err.Error()))
	}
	return writeJSON(ctx, fiber.StatusOK, memberView{UserID: u.ID, Username: u.Username,
		Role: m.Role, RoleName: auth.RoleName(m.Role)})
}

func (s *Server) updateMemberRole(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	uid, err := strconv.ParseInt(ctx.Params("userID"), 10, 64)
	if err != nil {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "invalid user id"))
	}
	var in struct {
		Role int16 `json:"role"`
	}
	if !decode(ctx, &in) {
		return nil
	}
	if in.Role < auth.RoleOwner || in.Role > auth.RoleViewer {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "role ∈ [1..4]"))
	}
	var m model.TenantMember
	if err := s.db.Where("tenant_id = ? AND user_id = ?", c.TenantID, uid).First(&m).Error; err != nil {
		return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "member not found"))
	}
	// 保护最后一名 owner
	if m.Role == auth.RoleOwner && in.Role != auth.RoleOwner && s.ownerCount(c.TenantID) <= 1 {
		return writeAppErr(ctx, apperr.Conflict(apperr.CodeLastOwner, "cannot demote the last owner"))
	}
	s.db.Model(&m).Update("role", in.Role)
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"user_id": uid, "role": in.Role})
}

func (s *Server) removeMember(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	uid, err := strconv.ParseInt(ctx.Params("userID"), 10, 64)
	if err != nil {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "invalid user id"))
	}
	var m model.TenantMember
	if err := s.db.Where("tenant_id = ? AND user_id = ?", c.TenantID, uid).First(&m).Error; err != nil {
		return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "member not found"))
	}
	if m.Role == auth.RoleOwner && s.ownerCount(c.TenantID) <= 1 {
		return writeAppErr(ctx, apperr.Conflict(apperr.CodeLastOwner, "cannot remove the last owner"))
	}
	s.db.Delete(&m)
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"ok": true})
}

func (s *Server) ownerCount(tenantID int64) int64 {
	var cnt int64
	s.db.Model(&model.TenantMember{}).Where("tenant_id = ? AND role = ?", tenantID, auth.RoleOwner).Count(&cnt)
	return cnt
}

// listMyTenants 当前用户所属租户列表（租户切换器数据源）。
func (s *Server) listMyTenants(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var ms []model.TenantMember
	s.db.Where("user_id = ?", c.UserID).Order("id asc").Find(&ms)
	items := make([]map[string]any, 0, len(ms))
	for _, m := range ms {
		var t model.Tenant
		if err := s.db.First(&t, m.TenantID).Error; err != nil {
			continue
		}
		items = append(items, map[string]any{
			"tenant_id":  m.TenantID,
			"name":       t.Name,
			"role":       m.Role,
			"is_current": m.TenantID == c.TenantID,
		})
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"items": items})
}

// createTenant 创建租户并把调用者设为 owner（自助式多租户）。
func (s *Server) createTenant(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var in struct {
		Name string `json:"name"`
	}
	if !decode(ctx, &in) {
		return nil
	}
	if in.Name == "" {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "name 必填"))
	}
	t := &model.Tenant{ID: model.NextID(), Name: in.Name, Status: 1}
	if err := s.db.Create(t).Error; err != nil {
		return writeAppErr(ctx, apperr.Internal(err.Error()))
	}
	m := &model.TenantMember{ID: model.NextID(), TenantID: t.ID, UserID: c.UserID, Role: auth.RoleOwner}
	if err := s.db.Create(m).Error; err != nil {
		return writeAppErr(ctx, apperr.Internal(err.Error()))
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"id": t.ID, "name": t.Name, "role": auth.RoleOwner})
}

// switchTenant 重新签发指向用户另一租户身份的 token。
func (s *Server) switchTenant(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var in struct {
		TenantID int64 `json:"tenant_id"` // normalizeIDs 兼容字符串/数字两种传法
	}
	if !decode(ctx, &in) {
		return nil
	}
	var m model.TenantMember
	if err := s.db.Where("user_id = ? AND tenant_id = ?", c.UserID, in.TenantID).First(&m).Error; err != nil {
		return writeAppErr(ctx, apperr.Forbidden(apperr.CodeNoMembership, "not a member of that tenant"))
	}
	token, err := auth.IssueToken(s.cfg.JWTSecret, c.UserID, m.TenantID, m.Role, s.cfg.JWTExpireHours)
	if err != nil {
		return writeErr(ctx, fiber.StatusInternalServerError, err.Error())
	}
	// 租户切换审计：落在目标租户下（“谁切入了本租户”）
	audit.Log(s.db, m.TenantID, c.UserID, "switch_tenant", "tenant", strconv.FormatInt(m.TenantID, 10),
		map[string]any{"from_tenant": c.TenantID, "role": m.Role})
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"token": token, "tenant_id": m.TenantID, "role": m.Role})
}
