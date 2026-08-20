package httpserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/auth"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// ---- 租户 API Token（CI/CLI 机器凭证） ----
//
// 设计：
//   - 原始 token 形如 tp_<64 hex>，仅在创建时返回一次；
//   - 数据库只存 SHA-256 哈希（token_hash）；
//   - 认证时按当前 tenant_members.role 动态取角色（成员被移除/禁用立即失效）；
//   - scopes 本期仅存储展示，授权面暂等于该用户的 JWT 权限，后续可做细粒度裁剪；
//   - 表结构由 schema_migrations v2 管理（见 docs/ci-migration-plan.md）。

type apiTokenReq struct {
	Name      string   `json:"name"`
	Scopes    []string `json:"scopes"`
	ExpiresAt string   `json:"expires_at"` // RFC3339；空=永不过期
}

type apiTokenView struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	UserID     string          `json:"user_id"`
	Scopes     json.RawMessage `json:"scopes"`
	ExpiresAt  *time.Time      `json:"expires_at"`
	LastUsedAt *time.Time      `json:"last_used_at"`
}

func (s *Server) listAPITokens(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var rows []model.ApiToken
	if err := s.db.Where("tenant_id = ?", c.TenantID).Order("id desc").Find(&rows).Error; err != nil {
		return writeInternalErr(ctx, err)
	}
	out := make([]apiTokenView, 0, len(rows))
	for _, t := range rows {
		scopes := t.Scopes
		if len(scopes) == 0 {
			scopes = model.JSON(`["*"]`)
		}
		out = append(out, apiTokenView{
			ID:         strconv.FormatInt(t.ID, 10),
			Name:       t.Name,
			UserID:     strconv.FormatInt(t.UserID, 10),
			Scopes:     json.RawMessage(scopes),
			ExpiresAt:  t.ExpiresAt,
			LastUsedAt: t.LastUsedAt,
		})
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"items": out, "total": len(out)})
}

func (s *Server) createAPIToken(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var in apiTokenReq
	if !decode(ctx, &in) {
		return nil
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "name required"))
	}
	raw, hash, err := newAPITokenValue()
	if err != nil {
		return writeInternalErr(ctx, err)
	}

	tok := model.ApiToken{
		ID:        model.NextID(),
		TenantID:  c.TenantID,
		UserID:    c.UserID,
		Name:      in.Name,
		TokenHash: hash,
		Scopes:    model.JSON(`["*"]`),
	}
	if len(in.Scopes) > 0 {
		b, err := json.Marshal(in.Scopes)
		if err != nil {
			return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "scopes invalid"))
		}
		tok.Scopes = b
	}
	if strings.TrimSpace(in.ExpiresAt) != "" {
		t, err := time.Parse(time.RFC3339, in.ExpiresAt)
		if err != nil {
			return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "expires_at must be RFC3339"))
		}
		if !t.After(time.Now()) {
			return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "expires_at must be in the future"))
		}
		tok.ExpiresAt = &t
	}
	if err := s.db.Create(&tok).Error; err != nil {
		return writeInternalErr(ctx, err)
	}
	// 原始 token 只此一次可见。
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"id": strconv.FormatInt(tok.ID, 10), "token": raw})
}

func (s *Server) deleteAPIToken(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	id, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	res := s.db.Where("id = ? AND tenant_id = ?", id, c.TenantID).Delete(&model.ApiToken{})
	if res.Error != nil {
		return writeInternalErr(ctx, res.Error)
	}
	if res.RowsAffected == 0 {
		return writeErr(ctx, fiber.StatusNotFound, "not found")
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"ok": true})
}

// resolveAPIToken 在 JWT 解析失败后尝试按租户 API Token 认证。
func (s *Server) resolveAPIToken(ctx context.Context, raw string) (*auth.Claims, error) {
	if !strings.HasPrefix(raw, "tp_") {
		return nil, errors.New("not an api token")
	}
	var tok model.ApiToken
	if err := s.db.WithContext(ctx).Where("token_hash = ?", hashAPIToken(raw)).First(&tok).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("api token not found")
		}
		return nil, err
	}
	if tok.ExpiresAt != nil && tok.ExpiresAt.Before(time.Now()) {
		return nil, errors.New("api token expired")
	}
	var member model.TenantMember
	if err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND user_id = ?", tok.TenantID, tok.UserID).
		First(&member).Error; err != nil {
		return nil, errors.New("api token owner is no longer a tenant member")
	}
	var user model.User
	if err := s.db.WithContext(ctx).Select("id", "status").First(&user, tok.UserID).Error; err != nil ||
		user.Status != 1 {
		return nil, errors.New("api token owner disabled")
	}
	// last_used_at 尽力更新：失败只影响元数据，不影响认证。
	now := time.Now()
	_ = s.db.WithContext(ctx).Model(&tok).Update("last_used_at", &now).Error
	return &auth.Claims{UserID: tok.UserID, TenantID: tok.TenantID, Role: member.Role}, nil
}

// newAPITokenValue 生成原始 token（tp_ + 64 hex）与其 SHA-256 哈希。
func newAPITokenValue() (raw, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	raw = "tp_" + hex.EncodeToString(b)
	return raw, hashAPIToken(raw), nil
}

func hashAPIToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
