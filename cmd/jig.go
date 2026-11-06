package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/devenjarvis/lathe/internal/jig"
	"github.com/spf13/cobra"
)

// jigCmd is the agent-facing surface of the jig library: what kinds exist
// (list), how to write one (show), and whether what was written will build
// (check). Like `voice show`, list/show are read paths — the skill asks the
// binary for the catalog instead of carrying its own copy, so a new jig is
// available to every agent the moment it ships, with no skill edit.
var jigCmd = &cobra.Command{
	Use:   "jig",
	Short: "Browse and check jigs, the interactive blocks tutorials can embed",
}

var jigListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the available jig kinds and when to use each",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		writeJigList(cmd.OutOrStdout())
		return nil
	},
}

func writeJigList(out io.Writer) {
	_, _ = fmt.Fprintln(out, "Jigs are interactive blocks, written in a part as a fenced block: ```jig <kind>")
	_, _ = fmt.Fprintln(out, "Run `lathe jig show <kind>` for a kind's syntax and rules before using it.")
	_, _ = fmt.Fprintln(out)
	for _, k := range jig.All() {
		_, _ = fmt.Fprintf(out, "%s (%s)\n  %s\n  Good for: %s\n\n", k.Name, k.Label, k.Summary, k.GoodFor)
	}
}

var jigShowCmd = &cobra.Command{
	Use:   "show <kind>",
	Short: "Print a jig kind's syntax, rules, and a complete example",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, ok := jig.Lookup(strings.ToLower(args[0]))
		if !ok {
			var names []string
			for _, k := range jig.All() {
				names = append(names, k.Name)
			}
			return fmt.Errorf("unknown jig kind %q; available: %s", args[0], strings.Join(names, ", "))
		}
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(out, "# jig %s (%s)\n\n%s\n\nGood for: %s\n\n%s\n\nExample:\n\n```jig %s\n%s\n```\n",
			k.Name, k.Label, k.Summary, k.GoodFor, k.Doc, k.Name, k.Example)
		return nil
	},
}

var jigCheckCmd = &cobra.Command{
	Use:   "check <file-or-dir>...",
	Short: "Check every jig in markdown files (or a directory's *.md) and report any that won't build",
	Long: "Parse each ```jig block the way the reading page does and report, with file and line, any that would " +
		"render as an error note instead of a jig. Directories are checked one level deep (a tutorial dir's parts). " +
		"Exits non-zero when any jig has a problem. Run it before `lathe store` and `lathe extend-commit`.",
	Args: cobra.MinimumNArgs(1),
	// The problems are the output; a usage dump after them would bury them.
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		files, err := markdownFiles(args)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		var count, bad int
		for _, f := range files {
			src, err := os.ReadFile(f)
			if err != nil {
				return err
			}
			count += len(jig.Blocks(src))
			for _, p := range jig.Check(src) {
				bad++
				_, _ = fmt.Fprintf(out, "%s:%s\n", f, p)
			}
		}
		if bad > 0 {
			return fmt.Errorf("%d of %d jig(s) won't build; fix them (see `lathe jig show <kind>`) and re-run", bad, count)
		}
		_, _ = fmt.Fprintf(out, "ok: %d jig(s) in %d file(s)\n", count, len(files))
		return nil
	},
}

// markdownFiles expands args into a sorted list of .md files: files as given,
// directories one level deep.
func markdownFiles(args []string) ([]string, error) {
	var files []string
	for _, a := range args {
		info, err := os.Stat(a)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			files = append(files, a)
			continue
		}
		matches, err := filepath.Glob(filepath.Join(a, "*.md"))
		if err != nil {
			return nil, err
		}
		sort.Strings(matches)
		files = append(files, matches...)
	}
	return files, nil
}

func init() {
	jigCmd.AddCommand(jigListCmd, jigShowCmd, jigCheckCmd)
	rootCmd.AddCommand(jigCmd)
}
