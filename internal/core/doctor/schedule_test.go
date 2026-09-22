package doctor

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/remin-dev/remin/internal/testutil"
)

// OS 调度器接线契约：install 写调度文件（含 tick 命令与间隔）+ 台账 effect +
// best-effort 加载（可注入）；status 如实；remove 摘文件与台账；uninstall 台账回放可摘。

func withFakeSchedExec(t *testing.T) *[]string {
	t.Helper()
	calls := &[]string{}
	old := schedExec
	schedExec = func(name string, args ...string) error {
		*calls = append(*calls, name+" "+strings.Join(args, " "))
		return nil
	}
	t.Cleanup(func() { schedExec = old })
	return calls
}

func TestScheduleInstallRemoveStatus(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("平台护栏：调度文件格式 darwin/linux 各自实现，仅在本平台跑对应断言（另一平台逻辑由其 CI 覆盖）")
	}
	home := t.TempDir()
	st := testutil.NewStore(t)
	bin := filepath.Join(home, "bin", "remin")

	calls := withFakeSchedExec(t)
	if _, err := ScheduleInstall(home, st.Root, bin, "4h"); err != nil {
		t.Fatal(err)
	}

	var schedFile, cmdFile string
	if runtime.GOOS == "darwin" {
		schedFile = filepath.Join(home, "Library", "LaunchAgents", "dev.reminmem.tick.plist")
		cmdFile = schedFile // plist 单文件：命令与间隔同在
	} else {
		cmdFile = filepath.Join(home, ".config", "systemd", "user", "remin-tick.service")
		schedFile = filepath.Join(home, ".config", "systemd", "user", "remin-tick.timer")
	}
	data, err := os.ReadFile(cmdFile)
	if err != nil {
		t.Fatalf("命令文件应落位: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "tick") || !strings.Contains(body, bin) {
		t.Fatalf("命令文件应含 tick 命令与二进制路径: %s", body)
	}
	timerData, err := os.ReadFile(schedFile)
	if err != nil {
		t.Fatalf("调度文件应落位: %v", err)
	}
	if !strings.Contains(string(timerData), "14400") { // 4h = 14400s（间隔在 timer/plist）
		t.Errorf("调度文件应含间隔 14400s: %s", timerData)
	}
	// 台账登记
	ledger, err := LoadLedger(st.Root)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range ledger.Effects {
		if e.Kind == "sched-file" && e.Command == bin {
			found = true
		}
	}
	if !found {
		t.Fatalf("sched-file effect 应入台账: %+v", ledger.Effects)
	}
	// best-effort 加载被调起；重装语义正确（darwin：bootout 卸旧先于 bootstrap 装新；
	// linux：daemon-reload 先于 enable）
	if len(*calls) < 2 {
		t.Fatalf("加载调用应至少 2 次: %v", *calls)
	}
	if runtime.GOOS == "darwin" {
		if !strings.Contains((*calls)[0], "bootout") || !strings.Contains((*calls)[1], "bootstrap") {
			t.Fatalf("调用序应为 bootout → bootstrap: %v", *calls)
		}
	} else {
		if !strings.Contains((*calls)[0], "daemon-reload") || !strings.Contains((*calls)[1], "enable") {
			t.Fatalf("调用序应为 daemon-reload → enable: %v", *calls)
		}
	}

	// status 如实
	stat := ScheduleStatus(home, st.Root)
	if !stat.Installed || stat.File != schedFile {
		t.Fatalf("status 应如实报已装: %+v", stat)
	}

	// remove 摘文件与台账
	if err := ScheduleRemove(home, st.Root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(schedFile); !os.IsNotExist(err) {
		t.Fatalf("remove 后调度文件应删除")
	}
	ledger, _ = LoadLedger(st.Root)
	for _, e := range ledger.Effects {
		if e.Kind == "sched-file" {
			t.Fatalf("remove 后台账应无 sched-file: %+v", e)
		}
	}
}

func TestScheduleInstallUnsupportedPlatform(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		t.Skip("平台护栏：本测试只在无调度实现的平台验证显式报错面；darwin/linux 走上面的正向用例")
	}
	home := t.TempDir()
	st := testutil.NewStore(t)
	if _, err := ScheduleInstall(home, st.Root, "bin", "4h"); err == nil {
		t.Fatal("不支持的平台应显式报错并给指引")
	}
}

