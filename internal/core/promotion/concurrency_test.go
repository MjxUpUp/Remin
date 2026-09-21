package promotion

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/remin-dev/remin/internal/core/audit"
	"github.com/remin-dev/remin/internal/core/exporter"
	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/store"
)

// P1-3 回归：audit 记录必须带 Batch——log --batch 按批次回看的落点
func TestPromoteAuditRecordCarriesBatch(t *testing.T) {
	st, in, au := fixture(t)
	batch, ids, _ := in.AddBatch("mine", []*inbox.Candidate{cand("带批次的记忆", "")})
	if _, err := Promote(st, in, au, Request{CandidateIDs: ids}); err != nil {
		t.Fatal(err)
	}
	recs, _ := au.List(audit.ActionPromote)
	if len(recs) != 1 || recs[0].Batch != batch {
		t.Fatalf("审计记录应带批次 id %s: %+v", batch, recs)
	}
	// 拒绝同样带批次
	batch2, ids2, _ := in.AddBatch("mine", []*inbox.Candidate{cand("将被拒绝", "")})
	if _, err := Reject(st, in, au, ids2, ""); err != nil {
		t.Fatal(err)
	}
	rrecs, _ := au.List(audit.ActionReject)
	if len(rrecs) != 1 || rrecs[0].Batch != batch2 {
		t.Fatalf("拒绝记录应带批次: %+v", rrecs)
	}
}

// P1-2 回归：并发 promote 串行化——版本不双分配、无半提交
func TestConcurrentPromotesSerialize(t *testing.T) {
	st, in, au := fixture(t)
	const n = 6
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		_, ids, _ := in.AddBatch("mine", []*inbox.Candidate{cand(fmt.Sprintf("并发记忆 %d", i), "")})
		wg.Add(1)
		go func(idx int, candIDs []string) {
			defer wg.Done()
			_, errs[idx] = Promote(st, in, au, Request{CandidateIDs: candIDs})
		}(i, ids)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("并发 promote %d 失败: %v", i, err)
		}
	}
	if v, _ := st.Version(); v != n {
		t.Errorf("版本应恰好 %d（不双分配不跳号）: %d", n, v)
	}
	ms, _ := st.ListMemories()
	if len(ms) != n {
		t.Errorf("应有 %d 条记忆: %d", n, len(ms))
	}
	// 审计台账无交错损坏（n 条 promote 记录，每条 1 id）
	recs, _ := au.List(audit.ActionPromote)
	if len(recs) != n {
		t.Errorf("审计记录应 %d 条: %d", n, len(recs))
	}
	// 工作区干净（无半提交残留）
	out, _ := store.GitRun(st.Root, "status", "--porcelain")
	if strings.TrimSpace(out) != "" {
		t.Errorf("并发后工作区应干净: %q", out)
	}
}

