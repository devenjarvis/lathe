package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/devenjarvis/lathe/internal/store"
	"github.com/spf13/cobra"
)

var (
	verifyResultStatus     string
	verifyResultPart       string
	verifyResultFailedStep int
	verifyResultError      string
	verifyResultCheckedAt  string
	verifyResultCode       string
)

// verifyResultCmd is how the /lathe-verify skill records the outcome of a
// verification run. The skill runs in the user's interactive coding-agent
// session and calls this command (or the lathe_verify_result MCP tool, which
// shares store.RecordVerify) to mutate durable state — keeping the Go binary
// the sole writer of metadata.json and verify-result.json.
var verifyResultCmd = &cobra.Command{
	Use:   "verify-result <slug>",
	Short: "Record the result of verifying a tutorial (used by the /lathe-verify skill)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug := args[0]
		tutDir, err := store.TutorialPath(slug)
		if err != nil {
			return err
		}
		status := store.Status(verifyResultStatus)
		if err := store.RecordVerify(tutDir, store.VerifyRecord{
			Status:     status,
			Part:       verifyResultPart,
			FailedStep: verifyResultFailedStep,
			Error:      verifyResultError,
			CheckedAt:  verifyResultCheckedAt,
			Code:       verifyResultCode,
		}); err != nil {
			return err
		}
		if status == store.StatusVerifying {
			return nil
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Recorded %s for %q\n", status, slug)

		// Save the snapshot after the status is recorded, so a snapshot problem
		// can never strand the badge at "verifying".
		if verifyResultCode != "" {
			if err := store.SaveCode(tutDir, verifyResultCode); err != nil {
				return fmt.Errorf("recorded %s, but could not save the code snapshot: %w", status, err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Saved code snapshot to %s\n", filepath.Join(tutDir, store.CodeDirName))
		}
		return nil
	},
}

// validateSlug guards against empty, dot, and path-separator slugs so a slug
// can never escape ~/.lathe/tutorials/.
func validateSlug(slug string) error { return store.ValidateSlug(slug) }

func init() {
	verifyResultCmd.Flags().StringVar(&verifyResultStatus, "status", "", "verifying, verified, failed, or skipped (required)")
	verifyResultCmd.Flags().StringVar(&verifyResultPart, "part", "", "filename of the part that failed")
	verifyResultCmd.Flags().IntVar(&verifyResultFailedStep, "failed-step", 0, "1-indexed step number that failed")
	verifyResultCmd.Flags().StringVar(&verifyResultError, "error", "", "error message or output to record")
	verifyResultCmd.Flags().StringVar(&verifyResultCheckedAt, "checked-at", "", "RFC3339 timestamp (defaults to now)")
	verifyResultCmd.Flags().StringVar(&verifyResultCode, "code", "", "git repo of the reconstructed code, one tagged commit per part (part-01, ...), to keep as the tutorial's code snapshot")
	verifyResultCmd.MarkFlagRequired("status") //nolint:errcheck
	rootCmd.AddCommand(verifyResultCmd)
}
