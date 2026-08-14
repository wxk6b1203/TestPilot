package auth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestHashPasswordCheckPasswordRoundTrip(t *testing.T) {
	const pw = "S3cure!pass"
	hash, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == "" || hash == pw {
		t.Fatalf("hash should be non-empty and differ from plaintext, got %q", hash)
	}
	if !CheckPassword(hash, pw) {
		t.Fatal("CheckPassword(hash, pw) = false, want true")
	}
}

func TestCheckPasswordWrongPassword(t *testing.T) {
	hash, err := HashPassword("correct-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if CheckPassword(hash, "wrong-password") {
		t.Fatal("CheckPassword(hash, wrong) = true, want false")
	}
	if CheckPassword(hash, "") {
		t.Fatal("CheckPassword(hash, empty) = true, want false")
	}
}

func TestIssueTokenParseTokenRoundTrip(t *testing.T) {
	const secret = "test-secret"
	tok, err := IssueToken(secret, 123, 456, RoleAdmin, 8)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	c, err := ParseToken(secret, tok)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if c.UserID != 123 {
		t.Errorf("UserID = %d, want 123", c.UserID)
	}
	if c.TenantID != 456 {
		t.Errorf("TenantID = %d, want 456", c.TenantID)
	}
	if c.Role != RoleAdmin {
		t.Errorf("Role = %d, want %d", c.Role, RoleAdmin)
	}
	if c.Issuer != "testpilot" {
		t.Errorf("Issuer = %q, want %q", c.Issuer, "testpilot")
	}
	if c.ExpiresAt == nil || c.IssuedAt == nil {
		t.Fatal("ExpiresAt/IssuedAt must be set")
	}
	if d := c.ExpiresAt.Sub(c.IssuedAt.Time); d != 8*time.Hour {
		t.Errorf("ExpiresAt-IssuedAt = %v, want 8h", d)
	}
}

func TestParseTokenWrongSecret(t *testing.T) {
	tok, err := IssueToken("secret-A", 1, 2, RoleMember, 1)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	if _, err := ParseToken("secret-B", tok); err == nil {
		t.Fatal("ParseToken with wrong secret: want error, got nil")
	}
}

func TestParseTokenExpired(t *testing.T) {
	const secret = "test-secret"
	// 手工构造一个过期 token：同 secret 签名，ExpiresAt 在过去。
	c := Claims{
		UserID:   1,
		TenantID: 2,
		Role:     RoleViewer,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-3 * time.Hour)),
			Issuer:    "testpilot",
		},
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign expired token: %v", err)
	}
	if _, err := ParseToken(secret, tok); err == nil {
		t.Fatal("ParseToken of expired token: want error, got nil")
	}
}

func TestIssueTokenExpireHoursFallback(t *testing.T) {
	const secret = "test-secret"
	for _, hours := range []int{0, -1, -100} {
		tok, err := IssueToken(secret, 1, 2, RoleMember, hours)
		if err != nil {
			t.Fatalf("IssueToken(%d): %v", hours, err)
		}
		c, err := ParseToken(secret, tok)
		if err != nil {
			t.Fatalf("ParseToken(%d): %v", hours, err)
		}
		d := c.ExpiresAt.Sub(c.IssuedAt.Time)
		// 两个 time.Now() 可能跨秒，允许 1 秒误差。
		if d < 24*time.Hour-time.Second || d > 24*time.Hour+time.Second {
			t.Errorf("expireHours=%d: ExpiresAt-IssuedAt = %v, want ~24h", hours, d)
		}
	}
}

func TestRoleName(t *testing.T) {
	cases := []struct {
		role int16
		want string
	}{
		{RoleOwner, "owner"},
		{RoleAdmin, "admin"},
		{RoleMember, "member"},
		{RoleViewer, "viewer"},
		{0, "unknown"},
		{99, "unknown"},
		{-1, "unknown"},
	}
	for _, tc := range cases {
		if got := RoleName(tc.role); got != tc.want {
			t.Errorf("RoleName(%d) = %q, want %q", tc.role, got, tc.want)
		}
	}
}

// FetchUserInfo（OAuth2 回调身份来源）：Bearer 校验 + 字段映射 + 错误面。
func TestFetchUserInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-1" {
			http.Error(w, `{"error":"invalid_token"}`, 401)
			return
		}
		fmt.Fprint(w, `{"sub":"u-1","email":"a@b.c","name":"Alice"}`)
	}))
	defer srv.Close()

	claims, err := FetchUserInfo(srv.URL, "tok-1")
	if err != nil {
		t.Fatal(err)
	}
	if claims.Sub != "u-1" || claims.Email != "a@b.c" || claims.PreferredUsername != "Alice" {
		t.Fatalf("claims: %+v", claims)
	}

	// preferred_username 优先于 name
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"sub":"u-2","preferred_username":"p","name":"n"}`)
	}))
	defer srv2.Close()
	c2, err := FetchUserInfo(srv2.URL, "x")
	if err != nil {
		t.Fatal(err)
	}
	if c2.PreferredUsername != "p" {
		t.Fatalf("preferred_username should win: %+v", c2)
	}

	// 错误面：401 / 空端点 / 缺身份字段
	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", 500)
	}))
	defer srv3.Close()
	if _, err := FetchUserInfo(srv3.URL, "x"); err == nil {
		t.Fatal("500 should error")
	}
	if _, err := FetchUserInfo("", "x"); err == nil {
		t.Fatal("empty endpoint should error")
	}
	srv4 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"name":"only-name"}`)
	}))
	defer srv4.Close()
	if _, err := FetchUserInfo(srv4.URL, "x"); err == nil {
		t.Fatal("missing sub+email should error")
	}
}
