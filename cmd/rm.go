package cmd

import (
	"bufio"
	"fmt"
	"strings"
	"time"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
	"github.com/spf13/cobra"
)

var (
	rmForce bool
	rmNow   bool
)

var rmCmd = &cobra.Command{
	Use:   "rm <slug>",
	Short: "Move a stored tutorial to the Trash (--now deletes it for good)",
	Long: `Move a stored tutorial to the Trash (~/.lathe/trash). It can be restored
from the Trash in lathe serve (Settings › Library › Show Trash) until the
retention setting passes (trash_days: 7, 30 or never; 30 by default).

With --now the tutorial is deleted permanently instead, and its shelf
placement is forgotten.

Each run also deletes Trash entries older than the retention setting.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug := args[0]
		dir, err := config.TutorialsDir()
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		purgeTrash(cmd, dir)
		if rmNow && !rmForce {
			_, _ = fmt.Fprintf(out, "Delete tutorial %q permanently? This can't be undone. [y/N]: ", slug)
			line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
			if strings.ToLower(strings.TrimSpace(line)) != "y" {
				_, _ = fmt.Fprintln(out, "aborted")
				return nil
			}
		}
		if rmNow {
			if err := store.DeleteNow(dir, slug); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "Deleted %s permanently\n", slug)
			return nil
		}
		// Moving to the Trash is undoable, so it needs no confirmation.
		if _, err := store.MoveToTrash(dir, slug, time.Now()); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "Moved %s to the Trash (restore it from Settings › Library › Show Trash in lathe serve)\n", slug)
		return nil
	},
}

// purgeTrash deletes Trash entries past the retention setting. Best effort:
// a failure is a warning, never a reason to fail the command.
func purgeTrash(cmd *cobra.Command, dir string) {
	n, err := store.PurgeTrash(dir, config.TrashDays(), time.Now())
	if err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not empty old Trash entries: %v\n", err)
	}
	switch {
	case n == 1:
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Deleted 1 expired Trash entry")
	case n > 1:
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Deleted %d expired Trash entries\n", n)
	}
}

func init() {
	rmCmd.Flags().BoolVarP(&rmForce, "force", "f", false, "skip the confirmation prompt (with --now)")
	rmCmd.Flags().BoolVar(&rmNow, "now", false, "delete permanently instead of moving to the Trash")
	rootCmd.AddCommand(rmCmd)
}
