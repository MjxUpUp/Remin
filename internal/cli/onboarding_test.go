package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/store"
	"github.com/remin-dev/remin/internal/testutil"
)

// —— P0-③ promote/reject --type 类型分诊 ——

func mixedBatch(t *testing.T) (*store.Store, string) {
	t.Helper()
	st := testutil.NewStore(t)
	mk := func(typ, body string) *inbox.Candidate {
		c := &inbox.Candidate{}
		c.Type = typ
		c.Facet = "dev"
		c.Status = store.StatusCandidate
		c.CapturedAt = store.NowTime()
		c.ReviewedAt = store.TimeUnknown
		c.Modified = store.NowTime()
		c.Trust = store.TrustUnverified
		c.Source = store.SourceAgent
		c.Provenance = store.Provenance{Origin: "claude-code", Ref: "session#t, line 1", Quote: body}
		c.Version = store.FormatVersion
		c.Body = body
		return c
	}
	in := inbox.New(st)
	id, _, err := in.AddBatch("test", []*inbox.Candidate{
		mk(store.TypeEpisodic, "会话交接 recap A"),
		mk(store.TypeEpisodic, "会话交接 recap B"),
		mk(store.TypePreference, "偏好：回复用中文"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return st, id
}

func TestResolveIDsTypeFilter(t *testing.T) {
	st, batch := mixedBatch(t)
	in := inbox.New(st)
	ids, err := resolveIDs(in, selectorFlags{batch: batch, all: true, typ: store.TypeEpisodic}, "候选")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("--type episodic 应只命中 2 条: %v", ids)
	}
	ids2, err := resolveIDs(in, selectorFlags{batch: batch, all: true, typ: store.TypePreference}, "候选")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids2) != 1 {
		t.Fatalf("--type preference 应只命中 1 条: %v", ids2)
	}
	// 不给 --type 行为不变（全量）
	ids3, err := resolveIDs(in, selectorFlags{batch: batch, all: true}, "候选")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids3) != 3 {
		t.Fatalf("无 --type 应全量 3 条: %v", ids3)
	}
}

func TestRejectTypeCLI(t *testing.T) {
	st, batch := mixedBatch(t)
	rootCmd.SetArgs([]string{"reject", "--batch", batch, "--all", "--type", store.TypeEpisodic, "--root", st.Root})
	out := captureStdout(t, func() {
		if err := rootCmd.Execute(); err != nil {
			t.Errorf("reject --type 应成功: %v", err)
		}
	})
	if !strings.Contains(out, "2 条") {
		t.Errorf("应只拒绝 2 条 episodic: %s", out)
	}
	// preference 仍在批次里待审
	in := inbox.New(st)
	cands, err := in.ListCandidates(batch)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].Type != store.TypePreference {
		t.Errorf("preference 应保留待审: %+v", cands)
	}
}

// —— P0-②⑦ inbox 构成统计 + --type 查看 + 行动引导 ——