// TestScheduleRemoveToleratesTeardownFailure 调度器侧卸载失败不阻断（agent 未加载是常态），
// 文件删除与台账清理照常完成
func TestScheduleRemoveToleratesTeardownFailure(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("平台护栏：调度文件格式 darwin/linux 各自实现，仅在本平台跑对应断言（另一平台逻辑由其 CI 覆盖）")
	}
	home := t.TempDir()
	st := testutil.NewStore(t)
	old := schedExec
	schedExec = func(name string, args ...string) error {
		return os.ErrPermission // 调度器侧全部失败
	}
	t.Cleanup(func() { schedExec = old })
	if _, err := ScheduleInstall(home, st.Root, filepath.Join(home, "bin", "remin"), "4h"); err != nil {
		t.Fatal(err)
	}
	if err := ScheduleRemove(home, st.Root); err != nil {
		t.Fatalf("teardown 失败不应阻断 remove（文件删除是主清理面）: %v", err)
	}
	var schedFile string
	if runtime.GOOS == "darwin" {
		schedFile = filepath.Join(home, "Library", "LaunchAgents", "dev.reminmem.tick.plist")
	} else {
		schedFile = filepath.Join(home, ".config", "systemd", "user", "remin-tick.timer")
	}
	if _, err := os.Stat(schedFile); !os.IsNotExist(err) {
		t.Fatalf("teardown 失败时文件仍应删除")
	}
}

// TestUninstallSkipsUserModifiedSchedFile 用户改过的调度文件不盲删（披露为 skipped）
func TestUninstallSkipsUserModifiedSchedFile(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("平台护栏：调度文件格式 darwin/linux 各自实现，仅在本平台跑对应断言（另一平台逻辑由其 CI 覆盖）")
	}
	home := t.TempDir()
	st := testutil.NewStore(t)
	bin := filepath.Join(home, "bin", "remin")
	withFakeSchedExec(t)
	if _, err := ScheduleInstall(home, st.Root, bin, "4h"); err != nil {
		t.Fatal(err)
	}
	var schedFile string
	if runtime.GOOS == "darwin" {
		schedFile = filepath.Join(home, "Library", "LaunchAgents", "dev.reminmem.tick.plist")
	} else {
		schedFile = filepath.Join(home, ".config", "systemd", "user", "remin-tick.timer")
	}
	// 用户接管：改写为我们不再认领的内容
	os.WriteFile(schedFile, []byte("# user took over\n"), 0o644)
	rep, err := Uninstall(home, st.Root, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(schedFile); err != nil {
		t.Fatalf("用户改过的调度文件不应被盲删")
	}
	skipped := false
	for _, s := range rep.Skipped {
		if strings.Contains(s.Reason, "用户修改") {
			skipped = true
		}
	}
	if !skipped {
		t.Fatalf("应披露为 skipped（用户修改）: %+v", rep.Skipped)
	}
}

// uninstall 台账回放摘除调度文件（用户修改过的文件不盲删）
func TestUninstallRevertsSchedule(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("平台护栏：调度文件格式 darwin/linux 各自实现，仅在本平台跑对应断言（另一平台逻辑由其 CI 覆盖）")
	}
	home := t.TempDir()
	st := testutil.NewStore(t)
	bin := filepath.Join(home, "bin", "remin")

	withFakeSchedExec(t)
	if _, err := ScheduleInstall(home, st.Root, bin, "4h"); err != nil {
		t.Fatal(err)
	}
	var schedFile string
	if runtime.GOOS == "darwin" {
		schedFile = filepath.Join(home, "Library", "LaunchAgents", "dev.reminmem.tick.plist")
	} else {
		schedFile = filepath.Join(home, ".config", "systemd", "user", "remin-tick.timer")
	}
	rep, err := Uninstall(home, st.Root, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(schedFile); !os.IsNotExist(err) {
		t.Fatalf("uninstall 回放应删调度文件: %s", schedFile)
	}
	removed := strings.Join(rep.Removed, "\n")
	if !strings.Contains(removed, "tick") && !strings.Contains(removed, "sched") {
		t.Fatalf("卸载报告应含调度摘除: %+v", rep.Removed)
	}
}
