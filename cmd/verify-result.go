package cmd

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
	"github.com/spf13/cobra"
)

var (
	verifyResultStatus     string
	verifyResultPart       string
	verifyResultFailedStep int
	verifyResultError      string
	verifyResultCheckedAt  string
)

// verifyResultCmd is how the /lathe-verify skill records the outcome of a
// verification run. The skill runs in the user's interactive Claude Code
// session (so it is never metered) and calls this command to mutate durable
// state — keeping the Go binary the sole writer of metadata.json and
// verify-result.json.
var verifyResultCmd = &cobra.Command{
	Use:   "verify-result <slug>",
	Short: "Record the result of verifying a tutorial (used by the /lathe-verify skill)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug := args[0]
		if err := validateSlug(slug); err != nil {
			return err
		}

		status := store.Status(verifyResultStatus)
		switch status {
		case store.StatusVerifying, store.StatusVerified, store.StatusFailed, store.StatusSkipped:
		default:
			return fmt.Errorf("invalid --status %q (want verifying, verified, failed, or skipped)", verifyResultStatus)
		}

		tutorialsDir, err := config.TutorialsDir()
		if err != nil {
			return err
		}
		tutDir := filepath.Join(tutorialsDir, slug)
		tut, err := store.ReadMetadata(tutDir)
		if err != nil {
			return fmt.Errorf("read metadata for %q: %w", slug, err)
		}

		// "verifying" is the in-flight marker the skill sets when it starts, so
		// the web UI shows the spinner. Don't start a verify on top of an
		// in-flight extend.
		if status == store.StatusVerifying {
			if tut.Status == store.StatusExtending {
				return fmt.Errorf("cannot verify %q while it is extending", slug)
			}
			tut.Status = store.StatusVerifying
			return store.WriteMetadata(tutDir, tut)
		}

		// Terminal status: write the result file, then flip metadata.
		checkedAt := verifyResultCheckedAt
		if checkedAt == "" {
			checkedAt = time.Now().UTC().Format(time.RFC3339)
		}
		result := &store.VerifyResult{
			Status:     status,
			Part:       verifyResultPart,
			FailedStep: verifyResultFailedStep,
			Error:      verifyResultError,
			CheckedAt:  checkedAt,
		}
		if err := store.WriteVerifyResult(tutDir, result); err != nil {
			return fmt.Errorf("write verify result: %w", err)
		}
		tut.Status = status
		if err := store.WriteMetadata(tutDir, tut); err != nil {
			return fmt.Errorf("write metadata: %w", err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Recorded %s for %q\n", status, slug)
		return nil
	},
}

// validateSlug guards against empty, dot, and path-separator slugs so a slug
// can never escape ~/.lathe/tutorials/.
func validateSlug(slug string) error {
	if slug == "" || slug == "." || slug == ".." || strings.ContainsAny(slug, `/\`) {
		return fmt.Errorf("invalid slug: %q", slug)
	}
	return nil
}

func init() {
	verifyResultCmd.Flags().StringVar(&verifyResultStatus, "status", "", "verifying, verified, failed, or skipped (required)")
	verifyResultCmd.Flags().StringVar(&verifyResultPart, "part", "", "filename of the part that failed")
	verifyResultCmd.Flags().IntVar(&verifyResultFailedStep, "failed-step", 0, "1-indexed step number that failed")
	verifyResultCmd.Flags().StringVar(&verifyResultError, "error", "", "error message or output to record")
	verifyResultCmd.Flags().StringVar(&verifyResultCheckedAt, "checked-at", "", "RFC3339 timestamp (defaults to now)")
	verifyResultCmd.MarkFlagRequired("status") //nolint:errcheck
	rootCmd.AddCommand(verifyResultCmd)
}
