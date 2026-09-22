package search

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/remin-dev/remin/internal/index"
	"github.com/remin-dev/remin/internal/store"
	"github.com/remin-dev/remin/internal/testutil"
)

func doc(id, body, facet, status, trust, expires, reviewed, verifyResult string) *store.Memory {
	m := &store.Memory{
		ID: id, Type: store.TypeSemantic, Facet: facet, Status: status,
		CapturedAt: store.NowTime(), ReviewedAt: reviewed, Modified: store.NowTime(),
		Trust: trust, Source: store.SourceAgent,
		Provenance: store.Provenance{Origin: "t", Ref: "t#1", Quote: body},
		Version:    store.FormatVersion, Body: body, Expires: expires,
	}
	if verifyResult != "" {
		m.Verify = &store.Verify{Condition: "c", Result: verifyResult}
	}
	return m
}

var now = time.Date(2026, 9, 21, 12, 0, 0, 0, time.Local)

func build(docs ...*store.Memory) *Searcher {
	return New(index.Build(1, docs))
}

// M2 完成判据：确定性——同版本+同查询=同结果
func TestDeterministic(t *testing.T) {
	s := build(
		doc("mem_A", "部署前必须先跑迁移脚本", "dev", store.StatusActive, store.TrustHumanVerified, "", store.NowTime(), ""),
		doc("mem_B", "回复用中文，代码注释用英文", "dev", store.StatusActive, store.TrustHumanVerified, "", store.NowTime(), ""),
	)
	r1 := s.Search("部署 注意事项", Options{Now: now})
	r2 := s.Search("部署 注意事项", Options{Now: now})
	if !reflect.DeepEqual(r1, r2) {
		t.Errorf("同查询应同结果:\n%+v\n%+v", r1, r2)
	}
	if len(r1.Hits) == 0 || r1.Hits[0].ID != "mem_A" {
		t.Errorf("中文查询首位应命中 mem_A: %+v", r1.Hits)
	}
	if len(r1.Hits) > 1 && r1.Hits[1].Score >= r1.Hits[0].Score {
		t.Errorf("排序应为分数降序: %+v", r1.Hits)
	}
}

// 弃权（A5）：垃圾查询必 abstain，且是显式字段
func TestAbstainGarbageQuery(t *testing.T) {
	s := build(doc("mem_A", "部署前必须先跑迁移脚本", "dev", store.StatusActive, store.TrustHumanVerified, "", store.NowTime(), ""))
	r := s.Search("zzxxqq 无关词", Options{Now: now})
	if !r.Abstained || r.Reason != "no_match" {
		t.Errorf("垃圾查询应弃权: %+v", r)
	}
	if len(r.Hits) != 0 {
		t.Errorf("弃权时不应有结果")
	}
}

// stale 注入率 = 0：superseded / verify failed / ephemeral 过期均不可见
func TestStaleExcluded(t *testing.T) {
	s := build(
		doc("mem_active", "数据库是 Postgres 新事实", "dev", store.StatusActive, store.TrustHumanVerified, "", store.NowTime(), ""),
		doc("mem_super", "数据库是 Mongo 旧事实", "dev", store.StatusSuperseded, store.TrustHumanVerified, "", store.NowTime(), ""),
		doc("mem_vfail", "登录走 OAuth", "dev", store.StatusActive, store.TrustHumanVerified, "", store.NowTime(), store.VerifyFailed),
		doc("mem_expired", "上次会话交接内容", "dev", store.StatusActive, store.TrustUnverified, "7d", "2026-01-01T00:00:00+08:00", ""),
	)
	r := s.Search("数据库 Mongo OAuth 会话", Options{Now: now, TopK: 10})
	for _, h := range r.Hits {
		if h.ID != "mem_active" {
			t.Errorf("过期/被替代/验证失败的记忆不应出现: %+v", h)
		}
	}
}

// facet 过滤 + trust 随行
func TestFacetFilterAndTrustCarried(t *testing.T) {
	s := build(
		doc("mem_dev", "部署脚本在 scripts 下", "dev", store.StatusActive, store.TrustAgentClaimed, "", store.NowTime(), ""),
		doc("mem_life", "部署家里的路由器", "life", store.StatusActive, store.TrustHumanVerified, "", store.NowTime(), ""),
	)
	r := s.Search("部署", Options{Now: now, Facet: "life"})
	if len(r.Hits) != 1 || r.Hits[0].ID != "mem_life" {
		t.Errorf("facet 过滤失败: %+v", r.Hits)
	}
	if r.Hits[0].Trust != store.TrustHumanVerified {
		t.Errorf("trust 应随行: %+v", r.Hits[0])
	}
}

