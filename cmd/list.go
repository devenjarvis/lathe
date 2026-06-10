// cmd/list.go
package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all stored tutorials",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := config.TutorialsDir()
		if err != nil {
			return err
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			fmt.Println("No tutorials yet. Run /lathe in your coding agent to generate one.")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "SLUG\tTITLE\tSTATUS\tPARTS")
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			tut, err := store.ReadMetadata(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			parts := "single"
			if tut.IsSeries() {
				parts = fmt.Sprintf("%d parts", len(tut.Parts))
			}
			badge := statusBadge(tut.Status)
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", tut.Slug, tut.Title, badge, parts)
		}
		return w.Flush()
	},
}

func statusBadge(s store.Status) string {
	switch s {
	case store.StatusUnverified:
		return "" // calm default — no badge, matching the web UI
	case store.StatusVerified:
		return "✅ verified"
	case store.StatusVerifying:
		return "⏳ verifying"
	case store.StatusFailed:
		return "❌ failed"
	case store.StatusSkipped:
		return "⚠️ skipped"
	case store.StatusExtending:
		return "⏳ extending"
	default:
		return string(s)
	}
}

func init() {
	rootCmd.AddCommand(listCmd)
}
