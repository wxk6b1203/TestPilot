#!/usr/bin/env bash
# TestPilot MVP 一键开发环境：Scheduler(:8080/:9090) + Worker + echo(:18080) + Vite(:5173)
# 用法：scripts/dev.sh [start|stop|status]

set -u
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DB="${TP_DB_PATH:-$ROOT/.data/testpilot.db}"
LOG_DIR="$ROOT/.data/logs"
PID_DIR="$ROOT/.data/pids"

mkdir -p "$LOG_DIR" "$PID_DIR" "$(dirname "$DB")"
# Worker 与 Scheduler 共享的产物目录（截图/trace/har）；生产换对象存储
export TP_ARTIFACT_DIR="$ROOT/.data/artifacts"
mkdir -p "$TP_ARTIFACT_DIR"

_start() {
  local name="$1"; shift
  if [ -f "$PID_DIR/$name.pid" ] && kill -0 "$(cat "$PID_DIR/$name.pid")" 2>/dev/null; then
    echo "✓ $name 已在运行 (pid $(cat "$PID_DIR/$name.pid"))"
    return 0
  fi
  nohup "$@" > "$LOG_DIR/$name.log" 2>&1 &
  echo $! > "$PID_DIR/$name.pid"
  echo "▶ $name 启动 (pid $!，日志 .data/logs/$name.log)"
}

start() {
  echo "== TestPilot dev 环境 =="
  _start echo python3 "$ROOT/scripts/echo_server.py" 18080
  _start grpc-echo sh -c "cd '$ROOT/worker' && PYTHONPATH=src exec venv/bin/python '$ROOT/scripts/grpc_echo_server.py' 19090"
  sleep 0.5
  # 构建到稳定路径再运行（不用 go run）：① pid 文件指向真实监听进程，stop 不留孤儿；
  # ② 配合 127.0.0.1 回环绑定，避免 macOS 应用防火墙对每次新构建的未签名二进制弹
  #   “接受传入连接”审批（loopback 监听不触发 ALF）。
  mkdir -p "$ROOT/.data/bin"
  (cd "$ROOT/scheduler" && go build -o "$ROOT/.data/bin/scheduler" ./cmd/scheduler) || {
    echo "✗ scheduler 构建失败"; return 1;
  }
  _start scheduler env TP_DB_PATH="$DB" TP_HTTP_ADDR=127.0.0.1:8080 TP_GRPC_ADDR=127.0.0.1:9090 \
    TP_STATIC_DIR="$ROOT/web/dist" "$ROOT/.data/bin/scheduler"
  sleep 2
  (cd "$ROOT/worker" && PYTHONPATH=src exec venv/bin/python -m testpilot_worker \
      --scheduler 127.0.0.1:9090 --capabilities functional,lowcode,playwright,stress --tags region=local) > "$LOG_DIR/worker.log" 2>&1 &
  echo $! > "$PID_DIR/worker.pid"
  echo "▶ worker 启动 (pid $!，日志 .data/logs/worker.log)"
  _start copilot "$ROOT/copilot/venv/bin/python" -m testpilot_copilot.main
  _start vite pnpm --dir "$ROOT/web" dev
  sleep 2
  echo ""
  echo "控制台:      http://localhost:5173  (dev，代理到 :8080)"
  echo "控制台(托管): http://localhost:8080  (需先 pnpm --dir web build)"
  echo "REST API:    http://localhost:8080/api/v1  (admin / admin123)"
  echo "Copilot:     http://localhost:8100/api  (聊天页 /copilot)"
  echo "gRPC:        127.0.0.1:9090"
  echo "echo 服务:   http://127.0.0.1:18080"
  echo "E2E 验证:    worker/venv/bin/python scripts/e2e.py"
}

stop() {
  for f in "$PID_DIR"/*.pid; do
    [ -f "$f" ] || continue
    pid="$(cat "$f")"
    name="$(basename "$f" .pid)"
    if kill "$pid" 2>/dev/null; then
      echo "■ $name 已停止 (pid $pid)"
    fi
    rm -f "$f"
  done
  # go run 的子进程兜底
  pkill -f 'exe/scheduler' 2>/dev/null || true
  pkill -f testpilot_worker 2>/dev/null || true
  pkill -f testpilot_copilot 2>/dev/null || true
  pkill -f echo_server.py 2>/dev/null || true
  # 端口兜底：pid 文件丢失/陈旧时按监听端口清场
  lsof -ti:8080 -ti:9090 -ti:8100 2>/dev/null | xargs kill 2>/dev/null || true
  sleep 0.5
  lsof -ti:8080 -ti:9090 -ti:8100 2>/dev/null | xargs kill -9 2>/dev/null || true
}

status() {
  for f in "$PID_DIR"/*.pid; do
    [ -f "$f" ] || { echo "（无运行中的组件）"; break; }
    pid="$(cat "$f")"
    name="$(basename "$f" .pid)"
    if kill -0 "$pid" 2>/dev/null; then
      echo "✓ $name (pid $pid)"
    else
      echo "✗ $name (已退出)"
    fi
  done
  curl -s --max-time 2 http://localhost:8080/healthz 2>/dev/null && echo "" || true
}

case "${1:-start}" in
  start) stop > /dev/null 2>&1; start ;;
  stop) stop ;;
  status) status ;;
  *) echo "用法: $0 [start|stop|status]"; exit 1 ;;
esac
