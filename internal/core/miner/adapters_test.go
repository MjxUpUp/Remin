package miner

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/remin-dev/remin/internal/core/config"
	"github.com/remin-dev/remin/internal/core/extractor"
	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/testutil"
)

// 多格式 transcript 契约：路径分派（zstd→dsh / rollout→codex / 其余→claude）；
// 各格式解析出 extractor.Event（含 Origin 归因）；多根发现去重；origin 随源
// （快速路径/recap/deep 全链路）。

const codexFixture = `{"timestamp":"2026-09-22T09:00:00.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"01a00d71-8111-71e2-b1f7-ef3a9d13301e","cwd":"/Users/jx/projects/affiliate","originator":"codex-cli","cli_version":"0.148.0"}}
{"timestamp":"2026-09-22T09:00:05.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"记住：上线前必须先跑 pnpm build 验证产物"}]}}
{"timestamp":"2026-09-22T09:00:10.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"我们最终选择 pnpm 而不是 npm，因为 workspace 协议解析是硬需求"}]}}
{"timestamp":"2026-09-22T09:00:15.000Z","type":"response_item","payload":{"type":"function_call","name":"shell","arguments":"{}"}}
{"timestamp":"2026-09-22T09:00:20.000Z","type":"event_msg","payload":{"type":"task_complete"}}
`

const dshFixture = `{"type":"session","version":0,"id":"session-85b2944f-ce05-4b01-8faf-b9566eedbb0a","createdAt":1787213329432,"cwd":"/Users/jx/own-projects/Forge","agentPreset":"standard"}
{"type":"permission/preset","seq":0,"time":1787213329965,"data":{"preset":"workspace-write"}}
{"type":"user/message","seq":14,"time":1787213444045,"data":{"content":[{"type":"text","text":"The approval policy changed from \"ask\" to \"never\" (changed by the user)."}],"source":{"kind":"plugin","plugin":"user-approval"},"role":"user","id":"fa4e8c77"}}
{"type":"user/message","seq":15,"time":1787213444046,"data":{"content":[{"type":"text","text":"检查我们这个项目，记得所有验证都要走 make constitution"}],"source":{"kind":"user","rpcId":"52d0145b"},"role":"user","id":"d66849ef"}}
{"type":"assistant/message","seq":55,"time":1787213446049,"data":{"turn":1,"step":1,"message":{"role":"assistant","content":[{"type":"reasoning","text":"内部推理不计入正文"},{"type":"text","text":"好的，验证统一走 make constitution，我会先跑宪法套件"}]}}}
{"type":"assistant/message","seq":60,"time":1787213450000,"data":{"turn":1,"step":2,"message":{"role":"assistant","content":[{"type":"tool-call","id":"call_41e0de34","name":"skill","arguments":"{\"name\":\"forge-quality\"}"}]}}}
{"type":"turn/end","seq":61,"time":1787213450001,"data":{"turn":1}}
`

// TestDetectFormat 路径分派契约
func TestDetectFormat(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/home/u/.dsh/sessions/--proj--/session-abc/session.jsonl.zstd", "dsh"},
		{"/home/u/.codex/sessions/2026/08/17/rollout-2026-08-17T09-58-57-01a.jsonl", "codex"},
		{"/home/u/.claude/projects/-proj/aaaabbbb.jsonl", "claude"},
		{"/tmp/custom/root/s.jsonl", "claude"},
	}
	for _, c := range cases {
		if got := detectFormat(c.path); got != c.want {
			t.Errorf("detectFormat(%s) = %s, want %s", c.path, got, c.want)
		}
	}
}

