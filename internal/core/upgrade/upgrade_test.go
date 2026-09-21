package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// 平台映射：npm os-arch 约定（windows → win32）
func TestPlatformKey(t *testing.T) {
	cases := []struct {
		goos, goarch, want string
	}{
		{"darwin", "arm64", "darwin-arm64"},
		{"darwin", "amd64", "darwin-x64"},
		{"linux", "amd64", "linux-x64"},
		{"linux", "arm64", "linux-arm64"},
		{"windows", "amd64", "win32-x64"},
	}
	for _, c := range cases {
		got, err := PlatformKey(c.goos, c.goarch)
		if err != nil || got != c.want {
			t.Errorf("PlatformKey(%s,%s) = %q,%v; want %q", c.goos, c.goarch, got, err, c.want)
		}
	}
	if _, err := PlatformKey("freebsd", "amd64"); err == nil {
		t.Error("不支持的平台应报错")
	}
}

func TestIsNewer(t *testing.T) {
	cases := []struct {
		candidate, current string
		want               bool
	}{
		{"v0.3.0", "0.2.10", true},
		{"0.10.0", "0.9.0", true},
		{"0.2.0", "0.2.0", false},
		{"0.2.0", "0.3.0", false},
		{"v0.3.0-4-gabc", "0.2.0", true}, // 开发构建后缀容忍
		{"dev", "0.2.0", false},          // 非 semver 绝不误报
		{"", "0.2.0", false},
	}
	for _, c := range cases {
		if got := IsNewer(c.candidate, c.current); got != c.want {
			t.Errorf("IsNewer(%q,%q) = %v; want %v", c.candidate, c.current, got, c.want)
		}
	}
}

// fixture：npm 平台包 tarball（package/bin/remin）
func buildTarball(t *testing.T, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	name := "package/bin/remin"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	tw.Write([]byte(content))
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// fake registry：latest 元数据 → 平台包版本元数据 → tarball
func fakeRegistry(t *testing.T, latest, tarballVer string, tarball []byte, integrity string) *httptest.Server {
	t.Helper()
	base := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/@reminmem%2Fremin/latest", "/@reminmem/remin/latest":
			json.NewEncoder(w).Encode(map[string]any{"version": latest})
		case "/@reminmem%2Fremin-" + runtimePlatform(t) + "/" + tarballVer,
			"/@reminmem/remin-" + runtimePlatform(t) + "/" + tarballVer:
			json.NewEncoder(w).Encode(map[string]any{"dist": map[string]any{
				"tarball":   base + "/tgz",
				"integrity": integrity,
			}})
		case "/tgz":
			w.Write(tarball)
		default:
			t.Errorf("意外请求: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	base = srv.URL
	return srv
}

func runtimePlatform(t *testing.T) string {
	t.Helper()
	p, err := PlatformKey(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func integrityOf(data []byte) string {
	h := sha512.Sum512(data)
	return "sha512-" + base64.StdEncoding.EncodeToString(h[:])
}

func writeStagedBin(t *testing.T, root, content string) string {
	t.Helper()
	p := filepath.Join(root, "bin", "remin")
	if runtime.GOOS == "windows" {
		p += ".exe"
	}
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunUpgrades(t *testing.T) {
	tarball := buildTarball(t, "new-binary-bytes")
	srv := fakeRegistry(t, "9.9.9", "9.9.9", tarball, integrityOf(tarball))
	defer srv.Close()

	root := t.TempDir()
	staged := writeStagedBin(t, root, "old-binary-bytes")

	res, err := Run(Options{Root: root, Registry: srv.URL, CurrentVersion: "0.2.0"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Updated || res.Latest != "9.9.9" {
		t.Fatalf("应完成升级: %+v", res)
	}
	data, _ := os.ReadFile(staged)
	if string(data) != "new-binary-bytes" {
		t.Fatalf("落位二进制应被替换: %q", data)
	}
	if fi, _ := os.Stat(staged); fi.Mode()&0o111 == 0 {
		t.Error("替换后必须保持可执行位")
	}
	// 不留 tmp 残骸
	m, _ := filepath.Glob(filepath.Join(root, "bin", "*"))
	if len(m) != 1 {
		t.Errorf("bin 目录应只有落位文件: %v", m)
	}
}

// 校验不符：拒绝替换，落位原样（供应链底线）
func TestRunTampered(t *testing.T) {
	tarball := buildTarball(t, "evil")
	bad := "sha512-" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0}, 64))
	srv := fakeRegistry(t, "9.9.9", "9.9.9", tarball, bad)
	defer srv.Close()

	root := t.TempDir()
	staged := writeStagedBin(t, root, "old-binary-bytes")
	if _, err := Run(Options{Root: root, Registry: srv.URL, CurrentVersion: "0.2.0"}); err == nil {
		t.Fatal("完整性校验失败必须报错")
	}
	data, _ := os.ReadFile(staged)
	if string(data) != "old-binary-bytes" {
		t.Fatal("校验失败绝不允许动落位文件")
	}
}

func TestRunUpToDate(t *testing.T) {
	srv := fakeRegistry(t, "0.1.0", "0.1.0", buildTarball(t, "x"), integrityOf(buildTarball(t, "x")))
	defer srv.Close()
	root := t.TempDir()
	writeStagedBin(t, root, "old")
	res, err := Run(Options{Root: root, Registry: srv.URL, CurrentVersion: "0.2.0"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated {
		t.Fatal("旧版本不应触发替换")
	}
}

// 无落位：upgrade 管辖的是落位真身；渠道副本不是它的事
func TestRunRequiresStaged(t *testing.T) {
	root := t.TempDir()
	if _, err := Run(Options{Root: root, Registry: "http://127.0.0.1:1", CurrentVersion: "0.2.0"}); err == nil {
		t.Fatal("无落位应报错并指引用户先 doctor --install")
	}
}

func TestCheckLatest(t *testing.T) {
	srv := fakeRegistry(t, "1.2.3", "1.2.3", nil, "")
	defer srv.Close()
	got, err := CheckLatest(srv.URL)
	if err != nil || got != "1.2.3" {
		t.Fatalf("CheckLatest = %q,%v", got, err)
	}
}

// 版本提示缓存：24h TTL，失败静默（hook 面/版本面永不因升级检查打扰用户）
func TestCachedLatest(t *testing.T) {
	root := t.TempDir()
	srv := fakeRegistry(t, "5.0.0", "5.0.0", nil, "")
	defer srv.Close()

	got, err := CachedLatest(root, srv.URL)
	if err != nil || got != "5.0.0" {
		t.Fatalf("首次应拉取: %q,%v", got, err)
	}
	// 缓存生效：关掉 registry 仍能拿到（读缓存）
	srv.Close()
	got2, err := CachedLatest(root, srv.URL)
	if err != nil || got2 != "5.0.0" {
		t.Fatalf("TTL 内应读缓存: %q,%v", got2, err)
	}
}
