// Package exporter 全量导出与整库还原（P2-H4：导出即完整；roundtrip 哈希一致）。
// restore 是唯一绕过 inbox 的通道——还原的是已人审的库（spec §状态机注）。
package exporter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/remin-dev/remin/internal/store"
)

// manifestName 导出清单文件名（确定性：只含文件哈希与版本，不含生成时间）
const manifestName = "MANIFEST.json"

// Manifest 哈希清单
type Manifest struct {
	Files   map[string]string `json:"files"`   // 相对路径 → sha256
	Version int               `json:"version"` // 导出时索引版本
}

// exportedDirs 参与导出的目录与文件（真源内容；可重建加速层不导出）
var exportedDirs = []string{"memory", "inbox", "audit"}

// Export 全量导出到 outDir（含 supersession 历史、provenance、审收记录）
func Export(st *store.Store, outDir string) (*Manifest, error) {
	v, err := st.Version()
	if err != nil {
		return nil, err
	}
	m := &Manifest{Files: map[string]string{}, Version: v}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	copyDir := func(rel string) error {
		return filepath.Walk(filepath.Join(st.Root, rel), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			relPath, _ := filepath.Rel(st.Root, path)
			dst := filepath.Join(outDir, relPath)
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			h, err := copyFileHash(path, dst)
			if err != nil {
				return err
			}
			m.Files[filepath.ToSlash(relPath)] = h
			return nil
		})
	}
	for _, d := range exportedDirs {
		if err := copyDir(d); err != nil {
			return nil, err
		}
	}
	// config 与 VERSION
	for _, rel := range []string{"config.yaml", filepath.Join("index", "VERSION")} {
		src := filepath.Join(st.Root, rel)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dst := filepath.Join(outDir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, err
		}
		h, err := copyFileHash(src, dst)
		if err != nil {
			return nil, err
		}
		m.Files[filepath.ToSlash(rel)] = h
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(outDir, manifestName), append(data, '\n'), 0o644); err != nil {
		return nil, err
	}
	return m, nil
}

// Restore 从导出物整库还原：先验证全部哈希，再整体替换；VERSION 取 max（单调不回退）。
// 不追加审计记录（还原的是已人审的库；git 提交信息即还原凭证），保证 roundtrip 哈希一致。
func Restore(st *store.Store, bundleDir string) error {
	data, err := os.ReadFile(filepath.Join(bundleDir, manifestName))
	if err != nil {
		return fmt.Errorf("缺少 %s（不是 remin 导出物？）: %w", manifestName, err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("清单损坏: %w", err)
	}
	// 阶段一：全量校验（任一不符即中止，不动真源）
	paths := make([]string, 0, len(m.Files))
	for rel := range m.Files {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		if strings.Contains(rel, "..") {
			return fmt.Errorf("清单含非法路径 %s", rel)
		}
		h, err := fileHash(filepath.Join(bundleDir, filepath.FromSlash(rel)))
		if err != nil {
			return fmt.Errorf("导出物缺文件 %s: %w", rel, err)
		}
		if h != m.Files[rel] {
			return fmt.Errorf("哈希不符：%s（导出物被改动，拒绝还原）", rel)
		}
	}
	// 阶段二：整体替换
	for _, d := range exportedDirs {
		if err := os.RemoveAll(filepath.Join(st.Root, d)); err != nil {
			return err
		}
	}
	for _, rel := range paths {
		dst := filepath.Join(st.Root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		src := filepath.Join(bundleDir, filepath.FromSlash(rel))
		if err := copyFile(src, dst); err != nil {
			return err
		}
	}
	// VERSION 冲突取 max
	local, _ := st.Version()
	version := m.Version
	if local > version {
		version = local
	}
	if err := st.WriteVersion(version); err != nil {
		return err
	}
	_, err = store.GitCommit(st.Root, fmt.Sprintf("restore: 整库还原自导出物（v%d，%d 文件，roundtrip 哈希一致）", version, len(paths)))
	return err
}

func copyFileHash(src, dst string) (string, error) {
	h, err := copyFileRetHash(src, dst)
	return h, err
}

func copyFile(src, dst string) error {
	_, err := copyFileRetHash(src, dst)
	return err
}

func copyFileRetHash(src, dst string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer out.Close()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), in); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fileHash(path string) (string, error) {
	in, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer in.Close()
	h := sha256.New()
	if _, err := io.Copy(h, in); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyBundle 校验导出物完整性（不还原）
func VerifyBundle(bundleDir string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(bundleDir, manifestName))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	for rel, want := range m.Files {
		got, err := fileHash(filepath.Join(bundleDir, filepath.FromSlash(rel)))
		if err != nil || got != want {
			return &m, fmt.Errorf("哈希不符: %s", rel)
		}
	}
	return &m, nil
}
