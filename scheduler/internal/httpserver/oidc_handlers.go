package httpserver

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/auth"
	"github.com/testpilot/testpilot/internal/logging"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// ---- OIDC 登录（授权码流）----
// GET  /api/v1/auth/oidc/providers            公开：可用身份源（登录页按钮）
// GET  /api/v1/auth/oidc/{id}/login           302 到 IdP 授权端点
// GET  /api/v1/auth/oidc/{id}/callback        code 换 token、验签、建/联账号、签发本地 JWT

type oidcStateEntry struct {
	at         time.Time
	redirect   string // 登录成功后 302 回跳的浏览器 origin（前端 SSE 登录页用）
	providerID int64  // 发起登录的 IdP（防 A 的 state 在 B 的 callback 兑现）
}

var oidcStates sync.Map // state → oidcStateEntry（10min 有效）

func newOIDCState(redirect string, providerID int64) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	s := hex.EncodeToString(b)
	oidcStates.Store(s, oidcStateEntry{at: time.Now(), redirect: redirect, providerID: providerID})
	return s
}

func takeOIDCState(s string, providerID int64) (string, bool) {
	v, ok := oidcStates.LoadAndDelete(s)
	if !ok {
		return "", false
	}
	e := v.(oidcStateEntry)
	if time.Since(e.at) > 10*time.Minute || e.providerID != providerID {
		return "", false
	}
	return e.redirect, true
}

// redirectAllowed 302 回跳白名单：同 host:port 精确匹配，或 localhost/回环（dev 前端 :5173 跨端口）。
// 加固：拒绝带 userinfo 的 URL（user@host）；Host 头参与判定时按 host:port 整体比较，
// 防止反代场景下 Host 头污染导致 token 回跳到攻击者域名。
func redirectAllowed(redirect string, ctx fiber.Ctx) bool {
	u, err := url.Parse(redirect)
	if err != nil || u.Hostname() == "" || u.User != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	if strings.EqualFold(u.Host, ctx.Host()) {
		return true
	}
	h := strings.ToLower(u.Hostname())
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

func (s *Server) oidcCallbackURI(ctx fiber.Ctx, id int64) string {
	// X-Forwarded-Proto 仅信任 https 值（反代场景覆盖客户端伪造）；
	// 其余一律按 http，杜绝任意 scheme 拼进回调 URL。
	proto := "http"
	if ctx.Get("X-Forwarded-Proto") == "https" {
		proto = "https"
	}
	return fmt.Sprintf("%s://%s/api/v1/auth/oidc/%d/callback", proto, ctx.Host(), id)
}

func (s *Server) listOIDCProvidersPublic(ctx fiber.Ctx) error {
	var rows []model.IdentityProvider
	s.db.Where("enabled = ?", true).Find(&rows)
	out := make([]map[string]any, 0, len(rows))
	for _, p := range rows {
		out = append(out, map[string]any{"id": p.ID, "name": p.Name, "type": p.Type})
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"items": out})
}

// resolveDoc 取身份源的端点：优先 discovery 文档；失败或提供方不发布时用 IdP 配置
// 显式端点兜底/覆盖（纯 OAuth2 提供方如 GitHub 没有 openid-configuration）。
func (s *Server) resolveDoc(p *model.IdentityProvider) (*auth.DiscoveryDoc, error) {
	cfg := p.ConfigMap()
	doc, err := auth.Discover(p.Issuer)
	if err != nil {
		if cfg["authorization_endpoint"] == "" || cfg["token_endpoint"] == "" {
			return nil, err
		}
		doc = &auth.DiscoveryDoc{Issuer: p.Issuer,
			Authorization: cfg["authorization_endpoint"], Token: cfg["token_endpoint"]}
	}
	if v := cfg["authorization_endpoint"]; v != "" {
		doc.Authorization = v
	}
	if v := cfg["token_endpoint"]; v != "" {
		doc.Token = v
	}
	if v := cfg["userinfo_endpoint"]; v != "" {
		doc.UserInfo = v
	}
	return doc, nil
}

func (s *Server) oidcLogin(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "invalid provider id"))
	}
	var p model.IdentityProvider
	if err := s.db.First(&p, id).Error; err != nil || !p.Enabled {
		return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "provider not found"))
	}
	doc, err := s.resolveDoc(&p)
	if err != nil {
		return writeAppErr(ctx, apperr.New(502, apperr.CodeOIDCExchange, "discovery failed: "+err.Error()))
	}
	// strings.Clone：fiber v3 ctx.Query 是 fasthttp 查询缓冲的零拷贝视图，
	// 连接复用后缓冲区会被后续请求覆盖，跨请求存活的 state 必须拷贝。
	rd := strings.Clone(ctx.Query("redirect"))
	state := newOIDCState(rd, id)
	return ctx.Redirect().Status(fiber.StatusFound).To(auth.AuthURL(doc, p.ClientID, s.oidcCallbackURI(ctx, id), state))
}

