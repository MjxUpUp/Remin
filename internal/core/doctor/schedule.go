// OS 调度器接线（闲时 tick 的「档位」面）：无常驻 daemon——由 OS 调度器按间隔
// 拉起一次性 `remin tick`。macOS launchd agent / Linux systemd user timer；
// effect 挂接线台账（kind=sched-file），uninstall 按台账回放摘除。
package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// schedExec 调度器加载/卸载命令的可注入执行面（测试替身用）
var schedExec = func(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

const (
	schedLabel    = "dev.reminmem.tick"
	schedUnitName = "remin-tick"
)

// ScheduleStatus 调度接线状态（如实：文件在即已装，不猜测加载态）
type ScheduleInfo struct {
	Installed bool   `json:"installed"`
	Platform  string `json:"platform"`
	File      string `json:"file,omitempty"`
	Interval  string `json:"interval,omitempty"`
	Note      string `json:"note,omitempty"`
}

func schedFiles(home string) []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{filepath.Join(home, "Library", "LaunchAgents", schedLabel+".plist")}
	case "linux":
		return []string{
			filepath.Join(home, ".config", "systemd", "user", schedUnitName+".service"),
			filepath.Join(home, ".config", "systemd", "user", schedUnitName+".timer"),
		}
	}
	return nil
}

// parseInterval 档位间隔（"30m"/"4h"/"1h30m"→秒；下限 15m 防高频轰炸）
func parseInterval(every string) (int, error) {
	if every == "" {
		every = "4h"
	}
	d, err := time.ParseDuration(every)
	if err != nil || d < 15*time.Minute {
		return 0, fmt.Errorf("间隔需 ≥15m 的时长（如 30m/1h/4h）: %q", every)
	}
	return int(d.Seconds()), nil
}

// ScheduleInstall 安装闲时 tick 调度：写调度文件 + best-effort 加载 + 台账登记。
// 返回给人看的安装描述与（若有）需手工执行的指引。
func ScheduleInstall(home, root, binPath, every string) ([]string, error) {
	secs, err := parseInterval(every)
	if err != nil {
		return nil, err
	}
	switch runtime.GOOS {
	case "darwin":
		return scheduleInstallLaunchd(home, root, binPath, secs)
	case "linux":
		return scheduleInstallSystemd(home, root, binPath, secs)
	}
	return nil, fmt.Errorf("当前平台 %s 暂无调度接线实现：可用系统定时任务定期执行 `%s tick --root %s`", runtime.GOOS, binPath, root)
}

func scheduleInstallLaunchd(home, root, binPath string, secs int) ([]string, error) {
	logPath := filepath.Join(root, "transcripts-cache", "tick.log")
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>tick</string>
		<string>--root</string>
		<string>%s</string>
	</array>
	<key>StartInterval</key>
	<integer>%d</integer>
	<key>RunAtLoad</key>
	<false/>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, schedLabel, xmlEscape(binPath), xmlEscape(root), secs, xmlEscape(logPath), xmlEscape(logPath))

	file := schedFiles(home)[0]
	if _, err := backupAndWrite(file, []byte(plist)); err != nil {
		return nil, fmt.Errorf("写入 launchd plist 失败: %w", err)
	}
	uid := strconv.Itoa(os.Getuid())
	// 重装/改档：先 bootout 旧任务再 bootstrap（label 已加载时 bootstrap 会失败，
	// 不先卸旧任务则运行中的间隔不更新而文件已更新——状态与实际不符）
	_ = schedExec("launchctl", "bootout", "gui/"+uid+"/"+schedLabel)
	loadErr := schedExec("launchctl", "bootstrap", "gui/"+uid, file)
	desc := []string{file + "（间隔 " + strconv.Itoa(secs) + "s）"}
	if loadErr != nil {
		desc = append(desc, "提示：launchctl bootstrap 未成功（"+loadErr.Error()+"），文件已落位，可手工执行：launchctl bootout gui/"+uid+"/"+schedLabel+" && launchctl bootstrap gui/"+uid+" "+file)
	}
	return desc, registerSchedEffects(root, binPath, file)
}

