#!/usr/bin/env bash
# P1-N2 宪法检查：适配器预算。全部适配器代码 ≤ 总代码 20%，单一 ≤8%。
# 当前架构为零适配器代码（doctor 写的是直调 remin CLI 的全局配置），此脚本
# 在未来出现 adapters/ 代码时持续把关。
set -euo pipefail
cd "$(dirname "$0")/.."

total=$(find . -name '*.go' -not -path './vendor/*' -not -path './.git/*' | xargs wc -l | tail -1 | awk '{print $1}')
adapter_total=$(find adapters -name '*.go' 2>/dev/null | xargs wc -l 2>/dev/null | tail -1 | awk '{print $1}')
adapter_total=${adapter_total:-0}

if [ "$total" -eq 0 ]; then echo "OK（无代码）"; exit 0; fi
pct=$(( adapter_total * 100 / total ))
echo "适配器代码: ${adapter_total}/${total} 行 (${pct}%)"
if [ "$pct" -gt 20 ]; then
  echo "VIOLATION: 适配器总预算超 20%，触发架构评审"
  exit 1
fi

for d in adapters/*/; do
  [ -d "$d" ] || continue
  n=$(find "$d" -name '*.go' | xargs wc -l 2>/dev/null | tail -1 | awk '{print $1}')
  n=${n:-0}
  p=$(( n * 100 / total ))
  echo "  $d: ${n} 行 (${p}%)"
  if [ "$p" -gt 8 ]; then
    echo "VIOLATION: 单一适配器 $d 超 8% 预算"
    exit 1
  fi
done
echo "OK: 适配器预算合规"