func (s *Server) oidcCallback(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "invalid provider id"))
	}
	redirect, ok := takeOIDCState(strings.Clone(ctx.Query("state")), id)
	if !ok {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeOIDCState, "invalid or expired state"))
	}
	code := ctx.Query("code")
	if code == "" {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "missing code"))
	}
	var p model.IdentityProvider
	if err := s.db.First(&p, id).Error; err != nil || !p.Enabled {
		return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "provider not found"))
	}
	doc, err := s.resolveDoc(&p)
	if err != nil {
		return writeAppErr(ctx, apperr.New(502, apperr.CodeOIDCExchange, "discovery failed: "+err.Error()))
	}
	tok, err := auth.Exchange(doc, p.ClientID, p.ClientSecret, code, s.oidcCallbackURI(ctx, id))
	if err != nil {
		return writeAppErr(ctx, apperr.New(502, apperr.CodeOIDCExchange, err.Error()))
	}
	// oidc：id_token 验签取身份；oauth2：access_token 换 userinfo 取身份
	var claims *auth.OIDCClaims
	switch p.Type {
	case "oauth2":
		access, _ := tok["access_token"].(string)
		if access == "" {
			return writeAppErr(ctx, apperr.New(502, apperr.CodeOIDCExchange, "token response lacks access_token"))
		}
		claims, err = auth.FetchUserInfo(doc.UserInfo, access)
	case "", "oidc":
		rawIDToken, _ := tok["id_token"].(string)
		if rawIDToken == "" {
			return writeAppErr(ctx, apperr.New(502, apperr.CodeOIDCExchange, "token response lacks id_token"))
		}
		claims, err = auth.VerifyIDToken(rawIDToken, p.Issuer, p.ClientID, p.ClientSecret)
	default:
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "unsupported provider type "+p.Type))
	}
	if err != nil {
		return writeAppErr(ctx, apperr.New(502, apperr.CodeOIDCExchange, err.Error()))
	}

	u, m, linkErr := s.linkOrCreateUser(&p, claims, claims.PreferredUsername)
	if linkErr != nil {
		return writeAppErr(ctx, linkErr)
	}
	token, err := auth.IssueToken(s.cfg.JWTSecret, u.ID, m.TenantID, m.Role, s.cfg.JWTExpireHours)
	if err != nil {
		return writeAppErr(ctx, apperr.Internal(err.Error()))
	}
	// 浏览器 SSO 流：带 redirect 时 302 回跳前端 hash 路由（token 走 URL）；
	// 否则保持 JSON（API 客户端兼容）
	if redirect != "" && redirectAllowed(redirect, ctx) {
		ctx.Set(fiber.HeaderCacheControl, "no-store")
		return ctx.Redirect().Status(fiber.StatusFound).
			To(redirect + "/#/auth/callback?token=" + url.QueryEscape(token))
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{
		"token": token, "user": u, "tenant_id": m.TenantID, "role": m.Role,
	})
}

// linkOrCreateUser 账号联结：
//  1. 已有 IdP 绑定 → 按 (identity_provider_id, sub) 精确命中（身份锚点，不受邮箱变更影响）；
//  2. 未绑定 → 仅当 email_verified=true 时允许按 email 联结既有账号（联结后写回绑定）；
//  3. 其余（未验证邮箱/新用户）→ 创建新账号（随机不可登录密码）+ provider 租户 viewer 成员。
// 防止：攻击者在 IdP 注册未验证邮箱 victim@corp 后直接接管既有账号。
func (s *Server) linkOrCreateUser(p *model.IdentityProvider, claims *auth.OIDCClaims, username string) (*model.User, *model.TenantMember, *apperr.Error) {
	var u model.User
	found := false
	// 1) 身份锚点：按 (provider, sub) 查绑定
	if claims.Sub != "" && p.ID != 0 {
		err := s.db.Where("oidc_provider_id = ? AND oidc_sub = ?", p.ID, claims.Sub).First(&u).Error
		if err == nil {
			found = true
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, apperr.Internal("oidc binding lookup failed")
		}
	}
	// 2) email 联结（仅已验证邮箱）
	if !found && claims.Email != "" && claims.EmailVerified {
		err := s.db.Where("email = ?", claims.Email).First(&u).Error
		if err == nil {
			found = true
			// 联结成功 → 写回绑定锚点
			if claims.Sub != "" && p.ID != 0 {
				if uerr := s.db.Model(&u).Updates(map[string]any{
					"oidc_provider_id": p.ID, "oidc_sub": claims.Sub,
				}).Error; uerr != nil {
					logging.L.Warnw("oidc binding writeback failed", "user", u.ID, "err", uerr)
				}
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, apperr.Internal("user lookup failed")
		}
	}
	// 3) 创建新账号
	if !found {
		email := claims.Email
		if email == "" {
			email = claims.Sub + "@" + p.Issuer
		}
		if username == "" {
			username = strings.Split(email, "@")[0]
		}
		// 用户名唯一性避让
		name := username
		for i := 1; ; i++ {
			var cnt int64
			s.db.Model(&model.User{}).Where("username = ?", name).Count(&cnt)
			if cnt == 0 {
				break
			}
			name = fmt.Sprintf("%s%d", username, i)
		}
		rb := make([]byte, 24)
		_, _ = rand.Read(rb)
		hash, err := auth.HashPassword(hex.EncodeToString(rb))
		if err != nil {
			return nil, nil, apperr.Internal(err.Error())
		}
		u = model.User{ID: model.NextID(), Username: name, Email: email,
			PasswordHash: string(hash), DisplayName: username, Status: 1,
			OIDCProviderID: p.ID, OIDCSub: claims.Sub}
		if err := s.db.Create(&u).Error; err != nil {
			return nil, nil, apperr.Internal(err.Error())
		}
	}
	var m model.TenantMember
	if err := s.db.Where("user_id = ? AND tenant_id = ?", u.ID, p.TenantID).First(&m).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, apperr.Internal("member lookup failed")
		}
		m = model.TenantMember{ID: model.NextID(), TenantID: p.TenantID, UserID: u.ID, Role: auth.RoleViewer}
		if err := s.db.Create(&m).Error; err != nil {
			return nil, nil, apperr.Internal(err.Error())
		}
	}
	return &u, &m, nil
}

