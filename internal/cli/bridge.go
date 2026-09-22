package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/remin-dev/remin/internal/core/bridge"
	"github.com/remin-dev/remin/internal/core/config"
	"github.com/remin-dev/remin/internal/core/importer"
	"github.com/remin-dev/remin/internal/core/view"
	"github.com/remin-dev/remin/internal/store"
	"github.com/spf13/cobra"
)

var bridgePullFlags struct {
	from  string
	apply bool
}

// bridgePullJSON pull 的 --json 契约（GUI 可判定 dry-run/apply 与跳过清单）
type bridgePullJSON struct {
	Applied bool             `json:"applied"`
	Pulled  int              `json:"pulled"`
	Skipped []string         `json:"skipped,omitempty"`
	Report  *importer.Report `json:"report"`
}

// bridge pull：平台笔记 → staging markdown → importer markdown-dir 通道
// （human-verified + 幂等指纹）；默认 dry-run 只报将摄取内容。
// staging 落 <root>/transcripts-cache/bridge/<ts>/（gitignored，provenance 引用可解析）
var bridgePullCmd = &cobra.Command{
	Use:   "pull --from notion|feishu [--apply]",
	Short: "从飞书/Notion 拉笔记 → inbox（human-verified 通道；默认 dry-run）",
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := mustStore()
		if err != nil {
			return fail(err)
		}
		cfg, err := config.Load(st.ConfigPath())
		if err != nil {
			return fail(err)
		}
		if bridgePullFlags.from != "notion" && bridgePullFlags.from != "feishu" {
			return fail(fmt.Errorf("--from 必须是 notion 或 feishu"))
		}
		ctx := context.Background()
		var res bridge.PullResult
		switch bridgePullFlags.from {
		case "notion":
			nc := bridge.NotionConfig{Token: os.Getenv("REMIN_NOTION_TOKEN")}
			if cfg.Bridge != nil && cfg.Bridge.Notion != nil {
				nc.Base, nc.ParentPageID = cfg.Bridge.Notion.Base, cfg.Bridge.Notion.ParentPageID
			}
			res, err = bridge.NotionPull(ctx, nc)
		case "feishu":
			fc := bridge.FeishuConfig{AppID: os.Getenv("REMIN_FEISHU_APP_ID"), AppSecret: os.Getenv("REMIN_FEISHU_APP_SECRET")}
			if cfg.Bridge != nil && cfg.Bridge.Feishu != nil {
				fc.Base, fc.WikiSpaceID, fc.PushFolderToken = cfg.Bridge.Feishu.Base, cfg.Bridge.Feishu.WikiSpaceID, cfg.Bridge.Feishu.PushFolderToken
			}
			res, err = bridge.FeishuPull(ctx, fc)
		}
		if err != nil {
			return fail(err)
		}
		stage := filepath.Join(st.Root, "transcripts-cache", "bridge", time.Now().Format("20060102T150405"))
		files, err := bridge.StageMarkdown(stage, res.Docs)
		if err != nil {
			return fail(err)
		}
		rep, err := importer.Import(st, importer.SrcMarkdownDir, stage, bridgePullFlags.apply, store.HomeDir())
		if err != nil {
			return fail(err)
		}
		return output(func() {
			fmt.Printf("拉取 %d 篇 → %d 个 staging 文件", len(res.Docs), len(files))
			if !bridgePullFlags.apply {
				fmt.Print("（dry-run，未写 inbox；--apply 生效）")
			}
			fmt.Println()
			for i, d := range res.Docs {
				if i >= 10 {
					fmt.Printf("  … 共 %d 篇\n", len(res.Docs))
					break
				}
				fmt.Printf("  · %s（%d 字）\n", d.Title, len([]rune(d.Text)))
			}
			for _, s := range res.Skipped {
				fmt.Printf("  ✗ 跳过：%s（拉取失败/无权限——原因为平台侧）\n", s)
			}
			printRootFooter(st.Root)
		}, bridgePullJSON{Applied: bridgePullFlags.apply, Pulled: len(res.Docs), Skipped: res.Skipped, Report: rep})
	},
}

