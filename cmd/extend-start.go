package cmd

import (
	"fmt"

	"github.com/devenjarvis/lathe/internal/store"
	"github.com/spf13/cobra"
)

// extendStartCmd reserves the next part and marks the tutorial in-flight. The
// /lathe-extend skill calls it first, reads the printed target filename, writes
// that part, then calls `lathe extend-commit`. The Go binary stays the sole
// writer of durable state (store.ExtendStart, shared with the
// lathe_extend_start MCP tool).
var extendStartCmd = &cobra.Command{
	Use:   "extend-start <slug>",
	Short: "Reserve the next part of a tutorial and mark it extending (used by the /lathe-extend skill)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		tutDir, err := store.TutorialPath(args[0])
		if err != nil {
			return err
		}
		part, err := store.ExtendStart(tutDir)
		if err != nil {
			return err
		}
		// Print only the filename so the skill can capture it cleanly.
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), part)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(extendStartCmd)
}
