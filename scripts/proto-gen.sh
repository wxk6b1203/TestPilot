#!/usr/bin/env bash
# TestPilot proto 代码生成（Go + Python gRPC）。
#
# 与 README「代码生成」命令一致，统一入口便于本地复现与 CI 校验：
#   scripts/proto-gen.sh            # 生成 Go 与 Python 代码
#   scripts/proto-check.sh          # 生成 + buf lint/breaking + git diff 检查
#
# 可覆盖的环境变量：
#   PROTOC          protoc 路径（默认 PATH 中的 protoc，要求 v35.x）
#   PYTHON          Python 解释器（默认 worker/venv/bin/python，其次 python3）
#   GOBIN           存放 protoc-gen-go / protoc-gen-go-grpc 的目录
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PROTOC="${PROTOC:-protoc}"
PYTHON="${PYTHON:-}"

if ! command -v "$PROTOC" >/dev/null 2>&1 && ! command -v "${PROTOC##*/}" >/dev/null 2>&1; then
  echo "protoc not found: $PROTOC" >&2
  exit 1
fi

# 找到 protoc-gen-go / protoc-gen-go-grpc。CI 会把 GOBIN 放入 PATH；
# 本地常见安装位置为 go env GOPATH/bin。
if ! command -v protoc-gen-go >/dev/null 2>&1; then
  for d in "${GOBIN:-}" "$(go env GOPATH 2>/dev/null)/bin" "$HOME/go/bin"; do
    if [ -x "$d/protoc-gen-go" ]; then
      export PATH="$PATH:$d"
      break
    fi
  done
fi
if ! command -v protoc-gen-go >/dev/null 2>&1; then
  echo "protoc-gen-go not found; install with:" >&2
  echo "  go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.28.1" >&2
  exit 1
fi
if ! command -v protoc-gen-go-grpc >/dev/null 2>&1; then
  echo "protoc-gen-go-grpc not found; install with:" >&2
  echo "  go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.2.0" >&2
  exit 1
fi

if [ -z "$PYTHON" ]; then
  if [ -x "$ROOT/worker/venv/bin/python" ]; then
    PYTHON="$ROOT/worker/venv/bin/python"
  else
    PYTHON=python3
  fi
fi
"$PYTHON" -c 'import grpc_tools' 2>/dev/null || {
  echo "grpcio-tools missing for $PYTHON; install with: pip install grpcio-tools==1.83.0 protobuf==7.35.1" >&2
  exit 1
}

PROTO_FILES=(
  "$ROOT/proto/testpilot/common/v1/types.proto"
  "$ROOT/proto/testpilot/worker/v1/worker.proto"
  "$ROOT/proto/testpilot/copilot/v1/copilot.proto"
)

echo "→ Go codegen"
"$PROTOC" -I "$ROOT/proto" \
  --go_out="$ROOT/scheduler/gen" --go_opt=module=github.com/testpilot/testpilot/gen \
  --go-grpc_out="$ROOT/scheduler/gen" --go-grpc_opt=module=github.com/testpilot/testpilot/gen \
  "${PROTO_FILES[@]}"

echo "→ Python codegen"
"$PYTHON" -m grpc_tools.protoc -I "$ROOT/proto" \
  --python_out="$ROOT/worker/src" --pyi_out="$ROOT/worker/src" \
  --grpc_python_out="$ROOT/worker/src" \
  "${PROTO_FILES[@]}"

echo "→ grounding"
"$PYTHON" "$ROOT/scripts/gen_grounding.py"

echo "✓ proto codegen done"
