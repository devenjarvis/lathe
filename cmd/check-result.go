package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/devenjarvis/lathe/internal/store"
	"github.com/spf13/cobra"
)

var checkResultFile string

// checkResultCmd is how the /lathe-check skill records a "Check my work"
// result. Like verify-result it is a write-only state mutation: the skill
// does the model work (running the checkpoint on a scratch copy of the
// learner's code and writing the hint ladder) and this command appends the
// result to the tutorial's checks.jsonl via store.RecordCheck, which the
// lathe_check_result MCP tool shares.
var checkResultCmd = &cobra.Command{
	Use:   "check-result <slug>",
	Short: "Record a \"Check my work\" result for a part (used by the /lathe-check skill)",
	Long: `Record a "Check my work" result. The result is JSON, read from --file
(a path, or - for stdin):

  {"part": "part-02.md", "status": "pass|fail|skipped",
   "confidence": "reference|text", "summary": "...", "on_part": "part-01.md",
   "findings": [{"file": "...", "nudge": "...", "pointer": "...",
                 "where": "...", "reveal": "..."}],
   "divergences": [{"file": "...", "note": "...", "later_part": "part-04.md"}]}`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug := args[0]
		tutDir, err := store.TutorialPath(slug)
		if err != nil {
			return err
		}
		var raw []byte
		switch checkResultFile {
		case "-":
			raw, err = io.ReadAll(cmd.InOrStdin())
		default:
			raw, err = os.ReadFile(checkResultFile)
		}
		if err != nil {
			return fmt.Errorf("read the result: %w", err)
		}
		var res store.CheckResult
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&res); err != nil {
			return fmt.Errorf("the result is not valid JSON for a check: %w", err)
		}
		if err := store.RecordCheck(tutDir, &res); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Recorded check of %s for %q: %s\n", res.Part, slug, res.Status)
		return nil
	},
}

func init() {
	checkResultCmd.Flags().StringVar(&checkResultFile, "file", "", "JSON result to record (a path, or - for stdin; required)")
	checkResultCmd.MarkFlagRequired("file") //nolint:errcheck
	rootCmd.AddCommand(checkResultCmd)
}
