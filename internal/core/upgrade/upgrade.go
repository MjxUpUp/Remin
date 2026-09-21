// Package upgrade 自更新：查 npm registry（@reminmem）→ 下载平台包 tarball →
// 完整性校验 → 原子替换落位二进制（<root>/bin/remin）。渠道副本（npm 全局目录/
// brew）永不参与运行时——upgrade 只管辖落位真身，落位不存在时指引 doctor --install。
// 依赖约束：纯标准库（宪法核心白名单）。
package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultRegistry 官方源；REMIN_NPM_REGISTRY 可覆盖（镜像/私有源）
	DefaultRegistry = "https://registry.npmjs.org"
	// MainPackage npm 主包（薄壳 launcher）
	MainPackage = "@reminmem/remin"
	// checkTTL 版本提示缓存有效期
	checkTTL = 24 * time.Hour
	// maxTarball tarball 大小上限（供应链防御：拒绝异常大包）
	maxTarball = 128 << 20
)

// Registry 解析生效 registry（env > 默认）
func Registry() string {
	if v := os.Getenv("REMIN_NPM_REGISTRY"); v != "" {
		return v
	}
	return DefaultRegistry
}

// PlatformKey npm os-arch 命名（windows → win32；amd64 → x64）
func PlatformKey(goos, goarch string) (string, error) {
	var osName string
	switch goos {
	case "darwin":
		osName = "darwin"
	case "linux":
		osName = "linux"
	case "windows":
		osName = "win32"
	default:
		return "", fmt.Errorf("不支持的平台 %s/%s（支持 darwin/linux/windows）", goos, goarch)
	}
	var arch string
	switch goarch {
	case "amd64":
		arch = "x64"
	case "arm64":
		arch = "arm64"
	default:
		return "", fmt.Errorf("不支持的架构 %s/%s（支持 amd64/arm64）", goos, goarch)
	}
	return osName + "-" + arch, nil
}

// ── semver ───────────────────────────────────────────────────────────────────

// parseSemver 宽容解析：容忍 v 前缀与 - prerelease/+ build 后缀；解析失败如实返回 false
func parseSemver(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || (len(p) > 1 && p[0] == '0') {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// IsNewer candidate 是否严格新于 current（任一侧非 semver → false，绝不误报）
func IsNewer(candidate, current string) bool {
	c, ok1 := parseSemver(candidate)
	v, ok2 := parseSemver(current)
	if !ok1 || !ok2 {
		return false
	}
	for i := range c {
		if c[i] != v[i] {
			return c[i] > v[i]
		}
	}
	return false
}

// ── registry 访问 ────────────────────────────────────────────────────────────

func httpGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxTarball))
}

// CheckLatest 主包 latest 版本
func CheckLatest(registry string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	data, err := httpGet(ctx, registry+"/@reminmem%2Fremin/latest")
	if err != nil {
		return "", err
	}
	var meta struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &meta); err != nil || meta.Version == "" {
		return "", fmt.Errorf("registry 元数据异常（@reminmem/remin latest 无 version）")
	}
	return meta.Version, nil
}

// fetchPlatformDist 平台包指定版本的下载地址与完整性哈希
func fetchPlatformDist(ctx context.Context, registry, platform, version string) (tarball, integrity string, err error) {
	data, err := httpGet(ctx, fmt.Sprintf("%s/@reminmem%%2Fremin-%s/%s", registry, platform, version))
	if err != nil {
		return "", "", err
	}
	var meta struct {
		Dist struct {
			Tarball   string `json:"tarball"`
			Integrity string `json:"integrity"`
		} `json:"dist"`
	}
	if err := json.Unmarshal(data, &meta); err != nil || meta.Dist.Tarball == "" {
		return "", "", fmt.Errorf("registry 元数据异常（@reminmem/remin-%s@%s 无 dist.tarball）", platform, version)
	}
	return meta.Dist.Tarball, meta.Dist.Integrity, nil
}

// verifyIntegrity npm integrity（sha512-BASE64）校验；无哈希 → 拒绝安装
func verifyIntegrity(data []byte, integrity string) error {
	if s, ok := strings.CutPrefix(integrity, "sha512-"); ok {
		h := sha512.Sum512(data)
		want, err := base64.StdEncoding.DecodeString(s)
		if err != nil || !bytes.Equal(h[:], want) {
			return fmt.Errorf("完整性校验失败（sha512 不符）——拒绝安装")
		}
		return nil
	}
	return fmt.Errorf("registry 元数据缺少可校验的完整性哈希——拒绝安装")
}