// 快照语义：钉住旧版本的索引看不到新落库的记忆
func TestSnapshotPinning(t *testing.T) {
	st := testutil.NewStore(t)
	m1 := doc("mem_A", "Postgres 迁移脚本", "dev", store.StatusActive, store.TrustHumanVerified, "", store.NowTime(), "")
	m1.ReviewedAt = store.NowTime()
	if err := st.SaveMemory(m1); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteVersion(1); err != nil {
		t.Fatal(err)
	}
	v1, err := index.Ensure(st, 1)
	if err != nil {
		t.Fatal(err)
	}
	// 之后新增记忆、版本推进
	m2 := doc("mem_B", "Mongo 已弃用", "dev", store.StatusActive, store.TrustHumanVerified, "", store.NowTime(), "")
	if err := st.SaveMemory(m2); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteVersion(2); err != nil {
		t.Fatal(err)
	}
	v2, err := index.Ensure(st, 2)
	if err != nil {
		t.Fatal(err)
	}
	r1 := New(v1).Search("Mongo", Options{Now: now})
	r2 := New(v2).Search("Mongo", Options{Now: now})
	if len(r1.Hits) != 0 {
		t.Errorf("v1 快照不应看到 v2 新增: %+v", r1.Hits)
	}
	if len(r2.Hits) != 1 {
		t.Errorf("v2 应看到: %+v", r2.Hits)
	}
}

// 索引缺失且非当前版本 → 报错（不静默用新数据顶替旧快照）
func TestMissingOldIndexRefuses(t *testing.T) {
	st := testutil.NewStore(t)
	m := doc("mem_A", "x", "dev", store.StatusActive, store.TrustHumanVerified, "", store.NowTime(), "")
	if err := st.SaveMemory(m); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteVersion(5); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Ensure(st, 3); err == nil {
		t.Error("缺失旧版本索引应报错而非静默重建")
	}
	// 当前版本缺失 → 重建
	idx, err := index.Ensure(st, 5)
	if err != nil || len(idx.Docs) != 1 {
		t.Errorf("当前版本应自动重建: %v", err)
	}
}

// ── 弃权校准（uplift 真源实测暴露的缺口回归）────────────────────────────────
// 真实感 20 条中文库：垃圾填充查询（只靠高频散字碰撞）必须弃权；
// 简短合法查询（共享词级 term）不受影响。分数阈值无法分离两者（实验证实：
// 垃圾 7.1 > 合法 6.0 区间重叠）——词级重合判据是确定性且与库规模无关的解。

var calibDocs = []string{
	"部署前必须检查数据库迁移脚本与回滚方案",
	"回复用中文，代码注释用英文",
	"球队主力前锋已改为梅西",
	"用户主力语言是 Rust",
	"构建一律走 pnpm 构建，不要用 npm",
	"团队周会固定在每周三上午十点",
	"登录走 OAuth，客户端凭据放环境变量",
	"验证统一走宪法套件，不单跑单元测试",
	"数据库部署窗口固定在周五低峰",
	"上次会话的交接内容：修了构建脚本",
	"偏好深色主题，编辑器字体用等宽",
	"接口错误统一返回结构化数据",
	"周末不处理工作消息，周一统一回复",
	"测试夹具放公共目录，不要复制粘贴",
	"部署脚本不要用 latest 标签，钉死版本号",
	"搜索的中文分词是一元加二元",
	"审计日志按追加写，永不覆盖",
	"会议室预订走日历工具",
	"数据备份保留七天，过期自动清理",
	"这个项目的需求文档在知识库",
}

func calibSearcher() *Searcher {
	var ms []*store.Memory
	for i, b := range calibDocs {
		ms = append(ms, doc(fmt.Sprintf("mem_C%02d", i), b, "dev", store.StatusActive, store.TrustHumanVerified, "", store.NowTime(), ""))
	}
	return build(ms...)
}

// TestAbstainFillerQueryOnRealisticStore 垃圾填充查询：散字碰撞不得作为置信（finding 回归）
func TestAbstainFillerQueryOnRealisticStore(t *testing.T) {
	s := calibSearcher()
	for _, q := range []string{
		"完全无关的查询 zzxxqq", // 原发现用例（当时返回 3 条命中）
		"随便看看有什么东西",
		"一些完全不相干的字词组合",
	} {
		r := s.Search(q, Options{Now: now})
		if !r.Abstained || r.Reason != "word_match_required" {
			t.Errorf("填充查询 %q 必须按词级判据弃权（得 %d 命中 reason=%s）", q, len(r.Hits), r.Reason)
		}
	}
}

// TestTerseLegitQueriesStillHit 简短合法查询不受判据影响（词级重合即置信）
func TestTerseLegitQueriesStillHit(t *testing.T) {
	s := calibSearcher()
	for q, wantSub := range map[string]string{
		"周会 时间":    "周会",
		"构建 pnpm":  "pnpm",
		"登录 OAuth": "OAuth",
		"备份 保留":    "备份",
		"深色 主题":    "深色",
	} {
		r := s.Search(q, Options{Now: now})
		if r.Abstained {
			t.Errorf("合法查询 %q 不应弃权: %s", q, r.Reason)
			continue
		}
		if !strings.Contains(r.Hits[0].Content, wantSub) {
			t.Errorf("查询 %q 首命中应含 %q: %+v", q, wantSub, r.Hits[0].Content)
		}
	}
}

