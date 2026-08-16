package httpserver

import (
	"errors"
	"net"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/static"
	"github.com/testpilot/testpilot/internal/artifactstore"
	"github.com/testpilot/testpilot/internal/audit"
	"github.com/testpilot/testpilot/internal/auth"
	"github.com/testpilot/testpilot/internal/config"
	"github.com/testpilot/testpilot/internal/cronsched"
	"github.com/testpilot/testpilot/internal/dispatch"
	"github.com/testpilot/testpilot/internal/metrics"
	"github.com/testpilot/testpilot/internal/runner"
	"github.com/testpilot/testpilot/internal/tracing"
	"gorm.io/gorm"
)

// Server 聚合 REST 层依赖。
type Server struct {
	db    *gorm.DB
	cfg   config.Config
	disp  *dispatch.Dispatcher
	run   *runner.Runner
	cron  *cronsched.Scheduler
	store artifactstore.Backend
}

func New(db *gorm.DB, cfg config.Config, disp *dispatch.Dispatcher, run *runner.Runner,
	cron *cronsched.Scheduler, store artifactstore.Backend) *Server {
	return &Server{db: db, cfg: cfg, disp: disp, run: run, cron: cron, store: store}
}

// artifactStore 取产物后端；未注入（测试）时退回本地目录。
func (s *Server) artifactStore() artifactstore.Backend {
	if s.store != nil {
		return s.store
	}
	local, err := artifactstore.NewLocal(s.cfg.ArtifactDir)
	if err != nil {
		return nil
	}
	return local
}

