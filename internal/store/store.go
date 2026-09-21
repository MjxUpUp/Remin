package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Store 记忆真源仓库（~/.remin/）
type Store struct {
	Root string
}

// Init 创建真源仓库：目录骨架、.gitignore、config.yaml、VERSION=0、git 首提交
func Init(root string) (*Store, error) {
	st := &Store{Root: root}
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		return nil, fmt.Errorf("%s 已是 git 仓库（已初始化过？）", root)
	}
	if _, err := GitHasIdentity(root); err != nil {
		return nil, err // 首提交与审收归因都需要 git 身份
	}
	dirs := []string{
		"memory",
		filepath.Join("inbox", "candidates"),
		filepath.Join("inbox", "batches"),
		"audit",
		"index",
		"views",
		"transcripts-cache",
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			return nil, fmt.Errorf("创建目录失败: %w", err)
		}
	}
	gitignore := "index/bm25-*\nviews/\ntranscripts-cache/\n"
	files := map[string]string{
		".gitignore":    gitignore,
		"config.yaml":   defaultConfigYAML,
		"index/VERSION": "0\n",
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
			return nil, fmt.Errorf("写入 %s 失败: %w", rel, err)
		}
	}
	if err := GitInit(root); err != nil {
		return nil, err
	}
	// 禁用后台 auto-gc：审收是连续高频小提交序列，detached gc 与 ref 更新竞态
	// 会丢失最新 commit 对象（实测：import+promote 同秒提交后 HEAD 对象消失）。
	// 需要整理时用户可手动 git gc。
	for _, kv := range [][2]string{{"gc.auto", "0"}, {"gc.autodetach", "false"}, {"core.commitGraph", "false"}} {
		if _, err := GitRun(root, "config", kv[0], kv[1]); err != nil {
			return nil, fmt.Errorf("配置 %s 失败: %w", kv[0], err)
		}
	}
	if _, err := GitRun(root, "add", "-A"); err != nil {
		return nil, err
	}
	if _, err := GitRun(root, "commit", "-q", "-m", "remin init: 记忆真源仓库建立"); err != nil {
		return nil, fmt.Errorf("init 首提交失败: %w", err)
	}
	return st, nil
}

// Open 打开已初始化的真源仓库
func Open(root string) (*Store, error) {
	st := &Store{Root: root}
	if _, err := os.Stat(filepath.Join(root, "index", "VERSION")); err != nil {
		return nil, fmt.Errorf("%s 不是已初始化的 remin 真源（先运行 remin init）", root)
	}
	return st, nil
}

const defaultConfigYAML = `facets: [dev, work, life]
autonomy: conservative
inject_facet: dev
tool_bindings:
  claude-code: dev
  codex: dev
  cursor: dev
  gemini-cli: dev
sync_remote: ""
`

// MemoryPath 记忆文件路径 memory/<type>/<id>.md
func (s *Store) MemoryPath(mType, id string) string {
	return filepath.Join(s.Root, "memory", mType, id+".md")
}

// SaveMemory 写记忆文件（不涉及 git；事务由 promotion 编排）
func (s *Store) SaveMemory(m *Memory) error {
	if err := m.Validate(); err != nil {
		return err
	}
	content, err := m.Render()
	if err != nil {
		return err
	}
	dir := filepath.Join(s.Root, "memory", m.Type)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.MemoryPath(m.Type, m.ID), []byte(content), 0o644)
}

// GetMemory 按 id 读取记忆（跨类型目录查找）
func (s *Store) GetMemory(id string) (*Memory, error) {
	for _, t := range []string{
		TypeEpisodic, TypeSemantic, TypeProcedural,
		TypePreference, TypeDecision, TypeSpatialContext,
	} {
		path := s.MemoryPath(t, id)
		if data, err := os.ReadFile(path); err == nil {
			m, err := ParseMemory(string(data))
			if err != nil {
				return nil, fmt.Errorf("解析 %s 失败: %w", path, err)
			}
			return m, nil
		}
	}
	return nil, fmt.Errorf("记忆 %s 不存在", id)
}

// MemoryExists id 是否已被占用（仅当前工作区）
func (s *Store) MemoryExists(id string) bool {
	_, err := s.GetMemory(id)
	return err == nil
}

// ListMemories 列出全部记忆（含 superseded/expired；确定性按 id 排序）
func (s *Store) ListMemories() ([]*Memory, error) {
	base := filepath.Join(s.Root, "memory")
	var out []*Memory
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(base, e.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".md") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(base, e.Name(), f.Name()))
			if err != nil {
				continue
			}
			m, err := ParseMemory(string(data))
			if err != nil {
				return nil, fmt.Errorf("解析 %s 失败: %w", f.Name(), err)
			}
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Version 当前索引版本号
func (s *Store) Version() (int, error) {
	data, err := os.ReadFile(filepath.Join(s.Root, "index", "VERSION"))
	if err != nil {
		return 0, fmt.Errorf("读取 VERSION 失败: %w", err)
	}
	var v int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &v); err != nil {
		return 0, fmt.Errorf("VERSION 内容非法: %q", string(data))
	}
	return v, nil
}

// WriteVersion 写索引版本号（仅 promotion/restore/sync 事务内调用）
func (s *Store) WriteVersion(v int) error {
	return os.WriteFile(filepath.Join(s.Root, "index", "VERSION"),
		[]byte(fmt.Sprintf("%d\n", v)), 0o644)
}

// SupersessionChain 沿链查询：older = 本条替代过的旧条目链，newer = 替代本条的新条目链
func (s *Store) SupersessionChain(id string) (older, newer []*Memory, err error) {
	cur, err := s.GetMemory(id)
	if err != nil {
		return nil, nil, err
	}
	// 向旧走（supersedes 单向指针由新指旧）
	for m := cur; m.Supersedes != ""; {
		old, e := s.GetMemory(m.Supersedes)
		if e != nil {
			break
		}
		older = append(older, old)
		m = old
	}
	// 向新走（superseded_by）
	for m := cur; m.SupersededBy != ""; {
		nxt, e := s.GetMemory(m.SupersededBy)
		if e != nil {
			break
		}
		newer = append(newer, nxt)
		m = nxt
	}
	return older, newer, nil
}

// ConfigPath config.yaml 路径
func (s *Store) ConfigPath() string {
	return filepath.Join(s.Root, "config.yaml")
}
