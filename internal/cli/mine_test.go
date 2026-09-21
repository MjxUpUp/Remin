package cli

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/remin-dev/remin/internal/testutil"
)

// --deep 未配置 llm 节时显式报错（CLI flag 接线层；用户显式要求过深路径，不静默降级）
func TestMineDeepFlagFailsFastWithoutLLMConfig(t *testing.T) {
	st := testutil.NewStore(t)
	rootCmd.SetArgs([]string{"mine", "--deep", "--root", st.Root})
	errText := captureStderr(t, func() {
		if err := rootCmd.Execute(); err == nil {
			t.Error("未配置 llm 节时 --deep 应报错（fail 走 SilentExit）")
		}
	})
	if !strings.Contains(errText, "llm") {
		t.Errorf("报错应指向 llm 配置: %s", errText)
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	fn()
	os.Stderr = old
	w.Close()
	data, _ := io.ReadAll(r)
	return string(data)
}
