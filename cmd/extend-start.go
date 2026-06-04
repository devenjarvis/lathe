package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/extend"
	"github.com/devenjarvis/lathe/internal/store"
	"github.com/spf13/cobra"
)

// extendStartCmd reserves the next part and marks the tutorial in-flight. The
// /lathe-extend skill calls it first, reads the printed target filename, writes
// that part, then calls `lathe extend-commit`. The Go binary stays the sole
// writer of durable state.
var extendStartCmd = &cobra.Command{
	Use:   "extend-start <slug>",
	Short: "Reserve the next part of a tutorial and mark it extending (used by the /lathe-extend skill)",
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

		// Legacy single-part tutorials store index.md; promote it so the series
		// always lives in part-NN.md files before we append.
		if err := store.PromoteIndexToPart(tutDir); err != nil {
			return fmt.Errorf("promote legacy tutorial: %w", err)
		}

		tut, err := store.ReadMetadata(tutDir)
		if err != nil {
			return fmt.Errorf("read metadata for %q: %w", slug, err)
		}
		if tut.Status == store.StatusExtending || tut.Status == store.StatusVerifying {
			return fmt.Errorf("already extending or verifying: status is %q", tut.Status)
		}

		pendingPart := extend.NextPartFilename(tut.Parts)
		tut.Status = store.StatusExtending
		tut.PendingPart = pendingPart
		if err := store.WriteMetadata(tutDir, tut); err != nil {
			return fmt.Errorf("write metadata: %w", err)
		}

		// Print only the filename so the skill can capture it cleanly.
		fmt.Fprintln(cmd.OutOrStdout(), pendingPart)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(extendStartCmd)
}