// TestParseCodexRollout codex rollout 解析：meta 取 session/cwd、message 取文本、
// function_call 标记工具使用、event_msg 忽略
func TestParseCodexRollout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-2026-09-22T09-00-00-01a00d71.jsonl")
	os.WriteFile(path, []byte(codexFixture), 0o644)
	events, lines, err := ParseTranscript(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	if lines != 5 {
		t.Fatalf("总行数应为 5: %d", lines)
	}
	if len(events) != 3 {
		t.Fatalf("应有 3 个文本事件（user/assistant/function_call）: %+v", events)
	}
	if events[0].Role != "user" || events[1].Role != "assistant" {
		t.Fatalf("角色错位: %+v", events)
	}
	if !strings.Contains(events[0].Text, "pnpm build") || !strings.Contains(events[1].Text, "workspace") {
		t.Fatalf("文本抽取错: %+v", events)
	}
	if !events[2].IsToolUse || events[2].Text != "" {
		t.Fatalf("function_call 应标记工具使用且无文本: %+v", events[2])
	}
	for _, ev := range events {
		if ev.SessionID != "01a00d71-8111-71e2-b1f7-ef3a9d13301e" {
			t.Fatalf("session id 应从 session_meta 继承: %+v", ev)
		}
		if ev.CWD != "/Users/jx/projects/affiliate" || ev.ProjectName != "affiliate" {
			t.Fatalf("cwd/项目名应从 meta 继承: %+v", ev)
		}
		if ev.Origin != "codex" {
			t.Fatalf("origin 归因应为 codex: %+v", ev)
		}
	}
	// 增量：从第 3 行起
	events3, _, err := ParseTranscript(path, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(events3) != 2 || events3[0].Line != 3 {
		t.Fatalf("断点续挖应从行 3 起: %+v", events3)
	}
}

// TestParseCodexCommandNoise codex 命令回显（<command-name> 等）沿用噪声过滤
func TestParseCodexCommandNoise(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-x.jsonl")
	os.WriteFile(path, []byte(`{"timestamp":"2026-09-22T09:00:00.000Z","type":"session_meta","payload":{"session_id":"s-codex","cwd":"/p/cx"}}
{"timestamp":"2026-09-22T09:00:05.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<command-name>/plugin</command-name>\n<command-message>plugin</command-message>"}]}}
`), 0o644)
	events, _, err := ParseTranscript(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 { // 噪声行仍解析为事件（过滤在 extractor.isNoise），但 SessionID 已继承
		t.Fatalf("命令回显仍为事件（extractor 过滤）: %+v", events)
	}
	cands := extractor.Extract(events)
	// 命令回显不产 typed 候选；transcript 带 session id 仍产 1 条 recap（episodic，预期行为）
	if len(cands) != 1 || cands[0].Type != "episodic" {
		t.Fatalf("命令回显只应产 recap（episodic）无 typed 候选: %+v", cands)
	}
}

// TestParseDSHSession DSH session 解析（解压器注入）：plugin 噪声过滤、text 抽取、
// tool-call 标记、epoch 毫秒转 RFC3339、session 头继承 cwd/id
func TestParseDSHSession(t *testing.T) {
	injectPlainDecompressor(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl.zstd")
	os.WriteFile(path, []byte(dshFixture), 0o644)
	events, lines, err := ParseTranscript(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	if lines != 7 {
		t.Fatalf("总行数应为 7: %d", lines)
	}
	if len(events) != 3 {
		t.Fatalf("应有 3 个事件（plugin 噪声不产事件）: %+v", events)
	}
	if events[0].Role != "user" || !strings.Contains(events[0].Text, "make constitution") {
		t.Fatalf("真人用户消息应保留（plugin 注入过滤）: %+v", events)
	}
	if events[1].Role != "assistant" || !strings.Contains(events[1].Text, "宪法套件") {
		t.Fatalf("助手 text 应抽取（reasoning 不计入正文）: %+v", events)
	}
	if strings.Contains(events[1].Text, "内部推理") {
		t.Fatalf("reasoning 不应混入正文: %+v", events)
	}
	if !events[2].IsToolUse {
		t.Fatalf("tool-call 应标记工具使用: %+v", events[2])
	}
	for _, ev := range events {
		if ev.SessionID != "session-85b2944f-ce05-4b01-8faf-b9566eedbb0a" {
			t.Fatalf("session id 应继承 session 头: %+v", ev)
		}
		if ev.CWD != "/Users/jx/own-projects/Forge" || ev.ProjectName != "Forge" {
			t.Fatalf("cwd/项目名应继承: %+v", ev)
		}
		if ev.Origin != "dsh" {
			t.Fatalf("origin 归因应为 dsh: %+v", ev)
		}
		if !strings.HasPrefix(ev.Timestamp, "2026-") {
			t.Fatalf("epoch 毫秒应转 RFC3339: %q", ev.Timestamp)
		}
	}
}

// TestParseDSHRealZstd 真实 zstd 压缩往返（zstd 二进制在场时）
func TestParseDSHRealZstd(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("平台护栏：真实压缩往返需 zstd 二进制（本机/多数开发机有；无二进制环境走注入解压器的解析测试）")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl.zstd")
	// 用 zstd 二进制真实压缩 fixture（与线上 .jsonl.zstd 形态一致）
	cmd := exec.Command("zstd", "-q", "-c", "-")
	cmd.Stdin = strings.NewReader(dshFixture)
	compressed, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, compressed, 0o644)
	events, _, err := ParseTranscript(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("真实 zstd 解压解析应同注入路径: %+v", events)
	}
}

// TestMineDiscoversAllRoots 多根发现：claude/codex/dsh 三根一次 mine 全覆盖（去重）
func TestMineDiscoversAllRoots(t *testing.T) {
	st := testutil.NewStore(t)
	claudeDir := t.TempDir()
	codexDir := t.TempDir()
	dshDir := t.TempDir()

	writeTranscript(t, transcript(claudeDir, sessID+".jsonl"), deepFixture)
	os.MkdirAll(filepath.Dir(filepath.Join(codexDir, "2026", "09", "22", "rollout-a.jsonl")), 0o755)
	os.WriteFile(filepath.Join(codexDir, "2026", "09", "22", "rollout-a.jsonl"), []byte(codexFixture), 0o644)
	sessDir := filepath.Join(dshDir, "--Users-jx-own-projects-Forge--", "session-85b2")
	os.MkdirAll(sessDir, 0o755)
	// 注入解压器直读原文件字节：写明文 JSONL 即可
	os.WriteFile(filepath.Join(sessDir, "session.jsonl.zstd"), []byte(dshFixture), 0o644)
	restore := injectPlainDecompressor(t)

	rep, err := Mine(context.Background(), st, config.Default(), Options{
		ClaudeDir:  claudeDir,
		ExtraRoots: []string{codexDir, dshDir, claudeDir}, // claudeDir 重复：应去重
	})
	restore()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Transcripts != 3 {
		t.Fatalf("三根应各挖 1 个 transcript（重复根去重）: %+v", rep)
	}
	// origin 归因随源
	in := inbox.New(st)
	batches, _ := in.ListBatches()
	origins := map[string]int{}
	for _, b := range batches {
		cands, _ := in.ListCandidates(b.ID)
		for _, c := range cands {
			origins[c.Provenance.Origin]++
		}
	}
	if origins["claude-code"] == 0 {
		t.Fatalf("claude 候选缺失: %+v", origins)
	}
	if origins["codex"] == 0 {
		t.Fatalf("codex 候选缺失: %+v", origins)
	}
	if origins["dsh"] == 0 {
		t.Fatalf("dsh 候选缺失: %+v", origins)
	}
}

// TestTickDeepOriginFollowsSource 深挖候选 origin 随源（codex·deep）
func TestTickDeepOriginFollowsSource(t *testing.T) {
	st := testutil.NewStore(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-b.jsonl")
	os.WriteFile(path, []byte(`{"timestamp":"2026-09-22T09:00:00.000Z","type":"session_meta","payload":{"session_id":"s2","cwd":"/p/cx2"}}
{"timestamp":"2026-09-22T09:00:05.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"这个项目验证要走 make constitution 才完整，单跑 go test 会漏依赖扫描"}]}}
`), 0o644)

	srv := deepServer(t, `[{"type":"preference","body":"验证统一走 make constitution，不单跑 go test","quote":"这个项目验证要走 make constitution 才完整，单跑 go test 会漏依赖扫描"}]`)
	defer srv.Close()

	cfg := config.Default()
	cfg.LLM = &config.LLMConfig{Endpoint: srv.URL, Model: "m", APIKey: "k", TimeoutMs: 3000}
	rep, err := Tick(context.Background(), st, cfg, TickOptions{ClaudeDir: t.TempDir(), ExtraRoots: []string{dir}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.DeepCandidates != 1 {
		t.Fatalf("tick 深挖应产 1 候选: %+v", rep)
	}
	cands, _ := inbox.New(st).ListCandidates(rep.DeepBatch)
	if len(cands) != 1 || cands[0].Provenance.Origin != "codex·deep" {
		t.Fatalf("深挖 origin 应随源 codex·deep: %+v", cands)
	}
}

// ── 测试脚手架 ───────────────────────────────────────────────────────────────

func injectPlainDecompressor(t *testing.T) func() {
	t.Helper()
	old := dshOpen
	dshOpen = func(path string) (io.ReadCloser, error) {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	t.Cleanup(func() { dshOpen = old }) // 测试结束即恢复（防污染后续测试的真实 zstd 路径）
	return func() { dshOpen = old }
}

// TestIncrementalSegmentInheritsSessionHeader 增量段（fromLine>1）必须继承会话头
// 状态（session/cwd/项目名）——审查 P1 修复的杀灭测试：头行在 fromLine 之前也要累积
func TestIncrementalSegmentInheritsSessionHeader(t *testing.T) {
	// codex：追加行 3-4 后从行 3 解析
	dir := t.TempDir()
	cpath := filepath.Join(dir, "rollout-inc.jsonl")
	os.WriteFile(cpath, []byte(codexFixture+
		`{"timestamp":"2026-09-22T09:00:25.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"记得先看 runbook 再动手"}]}}
`), 0o644)
	events, _, err := ParseTranscript(cpath, 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("行 6（追加的 user 消息）应产 1 事件: %+v", events)
	}
	ev := events[0]
	if ev.SessionID != "01a00d71-8111-71e2-b1f7-ef3a9d13301e" || ev.CWD != "/Users/jx/projects/affiliate" || ev.ProjectName != "affiliate" {
		t.Fatalf("增量段必须继承会话头（session/cwd/项目）: %+v", ev)
	}

	// dsh：追加行 8 后从行 8 解析
	injectPlainDecompressor(t)
	ddir := t.TempDir()
	dpath := filepath.Join(ddir, "session.jsonl.zstd")
	os.WriteFile(dpath, []byte(dshFixture+
		`{"type":"user/message","seq":20,"time":1787213500000,"data":{"content":[{"type":"text","text":"以后发布都要先跑插件验证"}],"source":{"kind":"user"},"role":"user"}}
`), 0o644)
	events, _, err = ParseTranscript(dpath, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("行 8 应产 1 事件: %+v", events)
	}
	ev = events[0]
	if ev.SessionID != "session-85b2944f-ce05-4b01-8faf-b9566eedbb0a" || ev.CWD != "/Users/jx/own-projects/Forge" || ev.ProjectName != "Forge" {
		t.Fatalf("DSH 增量段必须继承会话头: %+v", ev)
	}
}

// TestParseDSHCorruptArchiveFails 截断/损档必须以错误返回（调用方不推游标，下次重试）
// ——审查 P2 修复的杀灭测试：半档成功 ≠ 完整数据
func TestParseDSHCorruptArchiveFails(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("平台护栏：损档路径依赖真实 zstd 的非零退出（注入解压器无退出状态概念；无 zstd 环境由解析测试覆盖）")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl.zstd")
	// 构造截断档：压缩后砍掉尾部字节
	cmd := exec.Command("zstd", "-q", "-c", "-")
	cmd.Stdin = strings.NewReader(dshFixture)
	full, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, full[:len(full)-8], 0o644)
	_, _, err = ParseTranscript(path, 1)
	if err == nil {
		t.Fatal("截断档必须报错（不推游标防永久静默跳过）")
	}
}

// TestParseCodexDeveloperRoleFiltered developer 角色系统注入不产事件（与 DSH plugin 过滤同构）
func TestParseCodexDeveloperRoleFiltered(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-dev.jsonl")
	os.WriteFile(path, []byte(`{"timestamp":"2026-09-22T09:00:00.000Z","type":"session_meta","payload":{"session_id":"s-dev","cwd":"/p/dv"}}
{"timestamp":"2026-09-22T09:00:05.000Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"output_text","text":"[skill-scan] available skills catalog..."}]}}
{"timestamp":"2026-09-22T09:00:06.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"记住：部署走蓝绿"}]}}
`), 0o644)
	events, _, err := ParseTranscript(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Role != "user" {
		t.Fatalf("developer 注入应被过滤，仅剩 user 事件: %+v", events)
	}
}
