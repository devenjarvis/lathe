package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
	"github.com/spf13/cobra"
)

// verifyCancelCmd recovers a tutorial whose verification was interrupted. The
// /lathe-verify skill marks the tutorial "verifying" when it starts, but if the
// session dies before recording a terminal status (token limit, accidental
// cancellation, or a verify started by mistake), the tutorial is stuck
// with the badge spinning forever and the web verify endpoint refuses to
// re-run. This command resets the status without hand-editing metadata.json or
// manually forcing verify-result to fail to fail with appropriate arguments.
var verifyCancelCmd = &cobra.Command{
	Use:   "verify-cancel <slug>",
	Short: "Cancel an interrupted verification and restore the previous status",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug := args[0]
		if err := validateSlug(slug); err != nil {
			return err
		}
		tutorialsDir, err := config.TutorialsDir()
		if err != nil {
			return err
		}
		tutDir := filepath.Join(tutorialsDir, slug)
		tut, err := store.ReadMetadata(tutDir)
		if err != nil {
			return fmt.Errorf("no stored tutorial %q: %w", slug, err)
		}

		if tut.Status != store.StatusVerifying {
			return fmt.Errorf("%q is not verifying (status: %s) — nothing to cancel", slug, tut.Status)
		}

		// Cancellation is not a result, so verify-result.json is never written
		// or deleted so the last completed run's record stays intact.
		tut.Status = store.StatusUnverified
		if prior, err := store.ReadVerifyResult(tutDir); err == nil {
			tut.Status = prior.Status
		}
		if err := store.WriteMetadata(tutDir, tut); err != nil {
			return fmt.Errorf("write metadata: %w", err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Cancelled verification of %q (status: %s)\n", slug, tut.Status)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(verifyCancelCmd)
}