func TestInboxListCompositionAndGuidance(t *testing.T) {
	st, batch := mixedBatch(t)
	rootCmd.SetArgs([]string{"inbox", "--root", st.Root})
	out := captureStdout(t, func() {
		if err := rootCmd.Execute(); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{
		"episodic 2", "preference 1", // 构成统计
		"remin reject --batch", // 拒绝指引（原版缺失）
		"真源:",                  // 触点透明 footer
	} {
		if !strings.Contains(out, want) {
			t.Errorf("inbox 列表输出缺 %q:\n%s", want, out)
		}
	}
	_ = batch
}

func TestInboxDetailViewTypeFilter(t *testing.T) {
	st, batch := mixedBatch(t)
	rootCmd.SetArgs([]string{"inbox", "--batch", batch, "--type", store.TypePreference, "--root", st.Root})
	out := captureStdout(t, func() {
		if err := rootCmd.Execute(); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "偏好：回复用中文") {
		t.Errorf("--type preference 应显示偏好候选:\n%s", out)
	}
	if strings.Contains(out, "会话交接 recap") {
		t.Errorf("--type preference 不应显示 episodic:\n%s", out)
	}
	if !strings.Contains(out, "共 3 条（本视图 1 条）") && !strings.Contains(out, "本视图 1 条") {
		t.Errorf("应如实标注过滤后计数:\n%s", out)
	}
}

// —— P0-① 触点真源 footer ——

func TestRootFooterOnPromote(t *testing.T) {
	st, batch := mixedBatch(t)
	rootCmd.SetArgs([]string{"promote", "--batch", batch, "--all", "--type", store.TypePreference, "--root", st.Root})
	out := captureStdout(t, func() {
		if err := rootCmd.Execute(); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "真源: "+st.Root) {
		t.Errorf("promote 输出应含真源路径:\n%s", out)
	}
	if !strings.Contains(out, "已采纳 1 条") {
		t.Errorf("应只采纳 1 条 preference:\n%s", out)
	}
}

// —— P0-④ sync --set-remote 私有仓确认 ——

func TestSyncSetRemoteRequiresYes(t *testing.T) {
	withNonTTY(t)
	st := testutil.NewStore(t)
	remote := filepath.Join(t.TempDir(), "private.git")
	rootCmd.SetArgs([]string{"sync", "--set-remote", remote, "--root", st.Root})
	errText := captureStderr(t, func() {
		if err := rootCmd.Execute(); err == nil {
			t.Error("非 TTY 无 --yes 应报错（fail 走 SilentExit）")
		}
	})
	if !strings.Contains(errText, "私有") {
		t.Fatalf("错误应提及私有仓库: %s", errText)
	}
}

func TestSyncSetRemoteWithYes(t *testing.T) {
	withNonTTY(t)
	t.Cleanup(func() { syncFlags.yes = false }) // cobra flag 值跨 Execute 持久，防泄漏进后续测试
	st := testutil.NewStore(t)
	remote := filepath.Join(t.TempDir(), "private.git")
	rootCmd.SetArgs([]string{"sync", "--set-remote", remote, "--yes", "--root", st.Root})
	out := captureStdout(t, func() {
		if err := rootCmd.Execute(); err != nil {
			t.Errorf("--yes 应通过: %v", err)
		}
	})
	if !strings.Contains(out, "同步远端已设置") {
		t.Errorf("应设置成功: %s", out)
	}
}

// —— P1-⑤ init 向导（纯函数注入） ——

func TestWizardDefaults(t *testing.T) {
	lines := []string{"", "", "", ""} // 全回车取默认
	i := 0
	res, err := runInitWizard(func() string {
		if i < len(lines) {
			s := lines[i]
			i++
			return s
		}
		return ""
	}, wizardEnv{
		DefaultRoot:  "/tmp/remin-default",
		GitIdentity:  func() (string, error) { return "u <u@x>", nil },
		DetectAgents: func() []string { return []string{"claude-code", "codex"} },
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Root != "/tmp/remin-default" || res.Autonomy != "conservative" || !res.WireAgents || res.Remote != "" {
		t.Errorf("默认应：路径默认/保守档/接线/跳过远端: %+v", res)
	}
}

func TestWizardChoices(t *testing.T) {
	// 输入：自定义路径 / autonomy=2(快速) / 接线 n / 远端 url / 私有确认 y
	lines := []string{"/tmp/custom-remin", "2", "n", "git@github.com:u/mem.git", "y"}
	i := 0
	res, err := runInitWizard(func() string {
		s := lines[i]
		i++
		return s
	}, wizardEnv{
		DefaultRoot:  "/tmp/remin-default",
		GitIdentity:  func() (string, error) { return "u <u@x>", nil },
		DetectAgents: func() []string { return []string{"claude-code"} },
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Root != "/tmp/custom-remin" || res.Autonomy != "fast" || res.WireAgents {
		t.Errorf("选择应生效: %+v", res)
	}
	if res.Remote != "git@github.com:u/mem.git" {
		t.Errorf("远端应记录: %+v", res)
	}
}

func TestWizardRemoteMustConfirmPrivate(t *testing.T) {
	// 有检测到 agent 时第 3 步才提问接线（行序：路径/档位/接线/远端/确认）
	lines := []string{"", "", "", "git@github.com:u/mem.git", "n"} // 私有确认答 n
	i := 0
	_, err := runInitWizard(func() string {
		s := lines[i]
		i++
		return s
	}, wizardEnv{
		DefaultRoot:  "/tmp/d",
		GitIdentity:  func() (string, error) { return "u <u@x>", nil },
		DetectAgents: func() []string { return []string{"claude-code"} },
	})
	if err == nil || !strings.Contains(err.Error(), "私有") {
		t.Fatalf("远端未确认私有应报错: %v", err)
	}
}

func TestWizardGitIdentityMissing(t *testing.T) {
	_, err := runInitWizard(func() string { return "" }, wizardEnv{
		DefaultRoot:  "/tmp/d",
		GitIdentity:  func() (string, error) { return "", errGitIdentityMissing },
		DetectAgents: func() []string { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "git config") {
		t.Fatalf("git 身份缺失应给指引: %v", err)
	}
}

// withStdinDevNull 把 os.Stdin 换成 /dev/null（真实 char device + 立即 EOF——
// 不注入 stdinIsTTY，走产品真实判定路径；评审 P2-3：红线最薄处必须有 CLI 级测试）
func withStdinDevNull(t *testing.T) {
	t.Helper()
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	oldIn, oldTTY := os.Stdin, stdinIsTTY
	os.Stdin = f
	t.Cleanup(func() {
		os.Stdin = oldIn
		stdinIsTTY = oldTTY
		f.Close()
	})
}

// stdinIsTTY 判据钉死（mutation 存活位点 onboarding.go:40）：char-device stdin
// 必须判真——判据翻转会让真终端用户拿不到向导；EOF 兜底是 initWizard 层的职责
func TestStdinIsTTYCharDevice(t *testing.T) {
	withStdinDevNull(t) // /dev/null 是 char device，Stat 无错
	if !stdinIsTTY() {
		t.Error("char-device stdin（/dev/null）应判为 TTY（真终端判据；EOF 兜底在 initWizard 层）")
	}
}

// char-device stdin + EOF（</dev/null 防挂起的 CI 形态）→ 走直通路径，
// stdout 零向导泄漏，git 身份缺失时错误与旧版一致（评审 P2-1 契约）
func TestInitCharDeviceEOFAllPlain(t *testing.T) {
	withStdinDevNull(t)
	root := filepath.Join(t.TempDir(), "remin-store")
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t.local")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t.local")
	rootCmd.SetArgs([]string{"init", "--root", root})
	out := captureStdout(t, func() {
		if err := rootCmd.Execute(); err != nil {
			t.Errorf("init 应成功: %v", err)
		}
	})
	if !strings.Contains(out, "真源仓库已建立") || strings.Contains(out, "向导") {
		t.Errorf("EOF 应回落直通输出，零向导泄漏:\n%s", out)
	}
}

func TestInitCharDeviceEOFIdentityMissingOldBehavior(t *testing.T) {
	withStdinDevNull(t)
	// 清空身份（环境变量全空 = 无 global 配置的 CI 形态）
	t.Setenv("GIT_AUTHOR_NAME", "")
	t.Setenv("GIT_AUTHOR_EMAIL", "")
	t.Setenv("GIT_COMMITTER_NAME", "")
	t.Setenv("GIT_COMMITTER_EMAIL", "")
	root := filepath.Join(t.TempDir(), "remin-store")
	rootCmd.SetArgs([]string{"init", "--root", root})
	out := captureStdout(t, func() {
		if err := rootCmd.Execute(); err == nil {
			t.Error("缺 git 身份应失败")
		}
	})
	if strings.Contains(out, "向导") {
		t.Errorf("错误路径同样不得泄漏向导提示（评审 P2-1）:\n%s", out)
	}
}

// —— sync TTY 确认分支（评审 P2-3：此前零测试） ——

func TestSyncSetRemoteTTYConfirmEOF(t *testing.T) {
	// char-device stdin + EOF：TTY 分支提示后读到 EOF → fail-closed，远端不设置
	withStdinDevNull(t)
	st := testutil.NewStore(t)
	remote := filepath.Join(t.TempDir(), "private.git")
	rootCmd.SetArgs([]string{"sync", "--set-remote", remote, "--root", st.Root})
	errText := captureStderr(t, func() {
		if err := rootCmd.Execute(); err == nil {
			t.Error("EOF 未确认应失败")
		}
	})
	if !strings.Contains(errText, "私有") {
		t.Errorf("fail-closed 错误应含私有: %s", errText)
	}
}

func TestSyncSetRemoteTTYConfirmYes(t *testing.T) {
	// TTY 判定注入为 true + 管道喂 "y"：确认通过并设置
	oldTTY := stdinIsTTY
	stdinIsTTY = func() bool { return true }
	t.Cleanup(func() { stdinIsTTY = oldTTY })
	st := testutil.NewStore(t)
	remote := filepath.Join(t.TempDir(), "private.git")
	oldIn := os.Stdin
	pr, pw, _ := os.Pipe()
	pw.WriteString("y\n")
	pw.Close()
	os.Stdin = pr
	t.Cleanup(func() { os.Stdin = oldIn })
	rootCmd.SetArgs([]string{"sync", "--set-remote", remote, "--root", st.Root})
	out := captureStdout(t, func() {
		if err := rootCmd.Execute(); err != nil {
			t.Errorf("确认 y 应通过: %v", err)
		}
	})
	if !strings.Contains(out, "同步远端已设置") {
		t.Errorf("应设置成功: %s", out)
	}
}

func TestInboxUnknownTypeRejected(t *testing.T) {
	st, _ := mixedBatch(t)
	rootCmd.SetArgs([]string{"inbox", "--batch", "whatever", "--type", "opinion", "--root", st.Root})
	errText := captureStderr(t, func() {
		if err := rootCmd.Execute(); err == nil {
			t.Error("未知类型应报错")
		}
	})
	if !strings.Contains(errText, "未知类型") {
		t.Errorf("拼错类型应早失败: %s", errText)
	}
}

func TestResolveIDsIDAndTypeConflict(t *testing.T) {
	st, batch := mixedBatch(t)
	in := inbox.New(st)
	if _, err := resolveIDs(in, selectorFlags{ids: []string{"x"}, typ: store.TypeEpisodic}, "候选"); err == nil || !strings.Contains(err.Error(), "互斥") {
		t.Errorf("--id 与 --type 同给应显式报错: %v", err)
	}
	_ = batch
}

// —— P1-⑤ 非 TTY init 行为不变 ——

// withNonTTY 注入非交互 stdin 判定（测试环境 stdin 可能是 char device，不可依赖真实形态）
func withNonTTY(t *testing.T) {
	t.Helper()
	old := stdinIsTTY
	stdinIsTTY = func() bool { return false }
	t.Cleanup(func() { stdinIsTTY = old })
}

func TestInitNonTTYBehaviorUnchanged(t *testing.T) {
	withNonTTY(t)
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t.local")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t.local")
	root := filepath.Join(t.TempDir(), "remin-store")
	rootCmd.SetArgs([]string{"init", "--root", root})
	out := captureStdout(t, func() {
		if err := rootCmd.Execute(); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "真源仓库已建立") {
		t.Errorf("非 TTY init 应保持原输出:\n%s", out)
	}
	if strings.Contains(out, "向导") {
		t.Errorf("非 TTY 不应出现向导:\n%s", out)
	}
}