func scheduleInstallSystemd(home, root, binPath string, secs int) ([]string, error) {
	files := schedFiles(home)
	svc := fmt.Sprintf(`[Unit]
Description=Remin idle tick (incremental mine + deep drain)

[Service]
Type=oneshot
ExecStart=%s tick --root %s
`, binPath, root)
	timer := fmt.Sprintf(`[Unit]
Description=Run Remin idle tick periodically

[Timer]
OnBootSec=10min
OnUnitActiveSec=%ds
Unit=%s.service

[Install]
WantedBy=timers.target
`, secs, schedUnitName)

	if _, err := backupAndWrite(files[0], []byte(svc)); err != nil {
		return nil, fmt.Errorf("写入 systemd service 失败: %w", err)
	}
	if _, err := backupAndWrite(files[1], []byte(timer)); err != nil {
		return nil, fmt.Errorf("写入 systemd timer 失败: %w", err)
	}
	_ = schedExec("systemctl", "--user", "daemon-reload")
	enableErr := schedExec("systemctl", "--user", "enable", "--now", schedUnitName+".timer")
	desc := []string{files[1] + "（间隔 " + strconv.Itoa(secs) + "s）"}
	if enableErr != nil {
		desc = append(desc, "提示：systemctl enable 未成功（"+enableErr.Error()+"），文件已落位，可手工执行：systemctl --user enable --now "+schedUnitName+".timer")
	}
	// service 与 timer 都是我们创建的文件：两个 effect 都入台账（uninstall 按
	// 台账回放摘文件——只挂 timer 会漏 service 残留）
	for _, f := range files {
		if err := registerSchedEffects(root, binPath, f); err != nil {
			return desc, err
		}
	}
	return desc, nil
}

// registerSchedEffects 台账登记（幂等：同文件同命令替换）
func registerSchedEffects(root, binPath, schedFile string) error {
	ledger, err := LoadLedger(root)
	if err != nil {
		return err
	}
	ledger.Append(Effect{
		ID:      effectID("sched-file", schedFile, schedLabel),
		Kind:    "sched-file",
		File:    schedFile,
		Key:     schedLabel,
		Command: binPath,
		Created: true,
	})
	return ledger.Save(root)
}

// ScheduleRemove 摘除闲时 tick 调度（文件 + best-effort 卸载 + 台账）。
// 调度器侧卸载失败不阻断（agent 本就未加载是常态——install 时 bootstrap 也是
// best-effort）；文件删除失败才如实报错。
func ScheduleRemove(home, root string) error {
	var loadErrs []string
	for _, file := range schedFiles(home) {
		if _, err := os.Stat(file); err != nil {
			continue
		}
		_ = schedTeardown(file)
		if err := os.Remove(file); err != nil {
			loadErrs = append(loadErrs, err.Error())
		}
	}
	ledger, err := LoadLedger(root)
	if err != nil {
		return err
	}
	for _, e := range ledger.Effects {
		if e.Kind == "sched-file" {
			ledger.Remove(e.ID)
		}
	}
	if err := ledger.Save(root); err != nil {
		return err
	}
	if len(loadErrs) > 0 {
		return fmt.Errorf("调度文件删除失败: %s", strings.Join(loadErrs, "; "))
	}
	return nil
}

// ScheduleStatus 查看调度接线状态（linux 优先报 timer——间隔信息在 timer 内）
func ScheduleStatus(home, root string) *ScheduleInfo {
	s := &ScheduleInfo{Platform: runtime.GOOS}
	files := schedFiles(home)
	if len(files) == 0 {
		s.Note = "当前平台暂无调度接线实现"
		return s
	}
	// 逆序优先：schedFiles 末位是承载间隔的文件（linux timer；darwin 单文件）
	for i := len(files) - 1; i >= 0; i-- {
		if data, err := os.ReadFile(files[i]); err == nil {
			s.Installed = true
			s.File = files[i]
			s.Interval = schedIntervalOf(string(data))
			return s
		}
	}
	s.Note = "未安装（remin tick schedule install）"
	return s
}

func schedIntervalOf(body string) string {
	if i := strings.Index(body, "<integer>"); i >= 0 {
		if j := strings.Index(body[i:], "</integer>"); j > 0 {
			return strings.TrimSpace(body[i+9:i+j]) + "s"
		}
	}
	if i := strings.Index(body, "OnUnitActiveSec="); i >= 0 {
		rest := body[i+len("OnUnitActiveSec="):]
		if j := strings.IndexAny(rest, "\n\r"); j > 0 {
			return strings.TrimSpace(rest[:j])
		}
	}
	return ""
}

// xmlEscape plist 内文本转义（路径含 & < > 时不得产出非法 plist）
func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;", "'", "&#39;").Replace(s)
}

// schedTeardown 调度器侧卸载（best-effort；文件删除是主清理面）
func schedTeardown(schedFile string) error {
	switch {
	case strings.HasSuffix(schedFile, ".plist"):
		uid := strconv.Itoa(os.Getuid())
		return schedExec("launchctl", "bootout", "gui/"+uid+"/"+schedLabel)
	case strings.HasSuffix(schedFile, ".timer"):
		if err := schedExec("systemctl", "--user", "disable", "--now", schedUnitName+".timer"); err != nil {
			return err
		}
		return schedExec("systemctl", "--user", "daemon-reload")
	}
	return nil
}
