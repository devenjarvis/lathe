package cmd

import (
	"fmt"
	"strings"

	"github.com/devenjarvis/lathe/internal/store"
	"github.com/spf13/cobra"
)

var (
	withVerify    bool
	storeTags     []string
	storeTagsList []string
	storeSources  []string
	storeRepo     string
	storeBranch   string
	storeTools    []string
	storeVoice    string
	storeModel    string
)

var storeCmd = &cobra.Command{
	Use:   "store <path>",
	Short: "Save a tutorial directory to ~/.lathe/tutorials/",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		tut, err := store.Store(args[0], store.StoreOptions{
			Tags:    splitTags(append(storeTags, storeTagsList...)),
			Sources: storeSources,
			Repo:    storeRepo,
			Branch:  storeBranch,
			Tools:   parseTools(storeTools),
			Voice:   storeVoice,
			Model:   storeModel,
		})
		if err != nil {
			return err
		}
		fmt.Printf("Stored: %s (%s)\n", tut.Title, tut.Status)
		if tut.Repo != "" {
			fmt.Printf("Repo: %s", tut.RepoDisplay())
			if tut.RepoBranch != "" {
				fmt.Printf(" (%s)", tut.RepoBranch)
			}
			fmt.Println()
		}
		// Verification runs in the user's interactive Claude Code session via
		// the /lathe-verify skill (no metered headless run), so --verify just
		// surfaces the command to run rather than spawning anything.
		if withVerify {
			fmt.Printf("\nTo verify it, run this in your Claude Code session:\n\n  /lathe-verify %s\n", tut.Slug)
		}
		return nil
	},
}

// splitTags flattens the repeatable --tag flag, also splitting any
// comma-separated values so `--tags "a,b"` and `--tag a --tag b` both work.
// Normalization (trim/lowercase/dedupe) happens in store.NormalizeTags.
func splitTags(raw []string) []string {
	var out []string
	for _, r := range raw {
		out = append(out, strings.Split(r, ",")...)
	}
	return out
}

// parseTools turns repeatable `--tool name:version` values into store.Tools.
// The split is on the first ":" only, so versions containing ":" survive; a
// value with no ":" is recorded as a name with an empty version. Normalization
// (trim/lowercase/dedupe) happens in store.NormalizeTools.
func parseTools(raw []string) []store.Tool {
	var out []store.Tool
	for _, r := range raw {
		name, version, _ := strings.Cut(r, ":")
		out = append(out, store.Tool{Name: name, Version: version})
	}
	return out
}

func init() {
	storeCmd.Flags().BoolVar(&withVerify, "verify", false, "print the command to verify after storing")
	storeCmd.Flags().StringArrayVar(&storeTags, "tag", nil, "tag to attach (repeatable; --tag also accepts comma-separated values)")
	storeCmd.Flags().StringSliceVar(&storeTagsList, "tags", nil, "comma-separated tags (alias for repeated --tag)")
	storeCmd.Flags().StringArrayVar(&storeSources, "source", nil, "URL consulted while researching the tutorial (repeatable; the research trail surfaced as provenance)")
	storeCmd.Flags().StringVar(&storeRepo, "repo", "", "git remote the tutorial was written for (canonicalized to host/org/repo for grouping)")
	storeCmd.Flags().StringVar(&storeBranch, "repo-branch", "", "branch the tutorial targets (only recorded when --repo is set)")
	storeCmd.Flags().StringArrayVar(&storeTools, "tool", nil, "language/tool and version the tutorial targets, as name:version (repeatable)")
	storeCmd.Flags().StringVar(&storeVoice, "voice", "", "writing voice the tutorial was generated in (built-in preset or custom voice name)")
	storeCmd.Flags().StringVar(&storeModel, "model", "", "LLM that authored the tutorial, as a display label (e.g. \"Claude Opus 4.8\"); shown in the reading-page byline")
	rootCmd.AddCommand(storeCmd)
}