// P1-2 回归：并发 supersession——同一旧事实只被替代一次（TOCTOU 拦截）
func TestConcurrentSupersedeSameOld(t *testing.T) {
	st, in, au := fixture(t)
	_, ids, _ := in.AddBatch("mine", []*inbox.Candidate{cand("旧事实", "")})
	res, err := Promote(st, in, au, Request{CandidateIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	oldID := res.MemoryIDs[0]

	var wg sync.WaitGroup
	var succeeded, failed int64
	var mu sync.Mutex
	for i := 0; i < 4; i++ {
		_, ids2, _ := in.AddBatch("mine", []*inbox.Candidate{cand(fmt.Sprintf("新事实 %d", i), oldID)})
		wg.Add(1)
		go func(candIDs []string) {
			defer wg.Done()
			_, err := Promote(st, in, au, Request{CandidateIDs: candIDs})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed++
			} else {
				succeeded++
			}
		}(ids2)
	}
	wg.Wait()
	if succeeded != 1 {
		t.Errorf("同一旧事实并发替代应恰好成功 1 次: %d", succeeded)
	}
	old, _ := st.GetMemory(oldID)
	if old.Status != store.StatusSuperseded || old.SupersededBy == "" {
		t.Errorf("旧事实应被唯一替代: %+v", old)
	}
	// 双向链对称：superseded_by 指向的那条确实 supersedes 本条
	newer, _ := st.GetMemory(old.SupersededBy)
	if newer.Supersedes != oldID {
		t.Errorf("链不对称（TOCTOU 静默覆盖的证据）: old.superseded_by=%s, that.Supersedes=%s", old.SupersededBy, newer.Supersedes)
	}
}

// P1-2 回归：脏关键路径（memory/ 未提交）阻断变更事务——中断现场大声失败
func TestDirtyCriticalPathBlocksTransaction(t *testing.T) {
	st, in, au := fixture(t)
	// 模拟上次操作中断：memory/ 有未提交变更
	_, ids, _ := in.AddBatch("mine", []*inbox.Candidate{cand("脏树下的候选", "")})
	os.MkdirAll(filepath.Join(st.Root, "memory", "semantic"), 0o755)
	os.WriteFile(filepath.Join(st.Root, "memory", "semantic", "mem_DIRTY00000000000000000000.md"),
		[]byte("---\nid: mem_DIRTY00000000000000000000\n---\n未提交残留"), 0o644)

	_, err := Promote(st, in, au, Request{CandidateIDs: ids})
	if err == nil || !strings.Contains(err.Error(), "未提交变更") {
		t.Fatalf("脏关键路径应阻断并给出指引: %v", err)
	}
}

// P1-4 回归：restore 中途失败回滚（VERSION 写失败触发），真源回到原状
func TestRestoreMidwayFailureRollsBack(t *testing.T) {
	src, in, au := fixture2(t)
	_, ids, _ := in.AddBatch("mine", []*inbox.Candidate{cand("将随导出物走", "")})
	if _, err := Promote(src, in, au, Request{CandidateIDs: ids}); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), "bundle")
	if _, err := exportDir(src, bundle); err != nil {
		t.Fatal(err)
	}
	before, _ := src.ListMemories()
	vBefore, _ := src.Version()

	// 让换入后必然失败：VERSION 文件去写权限 → 换入后的 WriteVersion 失败
	verFile := filepath.Join(src.Root, "index", "VERSION")
	if err := os.Chmod(verFile, 0o400); err != nil {
		t.Fatal(err)
	}
	err := restoreDir(src, bundle)
	os.Chmod(verFile, 0o644)
	if err == nil {
		t.Fatal("中途失败应报错")
	}
	after, _ := src.ListMemories()
	if len(after) != len(before) {
		t.Errorf("回滚后记忆数应不变: %d → %d", len(before), len(after))
	}
	vAfter, _ := src.Version()
	if vAfter != vBefore {
		t.Errorf("回滚后版本应不变: %d → %d", vBefore, vAfter)
	}
}

// P1-4 回归：还原同内容 bundle（无 git 变更）不报错
func TestRestoreNoChangesIsSuccess(t *testing.T) {
	src, in, au := fixture2(t)
	_, ids, _ := in.AddBatch("mine", []*inbox.Candidate{cand("幂等还原", "")})
	if _, err := Promote(src, in, au, Request{CandidateIDs: ids}); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), "bundle")
	if _, err := exportDir(src, bundle); err != nil {
		t.Fatal(err)
	}
	if err := restoreDir(src, bundle); err != nil {
		t.Fatalf("同内容还原应成功（nothing to commit 容忍）: %v", err)
	}
}

// exportDir 导出包装
func exportDir(st *store.Store, dir string) (*exporter.Manifest, error) {
	return exporter.Export(st, dir)
}

func restoreDir(st *store.Store, dir string) error {
	return exporter.Restore(st, dir)
}

func fixture2(t *testing.T) (*store.Store, *inbox.Inbox, *audit.Audit) {
	return fixture(t)
}
