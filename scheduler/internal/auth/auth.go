package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// Claims 是 JWT 载荷（含租户与角色）。
type Claims struct {
	UserID   int64 `json:"uid"`
	TenantID int64 `json:"tid"`
	Role     int16 `json:"role"`
	jwt.RegisteredClaims
}

type ctxKey string

const ClaimsKey ctxKey = "tp_claims"

// HashPassword 生成 bcrypt 哈希。
func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

// CheckPassword 校验密码。
func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// IssueToken 签发 JWT（expireHours<=0 时按 24h）。
func IssueToken(secret string, userID, tenantID int64, role int16, expireHours int) (string, error) {
	if expireHours <= 0 {
		expireHours = 24
	}
	c := Claims{
		UserID:   userID,
		TenantID: tenantID,
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Duration(expireHours) * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "testpilot",
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(secret))
}

// ParseToken 校验并解析 JWT。
func ParseToken(secret, token string) (*Claims, error) {
	var c Claims
	t, err := jwt.ParseWithClaims(token, &c, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret), nil
	})
	if err != nil || !t.Valid {
		return nil, errors.New("invalid token")
	}
	return &c, nil
}

// FromContext 取调用者身份信息。
func FromContext(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(ClaimsKey).(*Claims)
	return c, ok
}

// Middleware 解析 Authorization: Bearer <token>，注入 Claims（经 c.SetContext，
// 下游 handler 由 c.Context() 取回）。
func Middleware(secret string) fiber.Handler {
	return func(c fiber.Ctx) error {
		h := c.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			return c.Status(fiber.StatusUnauthorized).
				SendString(`{"error":{"code":"UNAUTHORIZED","message":"missing bearer token"}}`)
		}
		claims, err := ParseToken(secret, strings.TrimPrefix(h, "Bearer "))
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).
				SendString(`{"error":{"code":"UNAUTHORIZED","message":"invalid token"}}`)
		}
		c.SetContext(context.WithValue(c.Context(), ClaimsKey, claims))
		return c.Next()
	}
}