// App 构建 fiber 应用：/api/v1 下除 login/OIDC 外全部走 JWT 认证 + RBAC 最小角色。
// 角色约定：viewer=GET 只读；member=领域 CRUD/触发；admin=成员/配额/通知/身份源/审计；owner=全部。
//
// 中间件链：recover → tracing(span) → metrics → CORS → [auth → audit → RequireRole] → handler。
// 未匹配路径不再经过 auth（net/http 时期经 /api/v1/ 子树兜底会 401 后再 404，此处直接 404）。
func (s *Server) App() *fiber.App {
	app := fiber.New(fiber.Config{
		CaseSensitive: true, // 对齐 net/http 大小写敏感语义
		BodyLimit:     s.cfg.BodyLimitMB << 20,
		ReadTimeout:   time.Duration(s.cfg.ReadTimeoutSec) * time.Second,
		WriteTimeout:  time.Duration(s.cfg.WriteTimeoutSec) * time.Second,
		IdleTimeout:   time.Duration(s.cfg.IdleTimeoutSec) * time.Second,
		ErrorHandler:  errorHandler,
	})
	app.Use(recover.New())
	app.Use(tracing.Middleware())
	app.Use(metrics.HTTPMiddleware)
	app.Use(corsDev)

	// 公开端点
	app.Get("/healthz", func(c fiber.Ctx) error {
		return writeJSON(c, fiber.StatusOK, map[string]any{"ok": true})
	})
	// 登录/注册同享登录限流（防暴力破解与批量注册：同 IP 10 次/分钟）。
	// 回环来源（本地开发/e2e/同机工具）不限流——攻击者无法伪造 TCP 源 IP，
	// 限流只对真实远程来源生效。
	app.Post("/api/v1/auth/login", func(c fiber.Ctx) error {
		if !isLoopbackAddr(c) && !loginLimit.Allow(c.IP()) {
			return writeErr(c, fiber.StatusTooManyRequests, "too many login attempts, try again later")
		}
		return s.login(c)
	})
	app.Post("/api/v1/auth/register", func(c fiber.Ctx) error {
		if !isLoopbackAddr(c) && !loginLimit.Allow(c.IP()) {
			return writeErr(c, fiber.StatusTooManyRequests, "too many attempts, try again later")
		}
		return s.register(c)
	})
	// OIDC 登录链路（公开）
	app.Get("/api/v1/auth/oidc/providers", s.listOIDCProvidersPublic)
	app.Get("/api/v1/auth/oidc/:id/login", s.oidcLogin)
	app.Get("/api/v1/auth/oidc/:id/callback", s.oidcCallback)
	// Prometheus 抓取：来源白名单由 metrics_allowed_cidrs 控制（空=不限制，本地默认）；
	// 生产配置后非白名单来源一律 403（也可在反向代理层做同等限制）
	app.Get("/metrics", s.metricsGuard(), adaptor.HTTPHandler(metrics.Handler()))

	// 受保护 API：组中间件 = JWT 认证 + 人工变更审计
	api := app.Group("/api/v1", auth.Middleware(s.cfg.JWTSecret), audit.Middleware(s.db))
	h := func(method, path string, min int16, fn fiber.Handler) {
		api.Add([]string{method}, path, auth.RequireRole(min, fn))
	}

	// 认证信息
	h(fiber.MethodGet, "/me", auth.RoleViewer, s.me)

	// 领域 CRUD
	h(fiber.MethodGet, "/projects", auth.RoleViewer, s.listProjects)
	h(fiber.MethodPost, "/projects", auth.RoleMember, s.createProject)
	h(fiber.MethodGet, "/projects/:id", auth.RoleViewer, s.getProject)
	h(fiber.MethodPut, "/projects/:id", auth.RoleMember, s.updateProject)
	h(fiber.MethodDelete, "/projects/:id", auth.RoleMember, s.deleteProject)

	h(fiber.MethodGet, "/environments", auth.RoleViewer, s.listEnvironments)
	h(fiber.MethodPost, "/environments", auth.RoleMember, s.createEnvironment)
	h(fiber.MethodPut, "/environments/:id", auth.RoleMember, s.updateEnvironment)
	h(fiber.MethodDelete, "/environments/:id", auth.RoleMember, s.deleteEnvironment)

	h(fiber.MethodGet, "/variables", auth.RoleViewer, s.listVariables)
	h(fiber.MethodPost, "/variables", auth.RoleMember, s.createVariable)
	h(fiber.MethodPut, "/variables/:id", auth.RoleMember, s.updateVariable)
	h(fiber.MethodDelete, "/variables/:id", auth.RoleMember, s.deleteVariable)

	h(fiber.MethodGet, "/apis", auth.RoleViewer, s.listAPIs)
	h(fiber.MethodPost, "/apis", auth.RoleMember, s.createAPI)
	h(fiber.MethodPost, "/apis/debug", auth.RoleMember, s.debugAPI)
	h(fiber.MethodGet, "/apis/:id", auth.RoleViewer, s.getAPI)
	h(fiber.MethodPut, "/apis/:id", auth.RoleMember, s.updateAPI)
	h(fiber.MethodDelete, "/apis/:id", auth.RoleMember, s.deleteAPI)

	h(fiber.MethodGet, "/grpc-apis", auth.RoleViewer, s.listGrpcAPIs)
	h(fiber.MethodPost, "/grpc-apis", auth.RoleMember, s.createGrpcAPI)
	h(fiber.MethodGet, "/grpc-apis/:id", auth.RoleViewer, s.getGrpcAPI)
	h(fiber.MethodPut, "/grpc-apis/:id", auth.RoleMember, s.updateGrpcAPI)
	h(fiber.MethodDelete, "/grpc-apis/:id", auth.RoleMember, s.deleteGrpcAPI)

	h(fiber.MethodGet, "/proto-files", auth.RoleViewer, s.listProtoFiles)
	h(fiber.MethodPost, "/proto-files", auth.RoleMember, s.createProtoFile)
	h(fiber.MethodGet, "/proto-files/:id", auth.RoleViewer, s.getProtoFile)
	h(fiber.MethodPut, "/proto-files/:id", auth.RoleMember, s.updateProtoFile)
	h(fiber.MethodDelete, "/proto-files/:id", auth.RoleMember, s.deleteProtoFile)

	h(fiber.MethodGet, "/cases", auth.RoleViewer, s.listCases)
	h(fiber.MethodPost, "/cases", auth.RoleMember, s.createCase)
	h(fiber.MethodGet, "/cases/:id", auth.RoleViewer, s.getCase)
	h(fiber.MethodPut, "/cases/:id", auth.RoleMember, s.updateCase)
	h(fiber.MethodDelete, "/cases/:id", auth.RoleMember, s.deleteCase)

	h(fiber.MethodGet, "/plans", auth.RoleViewer, s.listPlans)
	h(fiber.MethodPost, "/plans", auth.RoleMember, s.createPlan)
	h(fiber.MethodGet, "/plans/:id", auth.RoleViewer, s.getPlan)
	h(fiber.MethodPut, "/plans/:id", auth.RoleMember, s.updatePlan)
	h(fiber.MethodDelete, "/plans/:id", auth.RoleMember, s.deletePlan)

	h(fiber.MethodGet, "/tree", auth.RoleViewer, s.getProjectTree)
	h(fiber.MethodPost, "/tree/folders", auth.RoleMember, s.createFolder)
	h(fiber.MethodPut, "/tree/folders/:id", auth.RoleMember, s.renameFolder)
	h(fiber.MethodDelete, "/tree/folders/:id", auth.RoleMember, s.deleteFolder)
	h(fiber.MethodPost, "/tree/nodes", auth.RoleMember, s.mountAPI)
	h(fiber.MethodPut, "/tree/nodes/:id/move", auth.RoleMember, s.moveNode)
	h(fiber.MethodPut, "/tree/reorder", auth.RoleMember, s.reorderTree)
	h(fiber.MethodDelete, "/tree/nodes/:id", auth.RoleMember, s.unmountAPI)

	h(fiber.MethodGet, "/suites", auth.RoleViewer, s.listSuites)
	h(fiber.MethodPost, "/suites", auth.RoleMember, s.createSuite)
	h(fiber.MethodGet, "/suites/:id", auth.RoleViewer, s.getSuite)
	h(fiber.MethodPut, "/suites/:id", auth.RoleMember, s.updateSuite)
	h(fiber.MethodDelete, "/suites/:id", auth.RoleMember, s.deleteSuite)

	h(fiber.MethodGet, "/scripts", auth.RoleViewer, s.listScripts)
	h(fiber.MethodPost, "/scripts", auth.RoleMember, s.createScript)
	h(fiber.MethodGet, "/scripts/:id", auth.RoleViewer, s.getScript)
	h(fiber.MethodPut, "/scripts/:id", auth.RoleMember, s.updateScript)
	h(fiber.MethodDelete, "/scripts/:id", auth.RoleMember, s.deleteScript)

	// 导入导出
	h(fiber.MethodPost, "/import/openapi", auth.RoleMember, s.importOpenAPI)
	h(fiber.MethodPost, "/import/curl", auth.RoleMember, s.importCurl)
	h(fiber.MethodGet, "/export/openapi", auth.RoleViewer, s.exportOpenAPI)
	h(fiber.MethodGet, "/export/curl", auth.RoleViewer, s.exportCurl)
	h(fiber.MethodPost, "/import/postman", auth.RoleMember, s.importPostman)
	h(fiber.MethodGet, "/export/postman", auth.RoleViewer, s.exportPostman)

	// 运行
	h(fiber.MethodPost, "/plans/:id/run", auth.RoleMember, s.runPlan)
	h(fiber.MethodGet, "/runs", auth.RoleViewer, s.listRuns)
	h(fiber.MethodGet, "/runs/:id", auth.RoleViewer, s.getRun)
	h(fiber.MethodGet, "/artifacts/:id/content", auth.RoleViewer, s.getArtifactContent)

	h(fiber.MethodGet, "/stress-plans", auth.RoleViewer, s.listStressPlans)
	h(fiber.MethodPost, "/stress-plans", auth.RoleMember, s.createStressPlan)
	h(fiber.MethodGet, "/stress-plans/:id", auth.RoleViewer, s.getStressPlan)
	h(fiber.MethodPut, "/stress-plans/:id", auth.RoleMember, s.updateStressPlan)
	h(fiber.MethodDelete, "/stress-plans/:id", auth.RoleMember, s.deleteStressPlan)
	h(fiber.MethodPost, "/stress-plans/:id/run", auth.RoleMember, s.runStressPlan)
	h(fiber.MethodGet, "/stress-runs", auth.RoleViewer, s.listStressRuns)
	h(fiber.MethodGet, "/stress-runs/:id", auth.RoleViewer, s.getStressRun)

	// Copilot 会话：写消息/建会话视为成员动作（消耗 ai_calls 配额）；读历史 viewer 即可
	h(fiber.MethodGet, "/copilot/sessions", auth.RoleViewer, s.listCopilotSessions)
	h(fiber.MethodPost, "/copilot/sessions", auth.RoleMember, s.createCopilotSession)
	h(fiber.MethodGet, "/copilot/sessions/:id/messages", auth.RoleViewer, s.listCopilotMessages)
	h(fiber.MethodPost, "/copilot/sessions/:id/messages", auth.RoleMember, s.appendCopilotMessage)
	h(fiber.MethodDelete, "/copilot/sessions/:id", auth.RoleMember, s.deleteCopilotSession)
	// 回收站：读列表 viewer 即可；彻底删除属成员动作
	h(fiber.MethodGet, "/copilot/trash", auth.RoleViewer, s.listCopilotTrash)
	h(fiber.MethodDelete, "/copilot/trash/:id", auth.RoleMember, s.purgeCopilotTrash)

	// 租户治理（admin）：成员 / 审计 / 配额 / 通知 / 定时 / 身份源
	h(fiber.MethodGet, "/audit-logs", auth.RoleAdmin, s.listAuditLogs)
	h(fiber.MethodGet, "/tenant/members", auth.RoleAdmin, s.listMembers)
	h(fiber.MethodPost, "/tenant/members", auth.RoleAdmin, s.addMember)
	h(fiber.MethodPut, "/tenant/members/:userID", auth.RoleAdmin, s.updateMemberRole)
	h(fiber.MethodDelete, "/tenant/members/:userID", auth.RoleAdmin, s.removeMember)
	h(fiber.MethodPost, "/auth/switch-tenant", auth.RoleViewer, s.switchTenant)
	h(fiber.MethodGet, "/tenants", auth.RoleViewer, s.listMyTenants)
	h(fiber.MethodPost, "/tenants", auth.RoleViewer, s.createTenant)
	h(fiber.MethodGet, "/tenant/quotas", auth.RoleAdmin, s.listQuotas)
	h(fiber.MethodGet, "/tenant/settings", auth.RoleAdmin, s.listTenantSettings)
	h(fiber.MethodPut, "/tenant/settings/:key", auth.RoleAdmin, s.upsertTenantSetting)
	h(fiber.MethodDelete, "/tenant/settings/:key", auth.RoleAdmin, s.deleteTenantSetting)
	h(fiber.MethodGet, "/schedules", auth.RoleViewer, s.listSchedules)
	h(fiber.MethodPost, "/schedules", auth.RoleMember, s.createSchedule)
	h(fiber.MethodPut, "/schedules/:id", auth.RoleMember, s.updateSchedule)
	h(fiber.MethodDelete, "/schedules/:id", auth.RoleMember, s.deleteSchedule)
	h(fiber.MethodPut, "/tenant/quotas/:metric", auth.RoleAdmin, s.setQuota)
	h(fiber.MethodGet, "/identity-providers", auth.RoleAdmin, s.listIdentityProviders)
	h(fiber.MethodPost, "/identity-providers", auth.RoleAdmin, s.createIdentityProvider)
	h(fiber.MethodPut, "/identity-providers/:id", auth.RoleAdmin, s.updateIdentityProvider)
	h(fiber.MethodDelete, "/identity-providers/:id", auth.RoleAdmin, s.deleteIdentityProvider)
	h(fiber.MethodGet, "/notifications", auth.RoleAdmin, s.listNotificationChannels)
	h(fiber.MethodPost, "/notifications", auth.RoleAdmin, s.createNotificationChannel)
	h(fiber.MethodPut, "/notifications/:id", auth.RoleAdmin, s.updateNotificationChannel)
	h(fiber.MethodDelete, "/notifications/:id", auth.RoleAdmin, s.deleteNotificationChannel)

	// Worker 在线状态（调试/管理）
	h(fiber.MethodGet, "/workers", auth.RoleViewer, s.listWorkers)

	// Copilot SSE 反代（生产前端托管后，/copilot-api/* → copilot /api/*）。
	// 必须认证：不允许未登录流量直达 Copilot 服务（Authorization 原样透传由 Copilot 复核）。
	if s.cfg.CopilotURL != "" {
		app.All("/copilot-api/*", auth.Middleware(s.cfg.JWTSecret),
			auth.RequireRole(auth.RoleViewer, s.proxyCopilot))
	}

	// 可选：托管前端构建产物（static_dir；HashRouter，无需 history 回退）。
	// 注册在最后：API 路由先匹配，静态中间件只在无 API 命中时尝试文件。
	if s.cfg.StaticDir != "" {
		app.Use(static.New(s.cfg.StaticDir))
	}

	return app
}

