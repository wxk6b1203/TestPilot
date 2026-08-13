package httpserver

import (
	"regexp"

	"github.com/gofiber/fiber/v3"
	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// ---- 租户配置开关（tenant_settings：未来特性开关/租户级配置的落点）----

var settingKeyRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

func (s *Server) listTenantSettings(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	return listOf[model.TenantSetting](s.db, ctx, func(q *gorm.DB) *gorm.DB {
		return q.Where("tenant_id = ?", c.TenantID)
	})
}

func (s *Server) upsertTenantSetting(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	key := ctx.Params("key")
	if !settingKeyRe.MatchString(key) {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam,
			"key must match [A-Za-z0-9_.-]{1,64}"))
	}
	var in struct {
		Value string `json:"value"`
	}
	if !decode(ctx, &in) {
		return nil
	}
	var row model.TenantSetting
	err := s.db.Where("tenant_id = ? AND key = ?", c.TenantID, key).First(&row).Error
	switch {
	case err == nil:
		if row.Value != in.Value {
			row.Value = in.Value
			if uerr := s.db.Save(&row).Error; uerr != nil {
				return writeAppErr(ctx, apperr.Internal(uerr.Error()))
			}
		}
	case err == gorm.ErrRecordNotFound:
		row = model.TenantSetting{ID: model.NextID(), TenantID: c.TenantID, Key: key, Value: in.Value}
		if cerr := s.db.Create(&row).Error; cerr != nil {
			return writeAppErr(ctx, apperr.Internal(cerr.Error()))
		}
	default:
		return writeAppErr(ctx, apperr.Internal(err.Error()))
	}
	return writeJSON(ctx, fiber.StatusOK, &row)
}

func (s *Server) deleteTenantSetting(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	key := ctx.Params("key")
	if !settingKeyRe.MatchString(key) {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam,
			"key must match [A-Za-z0-9_.-]{1,64}"))
	}
	res := s.db.Where("tenant_id = ? AND key = ?", c.TenantID, key).Delete(&model.TenantSetting{})
	if res.Error != nil {
		return writeAppErr(ctx, apperr.Internal(res.Error.Error()))
	}
	if res.RowsAffected == 0 {
		return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "setting not found"))
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"ok": true})
}
