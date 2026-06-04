package cmd

import (
	"fmt"
	"strings"

	"github.com/devenjarvis/lathe/internal/store"
	"github.com/spf13/cobra"
)

var (
	withVerify    bool
	storeTags     []string
	storeTagsList []string
)

var storeCmd = &cobra.Command{
	Use:   "store <path>",
	Short: "Save a tutorial directory to ~/.lathe/tutorials/",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		tut, err := store.Store(args[0], splitTags(append(storeTags, storeTagsList...))...)
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

// splitTags flattens the repeatable --tag flag, also splitting any
// comma-separated values so `--tags "a,b"` and `--tag a --tag b` both work.
// Normalization (trim/lowercase/dedupe) happens in store.NormalizeTags.
func splitTags(raw []string) []string {
	var out []string
	for _, r := range raw {
		out = append(out, strings.Split(r, ",")...)
	}
	return out
}

func init() {
	storeCmd.Flags().BoolVar(&withVerify, "verify", false, "print the command to verify after storing")
	storeCmd.Flags().StringArrayVar(&storeTags, "tag", nil, "tag to attach (repeatable; --tag also accepts comma-separated values)")
	storeCmd.Flags().StringSliceVar(&storeTagsList, "tags", nil, "comma-separated tags (alias for repeated --tag)")
	rootCmd.AddCommand(storeCmd)
}
