package cmd

import (
	"context"
	"os"
	"runtime"

	"github.com/devenjarvis/lathe/internal/buildinfo"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:     "lathe",
	Short:   "Generate and manage hands-on technical tutorials",
	Version: buildinfo.Resolve(),
}

func init() {
	// rootCmd.Version (above) enables cobra's --version flag; the template
	// controls what it prints. buildinfo.String() folds in commit/date when
	// they were stamped at build time, so --version shows the full provenance.
	rootCmd.SetVersionTemplate("lathe " + buildinfo.String() + "\n")
}

// Execute runs the CLI. Bare `lathe` opens the desktop app in a desktop build
// with a display (see app.go); rootCmd itself stays non-runnable so its help
// and usage read exactly as they always have.
func Execute() {
	args := launchArgs(runtime.GOOS, os.Args[1:])
	if err := execute(context.Background(), args, launcher, os.Stdout, os.Stderr); err != nil {
		os.Exit(1)
	}
}