// ---- 身份源管理（admin）----

func (s *Server) listIdentityProviders(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	return listOf[model.IdentityProvider](s.db, ctx, func(q *gorm.DB) *gorm.DB {
		return q.Where("tenant_id = ?", c.TenantID).Order("id desc")
	})
}

type providerReq struct {
	Name         string `json:"name"`
	Issuer       string `json:"issuer"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	Type         string `json:"type"` // oidc（默认）| oauth2
	// oauth2 提供方端点覆盖（无 discovery 时必填 authorization/token；userinfo 必填）
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserInfoEndpoint      string `json:"userinfo_endpoint"`
	Enabled               *bool  `json:"enabled"`
}

func (in *providerReq) endpointsConfig() model.JSON {
	m := map[string]string{}
	for k, v := range map[string]string{
		"authorization_endpoint": in.AuthorizationEndpoint,
		"token_endpoint":         in.TokenEndpoint,
		"userinfo_endpoint":      in.UserInfoEndpoint,
	} {
		if v != "" {
			m[k] = v
		}
	}
	if len(m) == 0 {
		return nil
	}
	b, _ := json.Marshal(m)
	return model.JSON(b)
}

func (s *Server) createIdentityProvider(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var in providerReq
	if !decode(ctx, &in) {
		return nil
	}
	if in.Issuer == "" || in.ClientID == "" {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "issuer 与 client_id 必填"))
	}
	ptype := in.Type
	if ptype == "" {
		ptype = "oidc"
	}
	if ptype != "oidc" && ptype != "oauth2" {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "type 仅支持 oidc/oauth2"))
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	row := &model.IdentityProvider{
		ID: model.NextID(), TenantID: c.TenantID, Name: in.Name, Type: ptype,
		Issuer: strings.TrimSuffix(in.Issuer, "/"), ClientID: in.ClientID,
		ClientSecret: in.ClientSecret, Config: in.endpointsConfig(), Enabled: enabled,
	}
	if in.Name == "" {
		row.Name = in.Issuer
	}
	if err := s.db.Create(row).Error; err != nil {
		return writeAppErr(ctx, apperr.Internal(err.Error()))
	}
	return writeJSON(ctx, fiber.StatusOK, row)
}

func (s *Server) updateIdentityProvider(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "invalid id"))
	}
	var row model.IdentityProvider
	if err := s.db.Where("id = ? AND tenant_id = ?", id, c.TenantID).First(&row).Error; err != nil {
		return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "provider not found"))
	}
	var in providerReq
	if !decode(ctx, &in) {
		return nil
	}
	if in.Name != "" {
		row.Name = in.Name
	}
	if in.Issuer != "" {
		row.Issuer = strings.TrimSuffix(in.Issuer, "/")
	}
	if in.ClientID != "" {
		row.ClientID = in.ClientID
	}
	if in.ClientSecret != "" {
		row.ClientSecret = in.ClientSecret
	}
	if in.Type != "" {
		if in.Type != "oidc" && in.Type != "oauth2" {
			return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "type 仅支持 oidc/oauth2"))
		}
		row.Type = in.Type
	}
	if cfg := in.endpointsConfig(); len(cfg) > 0 {
		// 合并进现有 config（新端点覆盖旧值）
		merged := row.ConfigMap()
		var m map[string]string
		if json.Unmarshal([]byte(cfg), &m) == nil {
			for k, v := range m {
				merged[k] = v
			}
		}
		b, _ := json.Marshal(merged)
		row.Config = model.JSON(b)
	}
	if in.Enabled != nil {
		row.Enabled = *in.Enabled
	}
	if err := s.db.Save(&row).Error; err != nil {
		return writeAppErr(ctx, apperr.Internal(err.Error()))
	}
	return writeJSON(ctx, fiber.StatusOK, row)
}

func (s *Server) deleteIdentityProvider(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "invalid id"))
	}
	res := s.db.Where("id = ? AND tenant_id = ?", id, c.TenantID).Delete(&model.IdentityProvider{})
	if res.RowsAffected == 0 {
		return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "provider not found"))
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"ok": true})
}