var bridgePushFlags struct {
	to     string
	facet  string
	dryRun bool
}

// bridge push：view 投影 → 平台新页面（单向：只创建，永不回写既有页面）
var bridgePushCmd = &cobra.Command{
	Use:   "push --to notion|feishu [--facet dev] [--dry-run]",
	Short: "把记忆视图单向发布到飞书/Notion（只创建新页面，永不回写）",
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := mustStore()
		if err != nil {
			return fail(err)
		}
		if bridgePushFlags.to != "notion" && bridgePushFlags.to != "feishu" {
			return fail(fmt.Errorf("--to 必须是 notion 或 feishu"))
		}
		cfg, err := config.Load(st.ConfigPath())
		if err != nil {
			return fail(err)
		}
		text, err := view.AGENTS(st, bridgePushFlags.facet)
		if err != nil {
			return fail(err)
		}
		lines := splitLines(text)
		title := "Remin 记忆投影 " + time.Now().Format("2006-01-02 15:04")
		if bridgePushFlags.dryRun {
			return output(func() {
				fmt.Printf("[dry-run] 将发布 %d 行（标题 %q）——去掉 --dry-run 即真实发布\n", len(lines), title)
				printRootFooter(st.Root)
			}, map[string]any{"title": title, "lines": len(lines), "dry_run": true})
		}
		ctx := context.Background()
		var url string
		switch bridgePushFlags.to {
		case "notion":
			nc := bridge.NotionConfig{Token: os.Getenv("REMIN_NOTION_TOKEN")}
			if cfg.Bridge != nil && cfg.Bridge.Notion != nil {
				nc.Base, nc.ParentPageID = cfg.Bridge.Notion.Base, cfg.Bridge.Notion.ParentPageID
			}
			url, err = bridge.NotionPush(ctx, nc, title, lines)
		case "feishu":
			fc := bridge.FeishuConfig{AppID: os.Getenv("REMIN_FEISHU_APP_ID"), AppSecret: os.Getenv("REMIN_FEISHU_APP_SECRET")}
			if cfg.Bridge != nil && cfg.Bridge.Feishu != nil {
				fc.Base, fc.WikiSpaceID, fc.PushFolderToken = cfg.Bridge.Feishu.Base, cfg.Bridge.Feishu.WikiSpaceID, cfg.Bridge.Feishu.PushFolderToken
			}
			url, err = bridge.FeishuPush(ctx, fc, title, lines)
		}
		if err != nil {
			return fail(err)
		}
		return output(func() {
			if bridgePushFlags.to == "feishu" {
				fmt.Printf("已发布（单向新文档，文档 ID）：%s\n", url)
			} else {
				fmt.Printf("已发布（单向新页面）：%s\n", url)
			}
			printRootFooter(st.Root)
		}, map[string]any{"title": title, "url": url, "lines": len(lines)})
	},
}

var bridgeCmd = &cobra.Command{
	Use:   "bridge",
	Short: "飞书/Notion 笔记桥（pull 摄取 human-verified；push 单向发布视图）",
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			out = append(out, line)
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func init() {
	bridgePullCmd.Flags().StringVar(&bridgePullFlags.from, "from", "", "来源: notion | feishu")
	bridgePullCmd.Flags().BoolVar(&bridgePullFlags.apply, "apply", false, "真实写 inbox（默认 dry-run）")
	bridgePushCmd.Flags().StringVar(&bridgePushFlags.to, "to", "", "目标: notion | feishu")
	bridgePushCmd.Flags().StringVar(&bridgePushFlags.facet, "facet", "", "投影 facet（缺省全量）")
	bridgePushCmd.Flags().BoolVar(&bridgePushFlags.dryRun, "dry-run", false, "只报告不发布")
	bridgeCmd.AddCommand(bridgePullCmd)
	bridgeCmd.AddCommand(bridgePushCmd)
	rootCmd.AddCommand(bridgeCmd)
}
