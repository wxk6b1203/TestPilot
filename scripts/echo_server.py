#!/usr/bin/env python3
"""本地 echo HTTP 服务（stdlib 零依赖）：用于端到端联调。

路由：
  *            /echo           回显 method/path/query/headers/body
  GET          /json           固定 JSON（含嵌套数组，供 JSONPath 断言）
  GET          /status/{n}     返回指定状态码
  GET/POST     /delay/{ms}     延迟 ms 毫秒后回显
  GET          /form           UI 测试页（输入→提交→结果显示）
  GET          /download/x.txt 下载测试（Content-Disposition）
"""

import json
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

SINK = []  # webhook 接收槽

FORM_PAGE = """<!doctype html>
<html><head><meta charset="utf-8"><title>TestPilot Form</title></head>
<body>
  <h1 id="title">Login</h1>
  <input id="username" name="username" placeholder="username">
  <input id="password" name="password" type="password" placeholder="password">
  <label><input id="remember" type="checkbox"> remember me</label>
  <button id="login-btn" onclick="doLogin()">Sign in</button>
  <div id="result" style="display:none"></div>
  <script>
    function doLogin() {
      var u = document.getElementById('username').value;
      var el = document.getElementById('result');
      if (!u) { el.style.display = 'block'; el.textContent = 'username required'; return; }
      el.style.display = 'block';
      el.textContent = 'Welcome, ' + u + '!';
    }
  </script>
</body></html>
"""


class Echo(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _reply(self, status: int, payload: dict):
        body = json.dumps(payload, ensure_ascii=False).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _handle(self):
        u = urlparse(self.path)
        parts = [p for p in u.path.split("/") if p]
        length = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(length) if length else b""

        if parts[:1] == ["form"] and self.command == "GET":
            body = FORM_PAGE.encode()
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        if parts[:1] == ["download"] and len(parts) == 2 and self.command == "GET":
            body = f"testpilot download: {parts[1]}\n".encode()
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Disposition", f'attachment; filename="{parts[1]}"')
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return

        try:
            body = json.loads(raw) if raw else None
        except ValueError:
            body = raw.decode("utf-8", errors="replace")

        if parts[:1] == ["sink"] and self.command == "POST":
            # Webhook 接收槽（Phase 8 通知 e2e 用）：记录 body，稍后 /sink/dump 取出
            SINK.append({"path": u.path, "body": body})
            return self._reply(200, {"ok": True})
        if parts[:2] == ["sink", "dump"] and self.command == "GET":
            out, SINK[:] = list(SINK), []
            return self._reply(200, {"items": out})
        if parts[:1] == ["json"] and self.command == "GET":
            return self._reply(200, {
                "id": 42, "name": "testpilot", "ok": True,
                "user": {"name": "neo", "roles": ["admin", "tester"]},
                "items": [{"sku": "a", "price": 9.5}, {"sku": "b", "price": 20}],
            })
        if parts[:1] == ["status"] and len(parts) == 2 and parts[1].isdigit():
            return self._reply(int(parts[1]), {"status": int(parts[1])})
        if parts[:1] == ["delay"] and len(parts) == 2 and parts[1].isdigit():
            time.sleep(min(int(parts[1]), 30000) / 1000)
            return self._reply(200, {"delayed_ms": int(parts[1])})
        return self._reply(200, {
            "echo": True,
            "method": self.command,
            "path": u.path,
            "query": {k: v[0] if len(v) == 1 else v for k, v in parse_qs(u.query).items()},
            "headers": {k.lower(): v for k, v in self.headers.items()},
            "body": body,
        })

    do_GET = do_POST = do_PUT = do_DELETE = do_PATCH = _handle

    def log_message(self, fmt, *args):
        sys.stderr.write("echo %s\n" % (fmt % args))


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 18080
    srv = ThreadingHTTPServer(("127.0.0.1", port), Echo)
    print(f"echo server on 127.0.0.1:{port}", flush=True)
    srv.serve_forever()
