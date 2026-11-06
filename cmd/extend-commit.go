package cmd

import (
	"fmt"

	"github.com/devenjarvis/lathe/internal/store"
	"github.com/spf13/cobra"
)

// extendCommitCmd records a newly-written part. The /lathe-extend skill calls
// it after writing the part file: it appends the part to metadata, clears the
// pending marker, and resets status to unverified (extending no longer
// auto-chains to verification — that is a separate interactive step). The
// mutation is store.ExtendCommit, shared with the lathe_extend_commit MCP tool.
var extendCommitCmd = &cobra.Command{
	Use:   "extend-commit <slug> <part-file>",
	Short: "Record a newly written tutorial part (used by the /lathe-extend skill)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug, partFile := args[0], args[1]
		tutDir, err := store.TutorialPath(slug)
		if err != nil {
			return err
		}
		if err := store.ExtendCommit(tutDir, partFile, parseSources(extendCommitSources), extendCommitModel); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Added %s to %q\n", partFile, slug)
		return nil
	},
}

var (
	extendCommitSources []string
	extendCommitModel   string
)

func init() {
	extendCommitCmd.Flags().StringArrayVar(&extendCommitSources, "source", nil, "source consulted while researching this part, as URL or \"Title|URL\" (repeatable; appended to the tutorial's research trail)")
	extendCommitCmd.Flags().StringVar(&extendCommitModel, "model", "", "LLM authoring this part, as a display label (e.g. \"Claude Opus 4.8\"); refreshes the tutorial's byline when set")
	rootCmd.AddCommand(extendCommitCmd)
}
