package cmd

import (
	"fmt"

	"github.com/devenjarvis/lathe/internal/store"
	"github.com/spf13/cobra"
)

var withVerify bool

var storeCmd = &cobra.Command{
	Use:   "store <path>",
	Short: "Save a tutorial directory to ~/.lathe/tutorials/",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		tut, err := store.Store(args[0])
		if err != nil {
			return err
		}
		fmt.Printf("Stored: %s (%s)\n", tut.Title, tut.Status)
		// Verification runs in the user's interactive Claude Code session via
		// the /lathe-verify skill (no metered headless run), so --verify just
		// surfaces the command to run rather than spawning anything.
		if withVerify {
			fmt.Printf("\nTo verify it, run this in your Claude Code session:\n\n  /lathe-verify %s\n", tut.Slug)
		}
		return nil
	},
}

func init() {
	storeCmd.Flags().BoolVar(&withVerify, "verify", false, "print the command to verify after storing")
	rootCmd.AddCommand(storeCmd)
}
