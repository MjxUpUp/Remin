// Package config 真源仓库的 config.yaml（facet 定义、自治档位、工具绑定、同步远端）
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

const (
	AutonomyConservative = "conservative" // 全人工审收（默认）
	AutonomyFast         = "fast"         // 仅 ephemeral recap 自动生效
)

type Config struct {
	Facets      []string          `yaml:"facets"`
	Autonomy    string            `yaml:"autonomy"`
	InjectFacet string            `yaml:"inject_facet"`
	Bindings    map[string]string `yaml:"tool_bindings"`
	SyncRemote  string            `yaml:"sync_remote"`
}

func Default() *Config {
	return &Config{
		Facets:      []string{"dev", "work", "life"},
		Autonomy:    AutonomyConservative,
		InjectFacet: "dev",
		Bindings: map[string]string{
			"claude-code": "dev", "codex": "dev", "cursor": "dev", "gemini-cli": "dev",
		},
	}
}

// Load 从真源仓库读取；文件缺失时返回默认值（只读场景容错）
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("解析 config.yaml 失败: %w", err)
	}
	if c.Autonomy == "" {
		c.Autonomy = AutonomyConservative
	}
	return &c, nil
}

// Save 写回 config.yaml
func (c *Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
