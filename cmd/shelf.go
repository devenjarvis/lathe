package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/devenjarvis/lathe/internal/store"
	"github.com/spf13/cobra"
)

// shelfCmd manages the library's shelves (~/.lathe/shelves.json): reader
// state, like a tutorial's workspace, that the web list page groups by. A
// tutorial sits on exactly one shelf; without one it is Unsorted.
var shelfCmd = &cobra.Command{
	Use:   "shelf",
	Short: "List shelves, create one, or put a tutorial on a shelf",
}

var shelfListCmd = &cobra.Command{
	Use:   "list",
	Short: "List shelves and how many tutorials each holds",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := store.DefaultShelvesPath()
		if err != nil {
			return err
		}
		s, err := store.ReadShelves(path)
		if err != nil {
			return err
		}
		counts := map[string]int{}
		for _, slug := range storedSlugs(filepath.Dir(path)) {
			counts[s.ShelfOf(slug).ID]++
		}
		out := cmd.OutOrStdout()
		for _, sh := range append(append([]store.Shelf{}, s.Shelves...), store.UnsortedShelf()) {
			_, _ = fmt.Fprintf(out, "%-24s %3d  %s\n", sh.ID, counts[sh.ID], sh.Name)
		}
		return nil
	},
}

var shelfNewCmd = &cobra.Command{
	Use:   "new <name>",
	Short: "Create a shelf",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := store.DefaultShelvesPath()
		if err != nil {
			return err
		}
		var sh store.Shelf
		if _, err := store.UpdateShelves(path, func(s *store.Shelves) error {
			sh, err = s.Create(args[0])
			return err
		}); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created shelf %q (%s)\n", sh.Name, sh.ID)
		return nil
	},
}

var shelfSetCmd = &cobra.Command{
	Use:   "set <slug> <shelf>",
	Short: "Put a tutorial on a shelf (by id or name; \"unsorted\" takes it off)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug := args[0]
		tutDir, err := store.TutorialPath(slug)
		if err != nil {
			return err
		}
		if _, err := store.ReadMetadata(tutDir); err != nil {
			return fmt.Errorf("no tutorial %q", slug)
		}
		path, err := store.DefaultShelvesPath()
		if err != nil {
			return err
		}
		var sh store.Shelf
		if _, err := store.UpdateShelves(path, func(s *store.Shelves) error {
			var ok bool
			if sh, ok = s.Find(args[1]); !ok {
				return fmt.Errorf("no shelf %q (see lathe shelf list, or create it with lathe shelf new)", args[1])
			}
			return s.Assign(slug, sh.ID)
		}); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s is on %s\n", slug, sh.Name)
		return nil
	},
}

// storedSlugs lists the tutorial dirs under ~/.lathe/tutorials.
func storedSlugs(latheDir string) []string {
	entries, err := os.ReadDir(filepath.Join(latheDir, "tutorials"))
	if err != nil {
		return nil
	}
	var slugs []string
	for _, e := range entries {
		if e.IsDir() {
			slugs = append(slugs, e.Name())
		}
	}
	return slugs
}

func init() {
	shelfCmd.AddCommand(shelfListCmd, shelfNewCmd, shelfSetCmd)
	rootCmd.AddCommand(shelfCmd)
}
