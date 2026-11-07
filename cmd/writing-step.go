package cmd

import (
	"fmt"

	"github.com/devenjarvis/lathe/internal/store"
	"github.com/spf13/cobra"
)

var (
	writingStepSources []string
	writingStepAsk     string
	writingStepReplies []string
)

// writingStepCmd records a hosted new-tutorial run's progress in its
// reservation (store.RecordWriting, shared with the lathe_progress MCP tool):
// the step the /lathe skill is on, the sources it has read, and the short
// label and quick replies of a question it is about to ask. lathe serve shows
// them on the "Being written" cover and the progress page. It only applies to
// a tutorial Lathe reserved (status writing).
var writingStepCmd = &cobra.Command{
	Use:   "writing-step <slug> [versions|research|write|shelve]",
	Short: "Report progress on a new tutorial the hosted agent is writing (used by the /lathe skill)",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug := args[0]
		if err := validateSlug(slug); err != nil {
			return err
		}
		var step string
		if len(args) == 2 {
			step = args[1]
		}
		tutDir, err := store.TutorialPath(slug)
		if err != nil {
			return err
		}
		w, err := store.RecordWriting(tutDir, store.WritingUpdate{
			Step: step, Sources: parseSources(writingStepSources), Ask: writingStepAsk, Replies: writingStepReplies,
		})
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Progress for %q: step %s, %d source(s)\n", slug, orDash(w.Step), len(w.Sources))
		return nil
	},
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func init() {
	writingStepCmd.Flags().StringArrayVar(&writingStepSources, "source", nil, "a source you read, as URL or \"Title|URL\" (repeatable)")
	writingStepCmd.Flags().StringVar(&writingStepAsk, "ask", "", "short label of the question you are about to ask, e.g. \"confirm versions\"")
	writingStepCmd.Flags().StringArrayVar(&writingStepReplies, "reply", nil, "a quick reply for that question (repeatable, at most 4)")
	rootCmd.AddCommand(writingStepCmd)
}
