package miner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/remin-dev/remin/internal/core/config"
	"github.com/remin-dev/remin/internal/testutil"
)

// P1-⑥ 首挖默认限量：mtime 近 N 天过滤（--full-history/Force 不受限；queue 路径不受限）

func TestMineSinceDaysSkipsOldTranscripts(t *testing.T) {
	st := testutil.NewStore(t)
	dir := t.TempDir()

	writeTranscript(t, transcript(dir, "old0001-0000-0000-0000-000000000001.jsonl"),
		`{"type":"user","sessionId":"old0001","cwd":"/p","timestamp":"2026-08-01T10:00:00+08:00","message":{"role":"user","content":"记住：老会话的偏好"}}`)
	writeTranscript(t, transcript(dir, "new0001-0000-0000-0000-000000000002.jsonl"),
		`{"type":"user","sessionId":"new0001","cwd":"/p","timestamp":"2026-09-21T10:00:00+08:00","message":{"role":"user","content":"记住：新会话的偏好"}}`)

	old30 := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "Users-jx-own-projects-Remin", "old0001-0000-0000-0000-000000000001.jsonl"), old30, old30); err != nil {
		t.Fatal(err)
	}

	// 默认 7 天：只挖新文件，老文件计入跳过
	rep, err := Mine(context.Background(), st, config.Default(), Options{ClaudeDir: dir, SinceDays: 7})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Transcripts != 1 {
		t.Fatalf("近 7 天应只挖 1 个: %+v", rep)
	}
	if rep.SkippedOld != 1 {
		t.Fatalf("老 transcript 应计入跳过: %+v", rep)
	}

	// 全量（SinceDays=0）：两个都挖
	rep2, err := Mine(context.Background(), st, config.Default(), Options{ClaudeDir: dir, SinceDays: 0, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Transcripts != 2 || rep2.SkippedOld != 0 {
		t.Fatalf("全量应挖 2 个且无跳过: %+v", rep2)
	}
}

func TestMineSinceDays30CoversOlderFile(t *testing.T) {
	st := testutil.NewStore(t)
	dir := t.TempDir()
	writeTranscript(t, transcript(dir, "old0002-0000-0000-0000-000000000003.jsonl"),
		`{"type":"user","sessionId":"old0002","cwd":"/p","timestamp":"2026-08-01T10:00:00+08:00","message":{"role":"user","content":"记住：更早的偏好"}}`)
	old20 := time.Now().Add(-20 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "Users-jx-own-projects-Remin", "old0002-0000-0000-0000-000000000003.jsonl"), old20, old20); err != nil {
		t.Fatal(err)
	}
	rep, err := Mine(context.Background(), st, config.Default(), Options{ClaudeDir: dir, SinceDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Transcripts != 1 || rep.SkippedOld != 0 {
		t.Fatalf("30 天窗口应覆盖 20 天前文件: %+v", rep)
	}
}
