package httpserver

import (
	"github.com/gofiber/fiber/v3"

	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/quota"
)

// ---- 租户配额（admin）----

func (s *Server) listQuotas(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	return writeJSON(ctx, fiber.StatusOK, map[string]any{
		"items": quota.List(s.db, c.TenantID, s.disp.OnlineForTenant(c.TenantID)),
	})
}

func (s *Server) setQuota(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	metric := ctx.Params("metric")
	valid := false
	for _, m := range quota.Metrics {
		if m == metric {
			valid = true
			break
		}
	}
	if !valid {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "unknown metric"))
	}
	var in struct {
		Limit int64 `json:"limit"`
	}
	if !decode(ctx, &in) {
		return nil
	}
	if err := quota.Set(s.db, c.TenantID, metric, in.Limit); err != nil {
		return writeAppErr(ctx, apperr.Internal(err.Error()))
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{
		"metric": metric, "limit": in.Limit, "used": quota.Usage(s.db, c.TenantID, metric),
	})
}
