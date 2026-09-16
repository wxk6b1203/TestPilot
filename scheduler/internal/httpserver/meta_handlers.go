package httpserver

import "github.com/gofiber/fiber/v3"

// getMetaLocale 公开声明后端返回消息的固定语言。
// 本项目 i18n 策略：后端（scheduler/worker/copilot 工具面）错误与消息恒为英文，
// 不做运行时多语言；所有本地化展示由前端完成。前端可调用本接口确认后端消息语言，
// 而不必硬编码假设。
func (s *Server) getMetaLocale(ctx fiber.Ctx) error {
	return writeJSON(ctx, fiber.StatusOK, map[string]string{"language": "en-US"})
}