// extractBinary 从 npm tarball（package/ 前缀）提取 bin/remin(.exe)
func extractBinary(tarball []byte, exeName string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		return nil, fmt.Errorf("tarball 解压失败: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	want := "package/bin/" + exeName
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("tarball 内未找到 %s", want)
		}
		if err != nil {
			return nil, err
		}
		if filepath.ToSlash(filepath.Clean(hdr.Name)) == want {
			return io.ReadAll(io.LimitReader(tr, maxTarball))
		}
	}
}

// AtomicReplace 原子替换 dst：tmp 写入 → chmod 0755 → rename（运行中进程持旧
// inode 不受影响；Windows 目标被占用时先挪走旧文件）。
func AtomicReplace(dst string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".remin-new-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		_ = os.Rename(dst, dst+".old")
		if err2 := os.Rename(tmp.Name(), dst); err2 != nil {
			return err2
		}
		_ = os.Remove(dst + ".old")
	}
	return nil
}

// ── 升级主流程 ────────────────────────────────────────────────────────────────

// Options 升级参数（CurrentVersion 由 CLI 层注入——core 不反向依赖 cli）
type Options struct {
	Root           string
	Registry       string
	CurrentVersion string
}

// Result 升级结果
type Result struct {
	Current string `json:"current"`
	Latest  string `json:"latest"`
	Updated bool   `json:"updated"`
	Target  string `json:"target,omitempty"`
}

func exeName() string {
	if runtime.GOOS == "windows" {
		return "remin.exe"
	}
	return "remin"
}

// Run 检查并升级落位二进制；无更新时静默返回（Updated=false）
func Run(opts Options) (*Result, error) {
	plat, err := PlatformKey(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return nil, err
	}
	latest, err := CheckLatest(opts.Registry)
	if err != nil {
		return nil, fmt.Errorf("查询最新版本失败: %w", err)
	}
	res := &Result{Current: opts.CurrentVersion, Latest: latest}
	if !IsNewer(latest, opts.CurrentVersion) {
		return res, nil
	}
	stable := filepath.Join(opts.Root, "bin", exeName())
	if _, err := os.Stat(stable); err != nil {
		return res, fmt.Errorf("落位二进制不存在（%s）——先运行 remin doctor --install 落位；渠道内更新走渠道（如 npm update -g）", stable)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	url, integrity, err := fetchPlatformDist(ctx, opts.Registry, plat, latest)
	if err != nil {
		return res, fmt.Errorf("获取平台包元数据失败: %w", err)
	}
	data, err := httpGet(ctx, url)
	if err != nil {
		return res, fmt.Errorf("下载平台包失败: %w", err)
	}
	if err := verifyIntegrity(data, integrity); err != nil {
		return res, err
	}
	bin, err := extractBinary(data, exeName())
	if err != nil {
		return res, err
	}
	if err := AtomicReplace(stable, bin); err != nil {
		return res, fmt.Errorf("替换落位二进制失败: %w", err)
	}
	res.Updated = true
	res.Target = stable
	return res, nil
}

// ── 版本提示缓存（version/doctor 人面轻提示用；失败静默）────────────────────

type checkCache struct {
	TS     time.Time `json:"ts"`
	Latest string    `json:"latest"`
}

func cachePath(root string) string {
	return filepath.Join(root, "transcripts-cache", "upgrade-check.json")
}

// CachedLatest 带缓存的版本查询：TTL 内读缓存；拉取失败回退陈旧缓存；再不行报错。
// 调用方（version/doctor 提示路径）应忽略错误——升级检查永不构成干扰。
func CachedLatest(root, registry string) (string, error) {
	path := cachePath(root)
	if data, err := os.ReadFile(path); err == nil {
		var c checkCache
		if json.Unmarshal(data, &c) == nil && time.Since(c.TS) < checkTTL {
			return c.Latest, nil
		}
	}
	latest, err := CheckLatest(registry)
	if err != nil {
		// 拉取失败：陈旧缓存好过没有
		if data, rerr := os.ReadFile(path); rerr == nil {
			var c checkCache
			if json.Unmarshal(data, &c) == nil && c.Latest != "" {
				return c.Latest, nil
			}
		}
		return "", err
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	c := checkCache{TS: time.Now(), Latest: latest}
	if data, err := json.Marshal(c); err == nil {
		_ = os.WriteFile(path, append(data, '\n'), 0o644)
	}
	return latest, nil
}
