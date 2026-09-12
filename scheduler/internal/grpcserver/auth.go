// Package grpcserver 的 gRPC 认证拦截器：
//
//   - CopilotToolService（unary）：要求 metadata authorization: Bearer <JWT>，
//     校验通过后核对请求内 RequestContext（tenant_id/user_id）与 JWT claims 一致——
//     客户端无法再自报任意租户身份（原本完全信任调用方自带 RequestContext）。
//   - WorkerService.Connect（stream）：要求 metadata x-worker-token 与 Scheduler
//     配置的 worker_token 一致；未配置 token 时拒绝一切 Worker 注册（默认安全）。
package grpcserver

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	"github.com/testpilot/testpilot/internal/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	workerServiceConnect = "/testpilot.worker.v1.WorkerService/Connect"
	copilotServicePrefix = "/testpilot.copilot.v1.CopilotToolService/"
)

// bearerToken 从 incoming metadata 提取 Bearer token。
func bearerToken(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	for _, v := range md.Get("authorization") {
		if strings.HasPrefix(v, "Bearer ") {
			return strings.TrimPrefix(v, "Bearer ")
		}
	}
	return ""
}

// requestContextOf 反射取出请求消息中的 *commonv1.RequestContext 字段。
// 注意：proto 生成代码的字段名是 Ctx（proto 中 name=ctx），不是 "Context"——
// 曾因 FieldByName("Context") 导致全部 Copilot 工具 RPC 被误拒（InvalidArgument）。
// 契约：CopilotToolService 所有请求的第一个字段必须是 ctx（见 copilot.proto）。
func requestContextOf(req any) *commonv1.RequestContext {
	v := reflect.ValueOf(req)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	f := v.FieldByName("Ctx")
	if !f.IsValid() || f.Kind() != reflect.Ptr {
		return nil
	}
	rc, _ := f.Interface().(*commonv1.RequestContext)
	return rc
}

// CopilotAuthUnary 校验 Copilot 工具面的 JWT，并核对请求自带身份的归属。
func CopilotAuthUnary(jwtSecret string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !strings.HasPrefix(info.FullMethod, copilotServicePrefix) {
			return handler(ctx, req)
		}
		token := bearerToken(ctx)
		if token == "" {
			return nil, status.Error(codes.Unauthenticated,
				"copilot gRPC requires authorization: Bearer <jwt>")
		}
		claims, err := auth.ParseToken(jwtSecret, token)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "invalid jwt: "+err.Error())
		}
		rc := requestContextOf(req)
		if rc == nil {
			return nil, status.Error(codes.InvalidArgument, "request missing RequestContext")
		}
		// 身份一致性：客户端自报身份必须与 JWT 主体一致，杜绝伪造租户/用户
		if rc.GetTenantId() != claims.TenantID ||
			rc.GetUserId() != strconv.FormatInt(claims.UserID, 10) {
			return nil, status.Error(codes.PermissionDenied,
				"request context tenant/user does not match jwt claims")
		}
		// 角色边界与 REST 面对齐（server.go：viewer 只读 GET，member 才有领域
		// CRUD/触发）：读工具对 viewer 开放，写/触发工具要求 member 及以上。
		// 未知方法按需写权限处理（fail-closed），新增写 RPC 不至于漏防。
		if copilotMethodRequiresWrite(info.FullMethod) && claims.Role > auth.RoleMember {
			return nil, status.Error(codes.PermissionDenied,
				"role "+auth.RoleName(claims.Role)+" lacks permission (requires "+auth.RoleName(auth.RoleMember)+")")
		}
		return handler(ctx, req)
	}
}

// copilotMethodRequiresWrite 按 RPC 方法名判定是否写/触发类工具。
// 读前缀：List/Get/Query/Check；写前缀：Create/Update/Delete/Import/Apply/Trigger。
func copilotMethodRequiresWrite(fullMethod string) bool {
	name := fullMethod
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	for _, prefix := range []string{"List", "Get", "Query", "Check"} {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	return true
}

// workerTokenFromContext 从流的 incoming metadata 再取一次 x-worker-token
// （Connect 内注册时用：租户绑定需要"出示令牌 vs 声明租户"精确比对）。
func workerTokenFromContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	toks := md.Get("x-worker-token")
	if len(toks) != 1 {
		return ""
	}
	return toks[0]
}

// ParseWorkerTenantTokens 解析 TP_WORKER_TENANT_TOKENS：
// "租户ID=令牌,租户ID=令牌"。空串返回 nil（不启用租户绑定）。
func ParseWorkerTenantTokens(spec string) (map[int64]string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}
	out := make(map[int64]string)
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, tok, found := strings.Cut(part, "=")
		if !found || strings.TrimSpace(id) == "" || strings.TrimSpace(tok) == "" {
			return nil, fmt.Errorf("invalid TP_WORKER_TENANT_TOKENS entry %q (want tenant_id=token)", part)
		}
		tenant, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64)
		if err != nil || tenant <= 0 {
			return nil, fmt.Errorf("invalid TP_WORKER_TENANT_TOKENS tenant id %q", id)
		}
		out[tenant] = strings.TrimSpace(tok)
	}
	return out, nil
}

// WorkerAuthStream 校验 Worker 流令牌：共享令牌，或（配置了租户映射时）
// 任一租户令牌——精确的租户↔令牌绑定在 Connect 注册帧时校验。
func WorkerAuthStream(workerToken string, tenantTokens map[int64]string) grpc.StreamServerInterceptor {
	known := make(map[string]struct{})
	for _, t := range tenantTokens {
		known[t] = struct{}{}
	}
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if info.FullMethod != workerServiceConnect {
			return handler(srv, ss)
		}
		if workerToken == "" && len(known) == 0 {
			return status.Error(codes.Unauthenticated,
				"scheduler worker_token 未配置：拒绝一切 Worker 注册（请设置 TP_WORKER_TOKEN）")
		}
		md, ok := metadata.FromIncomingContext(ss.Context())
		if !ok {
			return status.Error(codes.Unauthenticated, "missing worker token metadata")
		}
		toks := md.Get("x-worker-token")
		if len(toks) != 1 || toks[0] == "" {
			return status.Error(codes.Unauthenticated, "invalid worker token")
		}
		if toks[0] != workerToken {
			if _, isTenant := known[toks[0]]; !isTenant {
				return status.Error(codes.Unauthenticated, "invalid worker token")
			}
		}
		return handler(srv, ss)
	}
}
