package cli

import (
	"fmt"

	"github.com/remin-dev/remin/internal/index"
	"github.com/remin-dev/remin/internal/search"
	"github.com/remin-dev/remin/internal/store"
	"github.com/spf13/cobra"
)

var searchFlags struct {
	facet string
	topK  int
}

// search 确定性 BM25 检索（trust/provenance 随行；低置信弃权）
var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "检索记忆（确定性 BM25；低置信主动弃权）",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := mustStore()
		if err != nil {
			return fail(err)
		}
		v, err := st.Version()
		if err != nil {
			return fail(err)
		}
		idx, err := index.Ensure(st, v)
		if err != nil {
			return fail(err)
		}
		res := search.New(idx).Search(joinArgs(args), search.Options{
			Facet: searchFlags.facet, TopK: searchFlags.topK,
		})
		return output(func() {
			if res.Abstained {
				fmt.Printf("（弃权：%s——宁可不知道，不能自信地错）\n", abstainLabel(res.Reason))
				printRootFooter(st.Root)
				return
			}
			fmt.Printf("命中 %d 条（索引 v%d）：\n", len(res.Hits), res.IndexVersion)
			for i, h := range res.Hits {
				fmt.Printf("%d. %s [%s/%s·%s] %.2f\n", i+1, h.ID, h.Type, h.Facet, store.TrustLabel(h.Trust), h.Score)
				fmt.Printf("   %s\n", oneLine(h.Content))
				fmt.Printf("   来源: %s（%s）\n", h.Provenance.Origin, h.Provenance.Ref)
			}
			printRootFooter(st.Root)
		}, res)
	},
}

func joinArgs(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}

func abstainLabel(reason string) string {
	if reason == "low_confidence" {
		return "置信不足"
	}
	return "无相关记忆"
}

func init() {
	searchCmd.Flags().StringVar(&searchFlags.facet, "facet", "", "分面过滤（dev/work/life…）")
	searchCmd.Flags().IntVar(&searchFlags.topK, "top-k", 8, "返回条数")
	rootCmd.AddCommand(searchCmd)
}
