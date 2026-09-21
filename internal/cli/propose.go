package cli

import (
	"fmt"
	"strings"

	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/store"
	"github.com/spf13/cobra"
)

var propFlags struct {
	type_           string
	facet           string
	context         []string
	origin          string
	ref             string
	quote           string
	ephemeral       bool
	verifyCondition string
}

// propose 显式记忆提案（FR-CAP-4「现在就记」）：人面通道 → inbox 待审
var proposeCmd = &cobra.Command{
	Use:   "propose <content>",
	Short: "立即记忆一条内容（进 inbox 待人审采纳）",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := mustStore()
		if err != nil {
			return fail(err)
		}
		in := inbox.New(st)
		body := strings.Join(args, " ")
		mtype := propFlags.type_
		if mtype == "" {
			mtype = store.TypeSemantic
		}
		if !store.ValidTypes[mtype] {
			return fail(fmt.Errorf("非法 type: %s", mtype))
		}
		now := store.NowTime()
		cand := &inbox.Candidate{}
		cand.Type = mtype
		cand.Facet = propFlags.facet
		if cand.Facet == "" {
			cand.Facet = "dev"
		}
		cand.Context = propFlags.context
		cand.Status = store.StatusCandidate
		cand.CapturedAt = now
		cand.ReviewedAt = store.TimeUnknown
		cand.Modified = now
		cand.Trust = store.TrustHumanVerified // 人面通道：人写的字
		cand.Source = store.SourceHuman
		cand.Provenance = store.Provenance{
			Origin: defaultStr(propFlags.origin, "human-cli"),
			Ref:    defaultStr(propFlags.ref, "remin propose"),
			Quote:  truncate(defaultStr(propFlags.quote, body), 400),
		}
		if propFlags.ephemeral {
			cand.Expires = "7d"
		}
		if propFlags.verifyCondition != "" {
			cand.Verify = &store.Verify{Condition: propFlags.verifyCondition, Result: store.VerifyUnknown}
		}
		cand.Version = store.FormatVersion
		cand.Body = body

		batchID, ids, err := in.AddBatch("manual", []*inbox.Candidate{cand})
		if err != nil {
			return fail(err)
		}
		return output(func() {
			fmt.Printf("已入 inbox（批次 %s，候选 %s）。人审生效: remin promote --id %s\n", batchID, ids[0], ids[0])
		}, map[string]interface{}{"batch": batchID, "candidate": ids[0]})
	},
}

func defaultStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func init() {
	proposeCmd.Flags().StringVar(&propFlags.type_, "type", "", "记忆类型（episodic/semantic/procedural/preference/decision）")
	proposeCmd.Flags().StringVar(&propFlags.facet, "facet", "dev", "所属分面（dev/work/life/自定义）")
	proposeCmd.Flags().StringSliceVar(&propFlags.context, "context", nil, "上下文标签（逗号分隔，如项目名）")
	proposeCmd.Flags().StringVar(&propFlags.origin, "origin", "", "来源系统（如 claude-code）")
	proposeCmd.Flags().StringVar(&propFlags.ref, "ref", "", "原始定位（会话 ID+行号 / 文档 ID）")
	proposeCmd.Flags().StringVar(&propFlags.quote, "quote", "", "原文摘录（缺省用 content）")
	proposeCmd.Flags().BoolVar(&propFlags.ephemeral, "ephemeral", false, "临时记忆（7 天自动过期）")
	proposeCmd.Flags().StringVar(&propFlags.verifyCondition, "verify-condition", "", "失效条件（path-exists:<p> / file-contains:<p>::<t> / 自然语言待人判）")
	rootCmd.AddCommand(proposeCmd)
}
