#!/usr/bin/env bash
# P1-N1 宪法检查：核心层零 agent SDK 依赖（白名单制——架构 §12）。
# 两档白名单：
#   核心四包（core/store/index/search）：仅 yaml + golang.org/x 标准库延伸；
#   internal/cli（协议层，ADR-0005 cobra 是认可选型）：另允 cobra/pflag。
# 表外即违规——黑名单挡不住新出的 agent SDK，白名单才挡得住。
set -euo pipefail
cd "$(dirname "$0")/.."

status=0
DOMAIN_RE='^[a-z0-9.-]+\.[a-z]{2,}/'
SELF_RE='^github\.com/remin-dev/remin'
CORE_ALLOW='^golang\.org/x/(sys|mod|skeleton)($|/)|^gopkg\.in/yaml\.v3$'
CLI_ALLOW="${CORE_ALLOW}|^github\.com/spf13/(cobra|pflag)$"

core_ext=$(go list -deps ./internal/core/... ./internal/store/... ./internal/index/... ./internal/search/... 2>/dev/null \
  | grep -E "$DOMAIN_RE" | grep -Ev "$SELF_RE" | sort -u || true)
core_viol=$(echo "$core_ext" | grep -Ev "$CORE_ALLOW" || true)
if [ -n "$core_viol" ]; then
  echo "VIOLATION: 核心四包出现白名单外的外部依赖："
  echo "$core_viol"
  status=1
fi

cli_ext=$(go list -deps ./internal/cli/... 2>/dev/null \
  | grep -E "$DOMAIN_RE" | grep -Ev "$SELF_RE" | sort -u || true)
cli_viol=$(echo "$cli_ext" | grep -Ev "$CLI_ALLOW" || true)
if [ -n "$cli_viol" ]; then
  echo "VIOLATION: internal/cli 出现白名单外的外部依赖："
  echo "$cli_viol"
  status=1
fi

if grep -rE '"github\.com/(modelcontextprotocol|anthropic|openai)' internal/core internal/store internal/index internal/search internal/cli --include='*.go' 2>/dev/null; then
  echo "VIOLATION: 核心源码出现 agent SDK import"
  status=1
fi

if [ $status -eq 0 ]; then
  echo "OK: 核心层外部依赖全部在分层白名单内"
  echo "  core/store/index/search: $(echo "$core_ext" | tr '\n' ' ')"
  echo "  cli: $(echo "$cli_ext" | tr '\n' ' ')"
fi
exit $status