// TestSingleCharQueryAbstains 纯单字查询按词级判据弃权（单字不构成词级信号，宁可不知道）
func TestSingleCharQueryAbstains(t *testing.T) {
	s := calibSearcher()
	r := s.Search("库", Options{Now: now})
	if !r.Abstained || r.Reason != "word_match_required" {
		t.Fatalf("纯单字查询应按词级判据弃权: %+v", r)
	}
}

// TestAbstainReasonOrder 词级判据先于低分判据（reason 可区分）
func TestAbstainReasonOrder(t *testing.T) {
	s := calibSearcher()
	r := s.Search("完全无关的查询", Options{Now: now})
	if !r.Abstained || r.Reason != "word_match_required" {
		t.Fatalf("散字-only 命中应报 word_match_required: %+v", r)
	}
	r2 := s.Search("zzxxqq wwvvuu", Options{Now: now})
	if !r2.Abstained || r2.Reason != "no_match" {
		t.Fatalf("无碰撞应报 no_match: %+v", r2)
	}
}

// TestWordMatchFacetScoped 词级证据必须在可见切片内：词级 term 只出现在其他 facet 的
// 记忆里时，该 facet 下最高命中仅散字重合 → 仍弃权
func TestWordMatchFacetScoped(t *testing.T) {
	s := build(
		doc("mem_F1", "部署窗口固定在周五低峰", "work", store.StatusActive, store.TrustHumanVerified, "", store.NowTime(), ""),
		doc("mem_F2", "团队周会安排在周三上午", "dev", store.StatusActive, store.TrustHumanVerified, "", store.NowTime(), ""),
	)
	r := s.Search("部署 窗口", Options{Facet: "dev", Now: now}) // 词级命中只在 work facet
	if !r.Abstained {
		t.Fatalf("跨 facet 词级命中不得作为置信: %+v", r.Hits)
	}
	r2 := s.Search("部署 窗口", Options{Facet: "work", Now: now})
	if r2.Abstained || r2.Hits[0].ID != "mem_F1" {
		t.Fatalf("同 facet 词级命中应返回: %+v", r2)
	}
}

// TestTopOnlyWordJudgment 最高命中无词级重合而次名有：整体弃权（top 即答案，
// 次名的词级证据救不了碰撞驱动的 top 分数）
func TestTopOnlyWordJudgment(t *testing.T) {
	s := build(
		doc("mem_T1", "中率最的无了着是着", "dev", store.StatusActive, store.TrustHumanVerified, "", store.NowTime(), ""), // 散字堆（高频字密堆，BM25 分高）
		doc("mem_T2", "部署窗口固定在周五低峰", "dev", store.StatusActive, store.TrustHumanVerified, "", store.NowTime(), ""),
	)
	// 钉判据行为：查询的 bigram（了看/看是）不出现在 T1 的相邻对里（T1 只有散字
	// 了/着/是）→ 仅散字重合 → 弃权（注意：查询任意相邻二字本身就是 bigram，
	// 自然垃圾查询的 bigram 极少碰巧出现在无关文本——这正是判据有效的原因）
	r := s.Search("了看是", Options{Now: now})
	if !r.Abstained || r.Reason != "word_match_required" {
		t.Fatalf("散字-only top 应弃权: %+v", r)
	}
}

// TestASCIISingleLetterAbstains 纯单字母查询弃权（不构成词级信号）
func TestASCIISingleLetterAbstains(t *testing.T) {
	s := calibSearcher()
	r := s.Search("a b c", Options{Now: now})
	if !r.Abstained {
		t.Fatalf("纯单字母查询应弃权: %+v", r)
	}
}

// TestBM25ScoreValuePinned 钉死 BM25 归一化链路的精确分数（mutation 存活位点
// search.go norm==0 守卫 ==→!= 后小夹具排名不变仅分数漂移——分数值断言杀灭；
// 确定性引擎的分数是契约的一部分：同版本+同查询=同分数）
func TestBM25ScoreValuePinned(t *testing.T) {
	s := calibSearcher()
	r := s.Search("周会 时间", Options{Now: now})
	if len(r.Hits) == 0 || r.Hits[0].ID != "mem_C05" {
		t.Fatalf("首命中应 mem_C05: %+v", r.Hits)
	}
	if r.Hits[0].Score != 6.7 {
		t.Fatalf("分数漂移（BM25 归一化链路被改）: %v ≠ 6.7", r.Hits[0].Score)
	}
}
