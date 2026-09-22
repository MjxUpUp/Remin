package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/remin-dev/remin/internal/core/config"
	"github.com/remin-dev/remin/internal/core/doctor"
	"github.com/remin-dev/remin/internal/core/miner"
	"github.com/remin-dev/remin/internal/store"
	"github.com/spf13/cobra"
)

var tickFlags struct {
	deepMax     int
	sinceDays   int
	fullHistory bool
}

// tick 闲时增量维护：增量快挖 → deep 待挖排空（OS 调度器按档拉起，无常驻 daemon）
var tickCmd = &cobra.Command{
	Use:   "tick",
	Short: "闲时增量维护：增量挖矿 + 深挖待办排空（供 OS 调度器定期拉起）",
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := mustStore()
		if err != nil {
			return fail(err)
		}
		cfg, err := config.Load(st.ConfigPath())
		if err != nil {
			return fail(err)
		}
		rep, err := miner.Tick(context.Background(), st, cfg, miner.TickOptions{
			ExtraRoots: miner.DefaultExtraRoots(), DeepMax: tickFlags.deepMax, SinceDays: tickFlags.sinceDays, FullHistory: tickFlags.fullHistory,
		})
		if err != nil {
			return fail(err)
		}
		return output(func() {
			if rep.Mine != nil {
				fmt.Printf("增量挖矿：%d 个 transcript，%d 条候选", rep.Mine.Transcripts, rep.Mine.Candidates)
				if rep.Mine.Batch != "" {
					fmt.Printf("（批次 %s）", rep.Mine.Batch)
				}
				fmt.Println()
				if rep.Mine.SkippedOld > 0 {
					fmt.Printf("跳过 %d 个 %d 天前的 transcript\n", rep.Mine.SkippedOld, tickFlags.sinceDays)
				}
			}
			if rep.DeepDrained > 0 || rep.DeepCandidates > 0 || rep.DeepAbstained > 0 {
				fmt.Printf("深挖排空：%d 段完成，%d 条候选", rep.DeepDrained, rep.DeepCandidates)
				if rep.DeepAbstained > 0 {
					fmt.Printf("，%d 段弃权", rep.DeepAbstained)
				}
				if rep.DeepBatch != "" {
					fmt.Printf("（批次 %s）", rep.DeepBatch)
				}
				fmt.Println()
			}
			if rep.DeepPending > 0 {
				fmt.Printf("待挖余量：%d 段（下次 tick 续挖）\n", rep.DeepPending)
			}
			if rep.Note != "" {
				fmt.Printf("备注: %s\n", rep.Note)
			}
			printRootFooter(st.Root)
		}, rep)
	},
}

// ── tick schedule：OS 调度器接线（launchd / systemd user timer）──────────────

var tickSchedFlags struct {
	every string
}

var tickScheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "OS 调度器接线管理（install/remove/status）",
}

var tickSchedInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "安装闲时 tick 调度（macOS launchd / Linux systemd user timer）",
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := mustStore()
		if err != nil {
			return fail(err)
		}
		bin := scheduleBinPath(st.Root)
		desc, err := doctor.ScheduleInstall(store.HomeDir(), st.Root, bin, tickSchedFlags.every)
		if err != nil {
			return fail(err)
		}
		return output(func() {
			fmt.Println("闲时 tick 调度已安装：")
			for _, d := range desc {
				fmt.Printf("  · %s\n", d)
			}
			printRootFooter(st.Root)
		}, desc)
	},
}

var tickSchedRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "摘除闲时 tick 调度（含台账回放）",
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := mustStore()
		if err != nil {
			return fail(err)
		}
		if err := doctor.ScheduleRemove(store.HomeDir(), st.Root); err != nil {
			return fail(err)
		}
		return output(func() {
			fmt.Println("闲时 tick 调度已摘除")
			printRootFooter(st.Root)
		}, map[string]bool{"removed": true})
	},
}

var tickSchedStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "查看闲时 tick 调度状态",
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := mustStore()
		if err != nil {
			return fail(err)
		}
		stat := doctor.ScheduleStatus(store.HomeDir(), st.Root)
		return output(func() {
			if stat.Installed {
				fmt.Printf("已安装：%s（间隔 %s）\n", stat.File, stat.Interval)
			} else if stat.Note != "" {
				fmt.Println(stat.Note)
			} else {
				fmt.Println("未安装：remin tick schedule install")
			}
			printRootFooter(st.Root)
		}, stat)
	},
}

// scheduleBinPath 调度指向的二进制：优先落位真身（稳定路径），回退当前可执行体
func scheduleBinPath(root string) string {
	stable := doctor.StablePath(root)
	if _, err := os.Stat(stable); err == nil {
		return stable
	}
	self, err := os.Executable()
	if err != nil {
		return stable
	}
	return self
}

func init() {
	tickCmd.Flags().IntVar(&tickFlags.deepMax, "deep-max", miner.DefaultTickDeepMax, "每 tick 最多深挖的 transcript 段数")
	tickCmd.Flags().IntVar(&tickFlags.sinceDays, "since", 7, "仅挖最近 N 天的 transcript（与 mine 同义）")
	tickCmd.Flags().BoolVar(&tickFlags.fullHistory, "full-history", false, "不限时间全量挖（显式关闭 --since）")
	rootCmd.AddCommand(tickCmd)

	tickSchedInstallCmd.Flags().StringVar(&tickSchedFlags.every, "every", "4h", "调度间隔（≥15m，如 30m/1h/4h）")
	tickScheduleCmd.AddCommand(tickSchedInstallCmd)
	tickScheduleCmd.AddCommand(tickSchedRemoveCmd)
	tickScheduleCmd.AddCommand(tickSchedStatusCmd)
	tickCmd.AddCommand(tickScheduleCmd)
}
