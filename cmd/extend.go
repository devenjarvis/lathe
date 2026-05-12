package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/extend"
	"github.com/devenjarvis/lathe/internal/store"
	"github.com/spf13/cobra"
)

var extendGuidance string

var extendCmd = &cobra.Command{
	Use:   "extend <slug>",
	Short: "Add a new part to an existing tutorial",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug := args[0]
		tutorialsDir, err := config.TutorialsDir()
		if err != nil {
			return err
		}
		tutDir := filepath.Join(tutorialsDir, slug)

		if err := store.PromoteIndexToPart(tutDir); err != nil {
			return fmt.Errorf("promote legacy tutorial: %w", err)
		}

		if err := extend.SpawnExtender(slug, tutDir, extendGuidance); err != nil {
			return err
		}

		tut, _ := store.ReadMetadata(tutDir)
		pendingPart := ""
		if tut != nil {
			pendingPart = tut.PendingPart
		}
		if pendingPart != "" {
			fmt.Printf("Extending tutorial %q with %s (running in background; refresh `lathe serve` to watch)\n", slug, pendingPart)
		} else {
			fmt.Printf("Extending tutorial %q (running in background; refresh `lathe serve` to watch)\n", slug)
		}
		return nil
	},
}

func init() {
	extendCmd.Flags().StringVar(&extendGuidance, "guidance", "", "optional guidance for the next part")
	rootCmd.AddCommand(extendCmd)
}
