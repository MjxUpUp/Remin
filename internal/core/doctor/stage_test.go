package doctor

import (
	"os"
	"path/filepath"
	"testing"
)

// 落位：二进制复制到 <root>/bin/remin，接线永远指向稳定路径
func TestStageCopiesBinary(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(t.TempDir(), "remin")
	if err := os.WriteFile(src, []byte("fake-binary-bytes"), 0o755); err != nil {
		t.Fatal(err)
	}

	stable, err := Stage(root, src)
	if err != nil {
		t.Fatal(err)
	}
	if stable != filepath.Join(root, "bin", "remin") {
		t.Fatalf("稳定路径应为 <root>/bin/remin: %s", stable)
	}
	data, err := os.ReadFile(stable)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "fake-binary-bytes" {
		t.Fatal("落位内容应与源一致")
	}
	if fi, _ := os.Stat(stable); fi.Mode()&0o111 == 0 {
		t.Fatal("落位二进制必须可执行")
	}

	// 幂等：已在稳定路径 → 原样返回，不自我复制
	again, err := Stage(root, stable)
	if err != nil {
		t.Fatal(err)
	}
	if again != stable {
		t.Fatalf("应返回同一稳定路径: %s", again)
	}
}

// .gitignore 防扫入：add -A 提交路径存在，bin/ 与 wiring.json 必须被忽略并记账（可逆）
func TestStageGitignore(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, ".gitignore"), []byte("index/bm25-*\nviews/\n"), 0o644)
	src := filepath.Join(t.TempDir(), "remin")
	os.WriteFile(src, []byte("bin"), 0o755)

	if _, err := Stage(root, src); err != nil {
		t.Fatal(err)
	}
	gi, _ := os.ReadFile(filepath.Join(root, ".gitignore"))
	for _, line := range []string{"bin/", "wiring.json"} {
		if !containsLine(string(gi), line) {
			t.Errorf(".gitignore 应包含 %q: %q", line, gi)
		}
	}
	// 记账：gitignore 行是 effect，卸载时可逆
	l, _ := LoadLedger(root)
	found := false
	for _, e := range l.Effects {
		if e.Kind == "gitignore" {
			found = true
		}
	}
	if !found {
		t.Error("gitignore 追加必须记为 effect（cordis 可逆性）")
	}

	// 幂等：重复 Stage 不重复追加
	if _, err := Stage(root, src); err != nil {
		t.Fatal(err)
	}
	gi2, _ := os.ReadFile(filepath.Join(root, ".gitignore"))
	if countLine(string(gi2), "bin/") != 1 {
		t.Errorf("gitignore 追加应幂等: %q", gi2)
	}
}

// 无 .gitignore 且非 git 仓库（未 init 的 root）：不制造文件，等 init 写新默认值
func TestStageNoGitignorePreInit(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(t.TempDir(), "remin")
	os.WriteFile(src, []byte("bin"), 0o755)
	if _, err := Stage(root, src); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".gitignore")); !os.IsNotExist(err) {
		t.Error("未初始化的 root 不应制造 .gitignore")
	}
}

func containsLine(body, line string) bool { return countLine(body, line) >= 1 }

func countLine(body, line string) int {
	n := 0
	cur := ""
	for _, r := range body {
		if r == '\n' {
			if cur == line {
				n++
			}
			cur = ""
		} else {
			cur += string(r)
		}
	}
	if cur == line {
		n++
	}
	return n
}
