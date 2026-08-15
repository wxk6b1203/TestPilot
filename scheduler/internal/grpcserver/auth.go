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
func requestContextOf(req any) *commonv1.RequestContext {
	v := reflect.ValueOf(req)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	f := v.FieldByName("Context")
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
		return handler(ctx, req)
	}
}

// WorkerAuthStream 校验 Worker 流的共享令牌。
func WorkerAuthStream(workerToken string) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if info.FullMethod != workerServiceConnect {
			return handler(srv, ss)
		}
		if workerToken == "" {
			return status.Error(codes.Unauthenticated,
				"scheduler worker_token 未配置：拒绝一切 Worker 注册（请设置 TP_WORKER_TOKEN）")
		}
		md, ok := metadata.FromIncomingContext(ss.Context())
		if !ok {
			return status.Error(codes.Unauthenticated, "missing worker token metadata")
		}
		toks := md.Get("x-worker-token")
		if len(toks) != 1 || toks[0] == "" || toks[0] != workerToken {
			return status.Error(codes.Unauthenticated, "invalid worker token")
		}
		return handler(srv, ss)
	}
}
