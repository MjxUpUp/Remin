package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/remin-dev/remin/internal/core/config"
	"github.com/remin-dev/remin/internal/testutil"
)

// bridge CLI 接线契约：pull 走配置端点 → importer 通道（dry-run 不写/apply 写批次）；
// push dry-run 不触网；--from/--to 校验。

func fakeNotion(t *testing.T) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/v1/blocks/parent1/children"):
			io.WriteString(w, `{"results":[{"id":"p1","type":"child_page","child_page":{"title":"部署手册"}}],"has_more":false}`)
		case strings.HasSuffix(r.URL.Path, "/v1/blocks/p1/children"):
			io.WriteString(w, `{"results":[{"type":"paragraph","paragraph":{"rich_text":[{"text":{"content":"先跑迁移再发布"}}]}}],"has_more":false}`)
		default:
			io.WriteString(w, `{"id":"newp","url":"https://notion.so/newp"}`)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func writeBridgeConfig(t *testing.T, root, base string) {
	t.Helper()
	cfg, err := config.Load(filepath.Join(root, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Bridge = &config.BridgeConfig{Notion: &config.BridgeNotionConfig{Base: base, ParentPageID: "parent1"}}
	if err := cfg.Save(filepath.Join(root, "config.yaml")); err != nil {
		t.Fatal(err)
	}
}

func runCapture(t *testing.T, args ...string) (string, error) {
	t.Helper()
	rootCmd.SetArgs(args)
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	err := rootCmd.Execute()
	os.Stdout = old
	w.Close()
	data, _ := io.ReadAll(r)
	return string(data), err
}

func TestBridgePullDryRunAndApply(t *testing.T) {
	st := testutil.NewStore(t)
	srv := fakeNotion(t)
	writeBridgeConfig(t, st.Root, srv.URL)
	t.Setenv("REMIN_NOTION_TOKEN", "t")

	out, err := runCapture(t, "bridge", "pull", "--from", "notion", "--json", "--root", st.Root)
	if err != nil {
		t.Fatalf("pull dry-run 不应失败: %v", err)
	}
	var rep struct {
		Data struct {
			Applied bool `json:"applied"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("json 包络: %v\n%s", err, out)
	}
	if rep.Data.Applied {
		t.Fatal("dry-run 不应 apply")
	}

	if _, err := runCapture(t, "bridge", "pull", "--from", "notion", "--apply", "--root", st.Root); err != nil {
		t.Fatalf("apply 不应失败: %v", err)
	}
	out, err = runCapture(t, "inbox", "--json", "--root", st.Root)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Data struct {
			Batches []struct {
				ID     string `json:"id"`
				Source string `json:"source"`
			} `json:"batches"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, b := range env.Data.Batches {
		if strings.Contains(b.Source, "import") {
			found = true
		}
	}
	if !found {
		t.Fatalf("apply 后应有 import 批次: %s", out)
	}
}

func TestBridgePushDryRunNoNetwork(t *testing.T) {
	st := testutil.NewStore(t)
	// 不配 bridge、不设 token：dry-run 仍应成功（不触网）
	rootCmd.SetArgs([]string{"bridge", "push", "--to", "notion", "--dry-run", "--root", st.Root})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("dry-run 不应触网失败: %v", err)
	}
}

func TestBridgePullBadSource(t *testing.T) {
	st := testutil.NewStore(t)
	rootCmd.SetArgs([]string{"bridge", "pull", "--from", "dropbox", "--root", st.Root})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("未知来源应报错")
	}
}
