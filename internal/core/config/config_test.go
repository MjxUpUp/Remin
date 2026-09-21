package config

import (
	"os"
	"path/filepath"
	"testing"
)

// 文件缺失时容错返回默认（只读场景不崩）
func TestLoadMissingReturnsDefault(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "none.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Facets) != 3 || c.Facets[0] != "dev" {
		t.Errorf("默认三面: %+v", c.Facets)
	}
	if c.Autonomy != AutonomyConservative {
		t.Errorf("默认保守档: %s", c.Autonomy)
	}
	if c.Bindings["claude-code"] != "dev" {
		t.Errorf("默认绑定: %+v", c.Bindings)
	}
}

// Save → Load 往返保持字段
func TestSaveLoadRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	c := Default()
	c.Autonomy = AutonomyFast
	c.SyncRemote = "git@github.com:u/mem.git"
	c.Bindings["dsh"] = "life"
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Autonomy != AutonomyFast || got.SyncRemote != "git@github.com:u/mem.git" {
		t.Errorf("往返丢失: %+v", got)
	}
	if got.Bindings["dsh"] != "life" || got.Bindings["claude-code"] != "dev" {
		t.Errorf("绑定往返: %+v", got.Bindings)
	}
}

// autonomy 为空时回填默认（旧配置兼容）
func TestLoadFillsEmptyAutonomy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte("facets: [dev]\n"), 0o644)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Autonomy != AutonomyConservative {
		t.Errorf("空 autonomy 应回填默认: %q", c.Autonomy)
	}
}
