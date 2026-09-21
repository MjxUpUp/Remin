package importer

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/store"
	"github.com/remin-dev/remin/internal/testutil"
)

func TestMarkdownDirHumanVerified(t *testing.T) {
	st := testutil.NewStore(t)
	dir := t.TempDir()
	os.MkdirAll(dir, 2026)
	os.WriteFile(filepath.Join(dir, "note1.md"), []byte("# 标题\n\n回复用中文，代码注释用英文。"), 0o644)
	os.WriteFile(filepath.Join(dir, "note2.md"), []byte("部署前必须先跑迁移脚本"), 0o644)

	rep, err := Import(st, SrcMarkdownDir, dir, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.NewItems != 2 {
		t.Fatalf("应发现 2 条: %+v", rep)
	}
	// apply
	rep2, err := Import(st, SrcMarkdownDir, dir, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Batch == "" {
		t.Fatal("apply 应生成批次")
	}
	in := inbox.New(st)
	cands, _ := in.ListCandidates(rep2.Batch)
	if len(cands) != 2 {
		t.Fatalf("应 2 条候选: %d", len(cands))
	}
	for _, c := range cands {
		if c.Trust != store.TrustHumanVerified {
			t.Errorf("人写的字应 human-verified: %s", c.Trust)
		}
		if !strings.HasPrefix(c.Provenance.Origin, "用户亲笔") {
			t.Errorf("provenance 应注明用户亲笔: %+v", c.Provenance)
		}
		if c.CapturedAt == store.TimeUnknown {
			t.Errorf("文件 mtime 应还原 captured_at: %+v", c)
		}
	}
	// 幂等：再导 → 全跳过
	rep3, _ := Import(st, SrcMarkdownDir, dir, true, "")
	if rep3.NewItems != 0 || rep3.Skipped != 2 {
		t.Errorf("幂等应只报增量: %+v", rep3)
	}
}

func TestChatGPTExportBothShapes(t *testing.T) {
	st := testutil.NewStore(t)
	dir := t.TempDir()
	strJSON := filepath.Join(dir, "strings.json")
	os.WriteFile(strJSON, []byte(`["用户主力语言是 Rust","喜欢深色主题"]`), 0o644)
	rep, err := Import(st, SrcChatGPTExport, strJSON, false, "")
	if err != nil || rep.NewItems != 2 {
		t.Fatalf("string 数组形态: %+v err=%v", rep, err)
	}
	objJSON := filepath.Join(dir, "objects.json")
	os.WriteFile(objJSON, []byte(`[{"id":"m1","content":"住在上海","created_at":1758000000},{"content":"无 id 条目"}]`), 0o644)
	rep2, err := Import(st, SrcChatGPTExport, objJSON, false, "")
	if err != nil || rep2.NewItems != 2 {
		t.Fatalf("对象数组形态: %+v err=%v", rep2, err)
	}
}

func TestClaudeAutoMemorySplit(t *testing.T) {
	st := testutil.NewStore(t)
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "projects", "proj-x", "memory")
	os.MkdirAll(memDir, 0o755)
	os.WriteFile(filepath.Join(memDir, "MEMORY.md"), []byte(
		"## 偏好\n\n回复用中文。\n\n喜欢简洁的回答。\n\n## 工具\n\n构建用 make。"), 0o644)
	os.WriteFile(filepath.Join(memDir, "topic-deploy.md"), []byte("部署走 ArgoCD"), 0o644)

	rep, err := Import(st, SrcClaudeAutoMemory, "", false, home)
	if err != nil {
		t.Fatal(err)
	}
	// MEMORY.md 3 段 + 主题文件 1 条
	if rep.NewItems != 4 {
		t.Fatalf("应拆出 4 条: %+v", rep)
	}
}

func TestClaudeMemSQLite(t *testing.T) {
	st := testutil.NewStore(t)
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".claude-mem"), 0o755)
	db := filepath.Join(home, ".claude-mem", "memories.db")
	sql := `CREATE TABLE memories (id INTEGER PRIMARY KEY, content TEXT, created_at INTEGER);
	INSERT INTO memories (content, created_at) VALUES ('用户在写一个记忆产品', 1758000000);
	INSERT INTO memories (content, created_at) VALUES ('喜欢用 Neovim', 1758000100);`
	out, err := runSQLiteScript(db, sql)
	if err != nil {
		t.Skipf("fixture 建库失败（sqlite3 不可用？）: %v %s", err, out)
	}
	rep, err := Import(st, SrcClaudeMem, "", false, home)
	if err != nil {
		t.Fatal(err)
	}
	if rep.NewItems != 2 {
		t.Fatalf("应读出 2 条: %+v", rep)
	}
}

func TestCodexMemories(t *testing.T) {
	st := testutil.NewStore(t)
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".codex", "memories"), 0o755)
	os.WriteFile(filepath.Join(home, ".codex", "memories", "AGENTS.md"), []byte("构建命令是 pnpm build"), 0o644)
	rep, err := Import(st, SrcCodexMemories, "", false, home)
	if err != nil || rep.NewItems != 1 {
		t.Fatalf("codex 来源: %+v err=%v", rep, err)
	}
}

// 冲突建议：库内旧事实 + 导入更新事实（有时间证据）→ supersede 建议由人裁
func TestConflictSuggestion(t *testing.T) {
	st := testutil.NewStore(t)
	// 库内已有旧事实（直接写文件模拟已人审状态）
	old := &store.Memory{
		ID: "mem_OLD0000000000000000000000", Type: store.TypeSemantic, Facet: "dev",
		Status:     store.StatusActive,
		CapturedAt: "2026-01-01T00:00:00+08:00", ReviewedAt: "2026-01-02T00:00:00+08:00",
		Modified: "2026-01-02T00:00:00+08:00",
		Trust:    store.TrustHumanVerified, Source: store.SourceAgent,
		Provenance: store.Provenance{Origin: "t", Ref: "t", Quote: "主力数据库是 Mongo"},
		Version:    store.FormatVersion, Body: "主力数据库是 Mongo",
	}
	if err := st.SaveMemory(old); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GitCommit(st.Root, "fixture: 旧事实"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "new.md"), []byte("主力数据库是 Postgres"), 0o644)
	rep, err := Import(st, SrcMarkdownDir, dir, true, "")
	if err != nil {
		t.Fatal(err)
	}
	in := inbox.New(st)
	cands, _ := in.ListCandidates(rep.Batch)
	found := false
	for _, c := range cands {
		if strings.Contains(c.Body, "Postgres") && c.DuplicateOf != "" {
			found = true
			if c.Supersedes == "" {
				t.Errorf("有时间证据且更新 → 应给 supersede 建议: %+v", c)
			}
			if c.Group != inbox.GroupConflict {
				t.Errorf("应标 conflict 组: %s", c.Group)
			}
		}
	}
	if !found {
		t.Fatalf("应识别相似并给建议: %+v", cands)
	}
}

func runSQLiteScript(db, sql string) (string, error) {
	cmd := exec.Command("sqlite3", db, sql)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("sqlite3: %s", errb.String())
	}
	return out.String(), nil
}
