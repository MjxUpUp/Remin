package inject

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/store"
	"github.com/remin-dev/remin/internal/testutil"
)

func mem(id, body, facet, status string, verifyResult, expires, reviewed string) *store.Memory {
	m := &store.Memory{
		ID: id, Type: store.TypeSemantic, Facet: facet, Status: status,
		CapturedAt: reviewed, ReviewedAt: reviewed, Modified: reviewed,
		Trust: store.TrustHumanVerified, Source: store.SourceAgent,
		Provenance: store.Provenance{Origin: "t", Ref: "t", Quote: body},
		Version:    store.FormatVersion, Body: body, Expires: expires,
	}
	if verifyResult != "" {
		m.Verify = &store.Verify{Condition: "c", Result: verifyResult}
	}
	return m
}

var now = time.Date(2026, 9, 21, 12, 0, 0, 0, time.Local)

// 注入集排除：superseded / verify failed / ephemeral 过期（策略单一来源 Searchable + 过期）
func TestRunFiltersInvisible(t *testing.T) {
	st := testutil.NewStore(t)
	for _, m := range []*store.Memory{
		mem("mem_A", "正常记忆", "dev", store.StatusActive, "", "", store.NowTime()),
		mem("mem_S", "被替代记忆", "dev", store.StatusSuperseded, "", "", store.NowTime()),
		mem("mem_V", "验证失败记忆", "dev", store.StatusActive, store.VerifyFailed, "", store.NowTime()),
		mem("mem_E", "过期 recap", "dev", store.StatusActive, "", "7d", "2026-01-01T00:00:00+08:00"),
	} {
		if err := st.SaveMemory(m); err != nil {
			t.Fatal(err)
		}
	}
	res, err := Run(st, Options{Facet: "dev", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"mem_S", "mem_V", "mem_E"} {
		if strings.Contains(res.Text, bad) {
			t.Errorf("不可见记忆 %s 不应注入", bad)
		}
	}
	if !strings.Contains(res.Text, "mem_A") || res.Lines != 1 {
		t.Errorf("正常记忆应注入: lines=%d\n%s", res.Lines, res.Text)
	}
}

// facet 过滤 + 行数预算截断
func TestRunFacetFilterAndLineBudget(t *testing.T) {
	st := testutil.NewStore(t)
	if err := st.SaveMemory(mem("mem_DEV", "开发记忆", "dev", store.StatusActive, "", "", store.NowTime())); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveMemory(mem("mem_LIFE", "生活记忆", "life", store.StatusActive, "", "", store.NowTime())); err != nil {
		t.Fatal(err)
	}
	res, _ := Run(st, Options{Facet: "life", Now: now})
	if strings.Contains(res.Text, "mem_DEV") || !strings.Contains(res.Text, "mem_LIFE") {
		t.Error("facet 过滤失效")
	}
	res2, _ := Run(st, Options{Facet: "dev", Now: now, MaxLines: 1})
	if res2.Lines != 1 {
		t.Errorf("行数预算应截断: %d", res2.Lines)
	}
}

// recap 候选提示：待审 ephemeral 批次给出一键采纳指引
func TestRunRecapHint(t *testing.T) {
	st := testutil.NewStore(t)
	in := inbox.New(st)
	c := &inbox.Candidate{}
	c.Type = store.TypeEpisodic
	c.Facet = "dev"
	c.Status = store.StatusCandidate
	c.CapturedAt = store.NowTime()
	c.ReviewedAt = store.TimeUnknown
	c.Modified = store.NowTime()
	c.Trust = store.TrustUnverified
	c.Source = store.SourceAgent
	c.Provenance = store.Provenance{Origin: "t", Ref: "t", Quote: "q"}
	c.Expires = "7d"
	c.Version = store.FormatVersion
	c.Body = "会话交接"
	if _, _, err := in.AddBatch("mine", []*inbox.Candidate{c}); err != nil {
		t.Fatal(err)
	}
	res, err := Run(st, Options{Facet: "dev", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "一键采纳") {
		t.Errorf("应含 recap 采纳提示:\n%s", res.Text)
	}
}

// 追赶预算到：TimedOut 置位、注入照常
func TestRunDrainTimeoutStillInjects(t *testing.T) {
	st := testutil.NewStore(t)
	if err := st.SaveMemory(mem("mem_A", "正文", "dev", store.StatusActive, "", "", store.NowTime())); err != nil {
		t.Fatal(err)
	}
	res, err := Run(st, Options{
		Facet: "dev", Now: now,
		Drain: func(ctx context.Context) (int, error) {
			<-ctx.Done() // 模拟超预算
			return 0, ctx.Err()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut {
		t.Error("预算到应置 TimedOut")
	}
	if !strings.Contains(res.Text, "mem_A") {
		t.Error("超时降级后注入应照常")
	}
}

// 追赶失败：DrainErr 如实上报（不吞错），注入继续
func TestRunDrainErrorRecorded(t *testing.T) {
	st := testutil.NewStore(t)
	boom := errors.New("队列读失败")
	res, err := Run(st, Options{
		Facet: "dev", Now: now,
		Drain: func(ctx context.Context) (int, error) { return 0, boom },
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.DrainErr != boom.Error() {
		t.Errorf("失败应如实上报 DrainErr: %q", res.DrainErr)
	}
}

// 排序确定性：同输入两次注入产物一致
func TestRunDeterministic(t *testing.T) {
	st := testutil.NewStore(t)
	for _, id := range []string{"mem_A", "mem_B", "mem_C"} {
		if err := st.SaveMemory(mem(id, "内容"+id, "dev", store.StatusActive, "", "", store.NowTime())); err != nil {
			t.Fatal(err)
		}
	}
	r1, _ := Run(st, Options{Facet: "dev", Now: now})
	r2, _ := Run(st, Options{Facet: "dev", Now: now})
	if r1.Text != r2.Text || r1.Lines != r2.Lines {
		t.Error("同输入注入产物应一致")
	}
}
