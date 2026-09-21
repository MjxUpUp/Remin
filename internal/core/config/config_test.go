package config

import (
	"os"
	"path/filepath"
	"strings"
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

// llm 节：深路径端点可配；密钥只从环境变量读（config.yaml 在 git 真源内，密钥不得入历史）
func TestLoadLLMSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte("llm:\n  endpoint: https://api.example.com/v1/chat/completions\n  model: m1\n  timeout_ms: 5000\n"), 0o644)
	t.Setenv("REMIN_LLM_API_KEY", "env-secret")
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.LLM == nil || c.LLM.Endpoint != "https://api.example.com/v1/chat/completions" ||
		c.LLM.Model != "m1" || c.LLM.TimeoutMs != 5000 {
		t.Fatalf("llm 节解析: %+v", c.LLM)
	}
	if c.LLM.APIKey != "env-secret" {
		t.Errorf("APIKey 应从 REMIN_LLM_API_KEY 注入: %q", c.LLM.APIKey)
	}
}

// 无 llm 节 = 深路径关闭（全系统零 LLM 默认不变）
func TestLoadWithoutLLMSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte("facets: [dev]\n"), 0o644)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.LLM != nil {
		t.Errorf("无 llm 节应为 nil: %+v", c.LLM)
	}
}

// timeout_ms 缺省回填硬预算默认
func TestLoadLLMDefaultTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte("llm:\n  endpoint: https://x\n"), 0o644)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.LLM == nil || c.LLM.TimeoutMs != LLMDefaultTimeoutMs {
		t.Errorf("缺省 timeout 应回填默认: %+v", c.LLM)
	}
}

// 保存往返不落密钥（yaml:"-"）
func TestSaveLLMDoesNotPersistKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	c := Default()
	c.LLM = &LLMConfig{Endpoint: "https://x", Model: "m", APIKey: "should-not-persist", TimeoutMs: 1000}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "should-not-persist") {
		t.Errorf("密钥不得写入 config.yaml（git 真源）: %s", data)
	}
}