// errorHandler 统一错误出口：/api 下保持 {"error":{"code","message"}} 契约
// （未匹配路由 404 等 fiber 内建错误也走这里），其余纯文本。
func errorHandler(c fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	msg := "internal error"
	var fe *fiber.Error
	if errors.As(err, &fe) {
		code, msg = fe.Code, fe.Message
	} else if err != nil {
		msg = err.Error()
	}
	if strings.HasPrefix(c.Path(), "/api/") {
		return writeErr(c, code, msg)
	}
	return c.Status(code).SendString(msg)
}

// corsDev 开发态宽松 CORS（生产经同源嵌入托管，不需要）。
// 仅当 HTTP 监听绑定在回环地址时才放行任意 Origin；生产监听（0.0.0.0/内网 IP）
// 时不设置 ACAO（同源托管场景浏览器不依赖 CORS 头，避免凭证被任意站点读取）。
func corsDev(c fiber.Ctx) error {
	if isLoopbackAddr(c) {
		c.Set("Access-Control-Allow-Origin", "*")
		c.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if c.Method() == fiber.MethodOptions {
			return c.SendStatus(fiber.StatusNoContent)
		}
	}
	return c.Next()
}

// isLoopbackAddr 判断请求来源地址是否为回环（CORS 宽松只给本地开发）。
func isLoopbackAddr(c fiber.Ctx) bool {
	ip := net.ParseIP(c.IP())
	return ip != nil && ip.IsLoopback()
}
