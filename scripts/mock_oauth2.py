#!/usr/bin/env python3
"""Mock OAuth2 授权服务器（stdlib 零依赖，GitHub 风格：**不发布** discovery 文档）。

端点：
  GET  /authorize?...&redirect_uri&state   302 回 redirect_uri?code=mock-code&state=...
  POST /token                              client_secret_basic；返回 access_token（无 id_token）
  GET  /userinfo                           Bearer access_token → {sub, email, name}

约定：client_id=mock-client  client_secret=mock-secret  用户=oauth2-user/mock@oauth2.local
（无 discovery → e2e 走 IdP config 显式端点兜底路径）

用法：python3 scripts/mock_oauth2.py [port=18110]
"""

import base64
import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

ISSUER = None  # main 里按端口填充
CLIENT_ID = "mock-client"
CLIENT_SECRET = "mock-secret"


class Provider(BaseHTTPRequestHandler):
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
        if u.path == "/userinfo":
            auth = self.headers.get("Authorization", "")
            if auth != "Bearer mock-access":
                return self._json(401, {"error": "invalid_token"})
            return self._json(200, {
                "sub": "oauth2-user-1",
                "email": "mock@oauth2.local",
                "name": "oauth2-user",
            })
        self._json(404, {"error": "not found"})

    def do_POST(self):
        u = urlparse(self.path)
        if u.path != "/token":
            return self._json(404, {"error": "not found"})
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
        })

    def log_message(self, fmt, *args):
        sys.stderr.write("mock-oauth2 %s\n" % (fmt % args))


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 18110
    ISSUER = f"http://127.0.0.1:{port}"
    srv = ThreadingHTTPServer(("127.0.0.1", port), Provider)
    print(f"mock OAuth2 provider on {ISSUER} (no discovery)", flush=True)
    srv.serve_forever()
