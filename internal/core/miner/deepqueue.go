// deep 待挖队列：快速路径挖过、尚待 LLM 深度提取的 transcript 行段。
// 挂在 transcripts-cache/deep-queue.jsonl（可弃缓存，不入真源 git）。
package miner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DeepItem 一段待深挖的 transcript 行范围（行号为解析后 JSONL 行，1-based 闭区间）
type DeepItem struct {
	Path     string `json:"path"`
	FromLine int    `json:"from_line"`
	ToLine   int    `json:"to_line"`
	QueuedAt string `json:"queued_at"`
}

// DeepQueue 队列整体（transcripts-cache/deep-queue.jsonl）
type DeepQueue struct {
	Path string
}

func LoadDeepQueue(root string) *DeepQueue {
	return &DeepQueue{Path: filepath.Join(root, "transcripts-cache", "deep-queue.jsonl")}
}

func deepKey(path string, from, to int) string {
	return path + "|" + strconv.Itoa(from) + "|" + strconv.Itoa(to)
}

func (q *DeepQueue) All() []DeepItem {
	data, err := os.ReadFile(q.Path)
	if err != nil {
		return nil
	}
	var items []DeepItem
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var it DeepItem
		if json.Unmarshal([]byte(line), &it) == nil {
			items = append(items, it)
		}
	}
	return items
}

// Append 幂等追加：同文件同段不重复入队
func (q *DeepQueue) Append(path string, from, to int) error {
	if from > to {
		return nil
	}
	key := deepKey(path, from, to)
	for _, it := range q.All() {
		if deepKey(it.Path, it.FromLine, it.ToLine) == key {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(q.Path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(q.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, _ := json.Marshal(DeepItem{Path: path, FromLine: from, ToLine: to, QueuedAt: nowISO()})
	_, err = f.Write(append(data, '\n'))
	return err
}

// RemoveDone 处理完成后摘除指定段（读改写重写整个文件）
func (q *DeepQueue) RemoveDone(keys map[string]bool) error {
	return q.rewrite(func(it DeepItem) bool {
		return !keys[deepKey(it.Path, it.FromLine, it.ToLine)]
	})
}

// RemoveCovered 摘除 path 下被 [from,to] 深挖范围完全覆盖的段
// （from_line ≥ from 且 to_line ≤ to——增量深挖只出队真正挖过的段，
// 早于断点的待挖段保留给闲时 tick）
func (q *DeepQueue) RemoveCovered(path string, from, to int) error {
	return q.rewrite(func(it DeepItem) bool {
		return !(it.Path == path && it.FromLine >= from && it.ToLine <= to)
	})
}

func (q *DeepQueue) rewrite(keep func(DeepItem) bool) error {
	items := q.All()
	var sb strings.Builder
	for _, it := range items {
		if !keep(it) {
			continue
		}
		data, _ := json.Marshal(it)
		sb.Write(data)
		sb.WriteByte('\n')
	}
	if sb.Len() == 0 {
		if err := os.Remove(q.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	tmp := q.Path + ".tmp"
	if err := os.WriteFile(tmp, []byte(sb.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, q.Path) // 原子换入（崩溃不截断队列）
}

// Save 保留给未来批量场景（当前 Append 即写、Remove 走原子 rewrite）
func (q *DeepQueue) Save() error {
	items := q.All()
	var sb strings.Builder
	for _, it := range items {
		data, _ := json.Marshal(it)
		sb.Write(data)
		sb.WriteByte('\n')
	}
	if sb.Len() == 0 {
		return nil
	}
	return os.WriteFile(q.Path, []byte(sb.String()), 0o644)
}
