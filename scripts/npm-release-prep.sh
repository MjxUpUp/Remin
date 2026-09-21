#!/usr/bin/env bash
# 组装 npm 发布目录（本地验证与 CI 发布共用；发布本身只发生在 GitHub Actions，
# 绝不从本机 publish）。
# 用法: npm-release-prep.sh <version> <binaries-dir> [out-dir]
#   binaries-dir 布局: <platform>/remin[.exe]（platform ∈ npm os-arch 命名）
#   产物: out/main（主包目录）+ out/<platform>（平台包目录 ×5）
set -euo pipefail
cd "$(dirname "$0")/.."

ver="${1:?用法: npm-release-prep.sh <version> <binaries-dir> [out-dir]}"
bindir="${2:?缺少 binaries-dir}"
out="${3:-npm/dist}"
[[ "$ver" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "版本号必须为 X.Y.Z: $ver"; exit 1; }
bindir="$(cd "$bindir" && pwd)"
rm -rf "$out"
mkdir -p "$out"

os_of() { case "$1" in darwin-*) echo darwin;; linux-*) echo linux;; win32-*) echo win32;; esac; }
arch_of() { case "$1" in *-arm64) echo arm64;; *-x64) echo x64;; esac; }

find_bin() { # 布局兼容：本地验证 <bindir>/<plat>/；CI download-artifact 产出 <bindir>/bin-<plat>/
  local plat="$1" src
  for prefix in "$plat" "bin-$plat"; do
    for exe in remin remin.exe; do
      src="$bindir/$prefix/$exe"
      [[ -f "$src" ]] && { echo "$src"; return 0; }
    done
  done
  return 1
}

for plat in darwin-arm64 darwin-x64 linux-arm64 linux-x64 win32-x64; do
  src="$(find_bin "$plat")" || { echo "缺少二进制: $bindir/$plat/remin（及 bin-$plat/ 布局）"; exit 1; }
  pkgdir="$out/$plat"
  mkdir -p "$pkgdir/bin"
  cp "$src" "$pkgdir/bin/"
  cat > "$pkgdir/package.json" <<EOF
{
  "name": "@reminmem/remin-$plat",
  "version": "$ver",
  "description": "Remin binary for $plat",
  "license": "MIT",
  "os": ["$(os_of "$plat")"],
  "cpu": ["$(arch_of "$plat")"],
  "files": ["bin"]
}
EOF
done

# 主包：版本与 optionalDependencies 钉到本次版本
maindir="$out/main"
mkdir -p "$maindir/bin"
cp npm/main/bin/remin.js "$maindir/bin/"
cp npm/main/README.md "$maindir/"
node -e '
const fs = require("fs");
const pkg = JSON.parse(fs.readFileSync("npm/main/package.json", "utf8"));
const ver = process.argv[1];
pkg.version = ver;
for (const k of Object.keys(pkg.optionalDependencies)) pkg.optionalDependencies[k] = ver;
fs.writeFileSync(process.argv[2] + "/package.json", JSON.stringify(pkg, null, 2) + "\n");
' "$ver" "$maindir"

echo "npm 发布目录已组装: ${out}（版本 ${ver}）"
find "$out" -name package.json | sort
