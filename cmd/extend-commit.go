package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
	"github.com/spf13/cobra"
)

// extendCommitCmd records a newly-written part. The /lathe-extend skill calls
// it after writing the part file: it appends the part to metadata, clears the
// pending marker, and resets status to unverified (extending no longer
// auto-chains to verification — that is a separate interactive step).
var extendCommitCmd = &cobra.Command{
	Use:   "extend-commit <slug> <part-file>",
	Short: "Record a newly written tutorial part (used by the /lathe-extend skill)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug, partFile := args[0], args[1]
		if err := validateSlug(slug); err != nil {
			return err
		}
		if err := validateSlug(partFile); err != nil {
			return fmt.Errorf("invalid part file: %w", err)
		}
		tutorialsDir, err := config.TutorialsDir()
		if err != nil {
			return err
		}
		tutDir := filepath.Join(tutorialsDir, slug)

		if _, err := os.Stat(filepath.Join(tutDir, partFile)); err != nil {
			return fmt.Errorf("part file %q not found: %w", partFile, err)
		}

		tut, err := store.ReadMetadata(tutDir)
		if err != nil {
			return fmt.Errorf("read metadata for %q: %w", slug, err)
		}

		// Idempotent: don't double-append if the skill re-runs the commit.
		found := false
		for _, p := range tut.Parts {
			if p == partFile {
				found = true
				break
			}
		}
		if !found {
			tut.Parts = append(tut.Parts, partFile)
		}
		tut.PendingPart = ""
		tut.Status = store.StatusUnverified
		if err := store.WriteMetadata(tutDir, tut); err != nil {
			return fmt.Errorf("write metadata: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Added %s to %q\n", partFile, slug)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(extendCommitCmd)
}
