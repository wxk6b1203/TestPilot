#!/usr/bin/env python3
"""Mock OIDC IdP（stdlib 零依赖，HS256 签名）：Phase 8 OIDC 登录 e2e 用。

端点：
  GET /.well-known/openid-configuration   discovery 文档
  GET /authorize?...&redirect_uri&state   直接 302 回 redirect_uri?code=mock-code&state=...
  POST /token                             返回 id_token（HS256，client_secret 作 HMAC key）
  GET /jwks                               空 JWKS（HS256 不用）

约定：client_id=mock-client  client_secret=mock-secret  用户=oidc-user/mock@oidc.local

用法：python3 scripts/mock_oidc.py [port=18100]
"""

import base64
import hashlib
import hmac
import json
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

ISSUER = None  # main 里按端口填充
CLIENT_ID = "mock-client"
CLIENT_SECRET = "mock-secret"


def b64url(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode()


def make_id_token() -> str:
    now = int(time.time())
    header = {"alg": "HS256", "typ": "JWT"}
    payload = {
        "iss": ISSUER,
        "aud": CLIENT_ID,
        "sub": "oidc-user-1",
        "email": "mock@oidc.local",
        "preferred_username": "oidc-user",
        "iat": now,
        "exp": now + 600,
    }
    head = b64url(json.dumps(header).encode()) + "." + b64url(json.dumps(payload).encode())
    sig = hmac.new(CLIENT_SECRET.encode(), head.encode(), hashlib.sha256).digest()
    return head + "." + b64url(sig)


class IdP(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _json(self, status: int, payload: dict):
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        u = urlparse(self.path)
        if u.path == "/.well-known/openid-configuration":
            return self._json(200, {
                "issuer": ISSUER,
                "authorization_endpoint": ISSUER + "/authorize",
                "token_endpoint": ISSUER + "/token",
                "jwks_uri": ISSUER + "/jwks",
            })
        if u.path == "/authorize":
            q = parse_qs(u.query)
            redirect = q.get("redirect_uri", [""])[0]
            state = q.get("state", [""])[0]
            if q.get("client_id", [""])[0] != CLIENT_ID or not redirect:
                return self._json(400, {"error": "bad request"})
            sep = "&" if "?" in redirect else "?"
            self.send_response(302)
            self.send_header("Location", f"{redirect}{sep}code=mock-code&state={state}")
            self.send_header("Content-Length", "0")
            self.end_headers()
            return
        if u.path == "/jwks":
            return self._json(200, {"keys": []})
        self._json(404, {"error": "not found"})

    def do_POST(self):
        u = urlparse(self.path)
        if u.path != "/token":
            return self._json(404, {"error": "not found"})
        # client_secret_basic 校验
        auth = self.headers.get("Authorization", "")
        expect = "Basic " + base64.b64encode(f"{CLIENT_ID}:{CLIENT_SECRET}".encode()).decode()
        if auth != expect:
            return self._json(401, {"error": "invalid_client"})
        length = int(self.headers.get("Content-Length") or 0)
        form = parse_qs(self.rfile.read(length).decode())
        if form.get("code", [""])[0] != "mock-code":
            return self._json(400, {"error": "invalid_grant"})
        self._json(200, {
            "access_token": "mock-access",
            "token_type": "Bearer",
            "expires_in": 600,
            "id_token": make_id_token(),
        })

    def log_message(self, fmt, *args):
        sys.stderr.write("mock-oidc %s\n" % (fmt % args))


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 18100
    ISSUER = f"http://127.0.0.1:{port}"
    srv = ThreadingHTTPServer(("127.0.0.1", port), IdP)
    print(f"mock OIDC IdP on {ISSUER}", flush=True)
    srv.serve_forever()
