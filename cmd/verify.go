package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
	"github.com/spf13/cobra"
)

// verifyCmd no longer runs verification itself. Verification now happens inside
// the user's interactive coding-agent session via the /lathe-verify skill — the
// binary never drives a model itself (which also keeps work on the user's
// subscription rather than metering a headless run). This command just hands
// off the exact skill invocation to paste.
var verifyCmd = &cobra.Command{
	Use:   "verify <slug>",
	Short: "Print the command to verify a stored tutorial in your coding agent",
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
		if _, err := store.ReadMetadata(tutDir); err != nil {
			return fmt.Errorf("no stored tutorial %q: %w", slug, err)
		}

		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"To verify %q, run this in your coding agent:\n\n  /lathe-verify %s\n", slug, slug)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(verifyCmd)
}
