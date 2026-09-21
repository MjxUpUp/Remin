#!/usr/bin/env bash
# P1-N1 宪法检查：核心层零 agent SDK 依赖。
# 核心包（core/store/index/search/cli）不允许依赖任何 agent 专属 SDK；
# MCP 官方 SDK 仅允许出现在 internal/protocol（ADR-0004）。
set -euo pipefail
cd "$(dirname "$0")/.."

CORE_PKGS="./internal/core/... ./internal/store/... ./internal/index/... ./internal/search/... ./internal/cli/..."
FORBIDDEN='modelcontextprotocol|anthropic|openai|langchain|claudesdk|codex-sdk|dendrite'

status=0
deps=$(go list -deps $CORE_PKGS 2>/dev/null || { echo "FAIL: go list 解析核心包失败"; exit 1; })
if echo "$deps" | grep -Ev '^github.com/remin-dev/remin' | grep -Eq "$FORBIDDEN"; then
  echo "VIOLATION: 核心包依赖了 agent SDK："
  echo "$deps" | grep -Eq "$FORBIDDEN" && echo "$deps" | grep -E "$FORBIDDEN" | head
  status=1
fi
if grep -rEn "\"github.com/($FORBIDDEN)" internal/core internal/store internal/index internal/search internal/cli --include='*.go' 2>/dev/null; then
  echo "VIOLATION: 核心源码出现 agent SDK import"
  status=1
fi

if [ $status -eq 0 ]; then echo "OK: 核心层零 agent SDK 依赖"; fi
exit $status
