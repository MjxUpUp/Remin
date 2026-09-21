package doctor

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLedgerRoundtrip(t *testing.T) {
	dir := t.TempDir()

	l, err := LoadLedger(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Effects) != 0 {
		t.Fatalf("不存在的台账应为空: %+v", l.Effects)
	}

	now := time.Now().Format(time.RFC3339)
	e := Effect{ID: "mcp-json|/home/x/.claude.json|mcpServers.memory", Kind: "mcp-json",
		File: "/home/x/.claude.json", Key: "mcpServers.memory", Command: "/home/x/.remin/bin/remin", TS: now}
	l.Append(e)
	if err := l.Save(dir); err != nil {
		t.Fatal(err)
	}

	l2, err := LoadLedger(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(l2.Effects) != 1 || l2.Effects[0].Command != e.Command {
		t.Fatalf("台账往返不一致: %+v", l2.Effects)
	}

	// 同 ID 追加 → 替换不重复
	e2 := e
	e2.Backup = "/some/backup"
	l2.Append(e2)
	if err := l2.Save(dir); err != nil {
		t.Fatal(err)
	}
	l3, _ := LoadLedger(dir)
	if len(l3.Effects) != 1 {
		t.Fatalf("同 ID 应替换不重复: %+v", l3.Effects)
	}
	if l3.Effects[0].Backup != e2.Backup {
		t.Fatal("替换后应保留新值")
	}

	// 删除
	l3.Remove(e.ID)
	if err := l3.Save(dir); err != nil {
		t.Fatal(err)
	}
	l4, _ := LoadLedger(dir)
	if len(l4.Effects) != 0 {
		t.Fatalf("删除后应为空: %+v", l4.Effects)
	}

	// 损坏台账：如实报错，不吞成空（与 readJSONObject 同一承诺）
	os.WriteFile(filepath.Join(dir, "wiring.json"), []byte("{bad"), 0o644)
	if _, err := LoadLedger(dir); err == nil {
		t.Fatal("损坏台账应报错而非静默视为空")
	}
}
