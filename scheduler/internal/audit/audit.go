// Package audit 人工操作审计（design 14：人工变更 / 租户切换 / 敏感读取；
// Copilot 写操作审计在 grpcserver 侧，actor=2）。
//
// 中间件只记成功的变更（非 GET 且响应 <300）；查询类请求不记（敏感变量
// 读取由 listVariables 显式记 secret_read）。审计写入失败仅记日志，不阻塞业务。
package audit

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/testpilot/testpilot/internal/auth"
	"github.com/testpilot/testpilot/internal/logging"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// Log 显式落一行人工审计（actor=1）。
func Log(db *gorm.DB, tenantID, userID int64, action, resType, resID string, detail any) {
	dj, _ := json.Marshal(detail)
	row := &model.AuditLog{
		ID:           model.NextID(),
		TenantID:     tenantID,
		Actor:        1, // human
		ActorID:      strconv.FormatInt(userID, 10),
		Action:       action,
		ResourceType: resType,
		ResourceID:   resID,
		Detail:       model.JSON(dj),
	}
	if err := db.Create(row).Error; err != nil {
		logging.L.Warnw("audit write failed", "action", action, "err", err)
	}
}

// Middleware 审计人工变更：/api/v1 下非 GET 且成功（<300）的请求。
// 须置于 auth.Middleware 之内（依赖 Claims）；/auth/ 公开路径跳过。
// 组中间件与终端 handler 同处一条 handler 链，c.Params 在 Next 前即可用。
func Middleware(db *gorm.DB) fiber.Handler {
	return func(c fiber.Ctx) error {
		if c.Method() == fiber.MethodGet || strings.HasPrefix(c.Path(), "/api/v1/auth/") {
			return c.Next()
		}
		err := c.Next()
		if c.Response().StatusCode() >= fiber.StatusMultipleChoices {
			return err
		}
		claims, _ := auth.FromContext(c.Context())
		if claims == nil {
			return err
		}
		Log(db, claims.TenantID, claims.UserID, actionOf(c.Method(), c.Path()), resourceType(c.Path()), resourceID(c),
			map[string]any{"method": c.Method(), "path": c.Path()})
		return err
	}
}

// actionOf 由方法/路径推导动作：POST .../run → run；POST→create；PUT/PATCH→update；DELETE→delete。
func actionOf(method, path string) string {
	switch method {
	case fiber.MethodPost:
		if strings.HasSuffix(path, "/run") {
			return "run"
		}
		return "create"
	case fiber.MethodPut, fiber.MethodPatch:
		return "update"
	case fiber.MethodDelete:
		return "delete"
	default:
		return strings.ToLower(method)
	}
}

// resourceType 取路径首段（/api/v1/<seg>）；tenant/* 与 copilot/* 保留两段以区分治理对象。
func resourceType(path string) string {
	segs := strings.Split(strings.TrimPrefix(path, "/api/v1/"), "/")
	if len(segs) == 0 {
		return ""
	}
	if (segs[0] == "tenant" || segs[0] == "copilot") && len(segs) > 1 {
		return segs[0] + "/" + segs[1]
	}
	return segs[0]
}

// resourceID 取常见路径参数（:id/:userID/:metric），均无则空。
func resourceID(c fiber.Ctx) string {
	for _, k := range []string{"id", "userID", "metric"} {
		if v := c.Params(k); v != "" {
			return v
		}
	}
	return ""
}
