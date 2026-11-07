package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/devenjarvis/lathe/internal/config"

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

var shelfListJSON bool

var shelfListCmd = &cobra.Command{
	Use:   "list",
	Short: "List shelves and how many tutorials each holds",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := config.TutorialsDir()
		if err != nil {
			return err
		}
		list, err := store.ListShelves(dir)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		if shelfListJSON {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			return enc.Encode(list)
		}
		for _, sh := range list {
			_, _ = fmt.Fprintf(out, "%-24s %3d  %s\n", sh.ID, sh.Count, sh.Name)
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

var shelfSetCreate bool

var shelfSetCmd = &cobra.Command{
	Use:   "set <slug> <shelf>",
	Short: "Put a tutorial on a shelf (by id or name; \"unsorted\" takes it off; --create makes a missing shelf)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := config.TutorialsDir()
		if err != nil {
			return err
		}
		sh, created, err := store.SetShelf(dir, args[0], args[1], shelfSetCreate)
		if err != nil {
			return err
		}
		if created {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created shelf %q (%s)\n", sh.Name, sh.ID)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s is on %s\n", args[0], sh.Name)
		return nil
	},
}

// shelfProposeCmd records the agent's pick for one tutorial in the pending
// "Sort my shelves" proposal. Nothing moves: the reader reviews and applies
// the proposal in Settings › Library.
var shelfProposeCmd = &cobra.Command{
	Use:   "propose <slug> <shelf>",
	Short: "Propose a shelf for a tutorial (reviewed in Settings › Library; moves nothing)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := config.TutorialsDir()
		if err != nil {
			return err
		}
		msg, err := store.ProposeShelf(dir, args[0], args[1])
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), msg)
		return nil
	},
}

var shelfSplitGroups []string

// shelfProposeSplitCmd records a proposed two-way split of a shelf. The
// shelf page previews it; nothing moves until the reader applies it.
var shelfProposeSplitCmd = &cobra.Command{
	Use:   "propose-split <shelf> --group \"<name>=<slug>,<slug>\" --group \"<name>=<slug>,…\"",
	Short: "Propose splitting a shelf in two (previewed on the shelf's page; moves nothing)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := config.TutorialsDir()
		if err != nil {
			return err
		}
		var groups []store.SplitGroup
		for _, g := range shelfSplitGroups {
			name, slugs, ok := strings.Cut(g, "=")
			if !ok {
				return fmt.Errorf("--group %q: want <name>=<slug>,<slug>", g)
			}
			groups = append(groups, store.SplitGroup{Name: name, Slugs: store.SplitTags([]string{slugs})})
		}
		msg, err := store.ProposeSplit(dir, args[0], groups)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), msg)
		return nil
	},
}

func init() {
	shelfListCmd.Flags().BoolVar(&shelfListJSON, "json", false, "print JSON (id, name, description, count)")
	shelfSetCmd.Flags().BoolVar(&shelfSetCreate, "create", false, "create the shelf when no shelf has that name")
	shelfProposeSplitCmd.Flags().StringArrayVar(&shelfSplitGroups, "group", nil, "a group as <name>=<slug>,<slug> (pass exactly two)")
	shelfCmd.AddCommand(shelfListCmd, shelfNewCmd, shelfSetCmd, shelfProposeCmd, shelfProposeSplitCmd)
	rootCmd.AddCommand(shelfCmd)
}
