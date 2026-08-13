package auth

import "github.com/gofiber/fiber/v3"

// 租户角色（数值越小权限越大）。
const (
	RoleOwner  int16 = 1 // 所有者：全部权限
	RoleAdmin  int16 = 2 // 管理员：+ 成员/配额/通知/身份源/审计
	RoleMember int16 = 3 // 成员：领域 CRUD + 触发运行
	RoleViewer int16 = 4 // 只读
)

// RoleName 返回角色展示名。
func RoleName(role int16) string {
	switch role {
	case RoleOwner:
		return "owner"
	case RoleAdmin:
		return "admin"
	case RoleMember:
		return "member"
	case RoleViewer:
		return "viewer"
	}
	return "unknown"
}

// RequireRole 包装 handler：claims.Role 必须 <= min（即权限不低于 min）。
func RequireRole(min int16, next fiber.Handler) fiber.Handler {
	return func(c fiber.Ctx) error {
		claims, ok := FromContext(c.Context())
		if !ok {
			return c.Status(fiber.StatusUnauthorized).
				SendString(`{"error":{"code":"UNAUTHORIZED","message":"missing claims"}}`)
		}
		if claims.Role > min {
			return c.Status(fiber.StatusForbidden).
				SendString(`{"error":{"code":"FORBIDDEN","message":"role ` +
					RoleName(claims.Role) + ` lacks permission (requires ` + RoleName(min) + `)"}}`)
		}
		return next(c)
	}
}
