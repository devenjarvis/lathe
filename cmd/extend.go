package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
	"github.com/spf13/cobra"
)

var extendGuidance string

// extendCmd no longer runs generation itself. Adding a part now happens inside
// the user's interactive coding-agent session via the /lathe-extend skill — the
// binary never drives a model itself (which also keeps work on the user's
// subscription rather than metering a headless run). This command just hands
// off the exact skill invocation to paste.
var extendCmd = &cobra.Command{
	Use:   "extend <slug>",
	Short: "Print the command to add a new part to a tutorial in your coding agent",
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

		handoff := "/lathe-extend " + slug
		if extendGuidance != "" {
			handoff += " " + extendGuidance
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"To add a new part to %q, run this in your coding agent:\n\n  %s\n", slug, handoff)
		return nil
	},
}

func init() {
	extendCmd.Flags().StringVar(&extendGuidance, "guidance", "", "optional guidance for the next part")
	rootCmd.AddCommand(extendCmd)
}
