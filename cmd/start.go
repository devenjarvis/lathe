package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
	"github.com/spf13/cobra"
)

var startNoOpen bool

// startCmd sets the folder where the reader types a tutorial's code (its
// workspace) and opens the tutorial in the running reader. The workspace is
// local reader state (workspace.json beside progress.json), never part of the
// tutorial's metadata. The agent panel's "Workspace" field sets the same
// thing from the browser.
var startCmd = &cobra.Command{
	Use:   "start <slug> [path]",
	Short: "Pick the folder you'll type a tutorial's code in, then open the tutorial",
	Long: "Records [path] (default: the current directory) as the tutorial's workspace, the folder where you type its code.\n" +
		"Lathe creates the folder if needed and runs git init when it is empty. If lathe serve is running, the tutorial opens in your browser.",
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug := args[0]
		if err := validateSlug(slug); err != nil {
			return err
		}
		tutorialsDir, err := config.TutorialsDir()
		if err != nil {
			return err
		}
		tutDir := filepath.Join(tutorialsDir, slug)
		if _, err := store.ReadMetadata(tutDir); err != nil {
			return fmt.Errorf("no stored tutorial %q: %w", slug, err)
		}

		path := "."
		if len(args) == 2 {
			path = args[1]
		}
		path, err = store.ExpandPath(path)
		if err != nil {
			return err
		}
		if path, err = filepath.Abs(path); err != nil {
			return err
		}
		setup, err := store.SetWorkspace(tutDir, path)
		if err != nil {
			return err
		}

		out := cmd.OutOrStdout()
		var did []string
		if setup.Created {
			did = append(did, "created it")
		}
		if setup.GitInit {
			did = append(did, "ran git init")
		}
		note := ""
		if len(did) > 0 {
			note = " (" + strings.Join(did, ", ") + ")"
		}
		_, _ = fmt.Fprintf(out, "Workspace for %s: %s%s\n", slug, setup.Path, note)

		rt, err := config.ReadServeRuntime()
		if err != nil {
			if !os.IsNotExist(err) {
				return err
			}
			_, _ = fmt.Fprintf(out, "Run `lathe serve` to read it, then open /%s/.\n", slug)
			return nil
		}
		url := strings.TrimRight(rt.URL, "/") + "/" + slug + "/"
		if startNoOpen {
			_, _ = fmt.Fprintf(out, "Read it at %s\n", url)
			return nil
		}
		_, _ = fmt.Fprintf(out, "Opening %s\n", url)
		openBrowser(url)
		return nil
	},
}

func init() {
	startCmd.Flags().BoolVar(&startNoOpen, "no-open", false, "print the tutorial's URL instead of opening the browser")
	rootCmd.AddCommand(startCmd)
}
