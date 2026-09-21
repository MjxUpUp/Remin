package eval

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var testBin string

// 宪法测试四件套 + budget（make constitution 入口；规则可判定、模型无关）。
// parity 套件需要真实二进制实跑 MCP stdio 通道——与产品（CLI 传 os.Executable）
// 同一通道，测试经 go build 产出。
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "remin-eval-bin")
	if err != nil {
		os.Exit(1)
	}
	testBin = filepath.Join(dir, "remin")
	if out, err := exec.Command("go", "build", "-o", testBin, "github.com/remin-dev/remin/cmd/remin").CombinedOutput(); err != nil {
		os.Stderr.Write(out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func TestConstitutionSuites(t *testing.T) {
	for _, suite := range []string{"trust", "roundtrip", "parity", "conflict", "budget"} {
		reps, err := Run(suite, testBin)
		if err != nil {
			t.Fatalf("%s: %v", suite, err)
		}
		rep := reps[0]
		if !rep.Passed {
			for _, c := range rep.Checks {
				if !c.Passed {
					t.Errorf("[%s] %s: %s", suite, c.Name, c.Detail)
				}
			}
		}
	}
}

// parity 无二进制时必须如实失败（不允许静默降级为引擎内对比）
func TestParityRequiresBinary(t *testing.T) {
	rep := SuiteParity("")
	if rep.Passed {
		t.Fatal("无二进制时 parity 应失败而非静默通过")
	}
}
