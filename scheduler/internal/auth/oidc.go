// 通用 OIDC 授权码流客户端：discovery + code 换 token + id_token 验签。
//
// 验签支持 RS256（JWKS）与 HS256（client_secret，部分内网 IdP/测试用）。
package auth

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// DiscoveryDoc .well-known/openid-configuration 的必要字段。
type DiscoveryDoc struct {
	Issuer        string `json:"issuer"`
	Authorization string `json:"authorization_endpoint"`
	Token         string `json:"token_endpoint"`
	JWKS          string `json:"jwks_uri"`
}

// OIDCClaims id_token 中业务关心的部分。
type OIDCClaims struct {
	Sub               string
	Email             string
	PreferredUsername string
}

var httpClient = &http.Client{Timeout: 10 * time.Second}

// ---- discovery / JWKS 缓存（10min TTL）----

type cacheEntry struct {
	doc  *DiscoveryDoc
	jwks *jwksSet
	at   time.Time
}

var (
	discCache sync.Map // issuer → cacheEntry
	cacheTTL  = 10 * time.Minute
)

// Discover 拉取（并缓存）issuer 的 discovery 文档。
func Discover(issuer string) (*DiscoveryDoc, error) {
	if e, ok := discCache.Load(issuer); ok && time.Since(e.(cacheEntry).at) < cacheTTL {
		return e.(cacheEntry).doc, nil
	}
	u := strings.TrimSuffix(issuer, "/") + "/.well-known/openid-configuration"
	resp, err := httpClient.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("discovery status %d", resp.StatusCode)
	}
	var doc DiscoveryDoc
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return nil, err
	}
	discCache.Store(issuer, cacheEntry{doc: &doc, at: time.Now()})
	return &doc, nil
}

// AuthURL 组装授权跳转地址。
func AuthURL(doc *DiscoveryDoc, clientID, redirectURI, state string) string {
	q := url.Values{
		"client_id":     {clientID},
		"redirect_uri":  {redirectURI},
		"response_type": {"code"},
		"scope":         {"openid email profile"},
		"state":         {state},
	}
	return doc.Authorization + "?" + q.Encode()
}

// Exchange code → token 响应（client_secret_basic）。
func Exchange(doc *DiscoveryDoc, clientID, clientSecret, code, redirectURI string) (map[string]any, error) {
	req, err := http.NewRequest("POST", doc.Token, strings.NewReader(url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {redirectURI},
	}.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(url.QueryEscape(clientID), url.QueryEscape(clientSecret))
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("token endpoint status %d: %s", resp.StatusCode, string(raw[:min(200, len(raw))]))
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ---- id_token 验签 ----

type jwksKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwksSet struct {
	Keys []jwksKey `json:"keys"`
}

func fetchJWKS(uri string) (*jwksSet, error) {
	resp, err := httpClient.Get(uri)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var set jwksSet
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&set); err != nil {
		return nil, err
	}
	return &set, nil
}

func rsaFromJWK(k jwksKey) (*rsa.PublicKey, error) {
	nb, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, err
	}
	eb, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, err
	}
	var e int
	for _, b := range eb {
		e = e<<8 | int(b)
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: e}, nil
}

// VerifyIDToken 验签 + 校验 iss/aud/exp，返回业务 claims。
func VerifyIDToken(raw, issuer, clientID, clientSecret string) (*OIDCClaims, error) {
	doc, err := Discover(issuer)
	if err != nil {
		return nil, err
	}
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		switch t.Method.Alg() {
		case "RS256":
			set, err := fetchJWKS(doc.JWKS)
			if err != nil {
				return nil, err
			}
			kid, _ := t.Header["kid"].(string)
			for _, k := range set.Keys {
				if k.Kty == "RSA" && (kid == "" || k.Kid == kid) {
					return rsaFromJWK(k)
				}
			}
			return nil, errors.New("no matching RSA key in JWKS")
		case "HS256":
			return []byte(clientSecret), nil
		}
		return nil, fmt.Errorf("unexpected alg %s", t.Method.Alg())
	}, jwt.WithIssuer(issuer), jwt.WithAudience(clientID), jwt.WithExpirationRequired())
	if err != nil || !token.Valid {
		return nil, fmt.Errorf("id_token invalid: %v", err)
	}
	out := &OIDCClaims{}
	out.Sub, _ = claims["sub"].(string)
	out.Email, _ = claims["email"].(string)
	out.PreferredUsername, _ = claims["preferred_username"].(string)
	if out.Sub == "" && out.Email == "" {
		return nil, errors.New("id_token lacks sub and email")
	}
	return out, nil
}
