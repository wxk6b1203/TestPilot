#!/usr/bin/env bash
# TestPilot proto 治理：lint + breaking + codegen 复现性检查。
#
# 本地：
#   scripts/proto-check.sh
# CI pull_request（比对 base 分支，需 fetch-depth: 0）：
#   BUF_AGAINST=".git#ref=origin/$GITHUB_BASE_REF,subdir=proto" scripts/proto-check.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

BUF="${BUF:-}"
if [ -z "$BUF" ]; then
  BUF="$(command -v buf || true)"
fi
if [ -n "$BUF" ]; then
  echo "→ buf lint"
  BUF_CACHE_DIR="${BUF_CACHE_DIR:-$HOME/.cache/buf}" "$BUF" lint
  if [ -n "${BUF_AGAINST:-}" ]; then
    echo "→ buf breaking against ${BUF_AGAINST}"
    BUF_CACHE_DIR="${BUF_CACHE_DIR:-$HOME/.cache/buf}" "$BUF" breaking --against "$BUF_AGAINST"
  fi
else
  echo "! buf not installed; skip lint/breaking (CI 会强制安装)" >&2
fi

echo "→ regenerate and compare"
"$ROOT/scripts/proto-gen.sh"

if ! git diff --exit-code -- \
    buf.yaml \
    scheduler/gen \
    worker/src/testpilot \
    scheduler/internal/grpcserver/schema.json \
    copilot/src/testpilot_copilot/grounding; then
  echo "✗ proto 生成产物与提交内容不一致：请运行 scripts/proto-gen.sh 并提交结果" >&2
  exit 1
fi

echo "✓ proto check passed"
