package cmd

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/devenjarvis/lathe/internal/store"
	"github.com/spf13/cobra"
)

var rmForce bool

var rmCmd = &cobra.Command{
	Use:   "rm <slug>",
	Short: "Delete a stored tutorial",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug := args[0]
		if !rmForce {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Delete tutorial %q? [y/N]: ", slug)
			reader := bufio.NewReader(cmd.InOrStdin())
			line, _ := reader.ReadString('\n')
			if strings.ToLower(strings.TrimSpace(line)) != "y" {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "aborted")
				return nil
			}
		}
		if err := store.Delete(slug); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Deleted %s\n", slug)
		return nil
	},
}

func init() {
	rmCmd.Flags().BoolVarP(&rmForce, "force", "f", false, "skip confirmation prompt")
	rootCmd.AddCommand(rmCmd)
}
