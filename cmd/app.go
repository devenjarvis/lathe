package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/devenjarvis/lathe/internal/desktop"
	"github.com/devenjarvis/lathe/internal/loginenv"
	"github.com/spf13/cobra"
)

var appAgent string

var appCmd = &cobra.Command{
	Use:   "app",
	Short: "Open the desktop app (the reader with your agent alongside)",
	Long: "Open the Lathe desktop app: the reader `lathe serve` shows in a browser, in its own window, " +
		"with your coding agent in the agent panel. Running `lathe` with no arguments does the same " +
		"in a desktop build. While it runs, `lathe work`, `lathe start` and `lathe verify-cancel` reach it " +
		"like they reach `lathe serve`.",
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return launcher.open(cmd.Context(), appOptions(appAgent))
	},
}

// appPrepareEnv runs before the app resolves and starts the agent. A dock or
// Start-menu launch inherits no shell PATH, so it loads the login shell's
// environment and the configured agent_path dirs (internal/loginenv), and
// logs what changed. Its error is logged by the app, never fatal.
var appPrepareEnv = func(ctx context.Context) error {
	rep, err := loginenv.Prepare(ctx)
	_, _ = fmt.Fprintln(os.Stderr, "lathe:", rep.String())
	return err
}

func appOptions(agent string) desktop.Options {
	return desktop.Options{Agent: agent, PrepareEnv: appPrepareEnv}
}

// appLauncher is how cmd reaches the desktop app, injectable so the decision
// logic is testable in a build without the desktop tag.
type appLauncher struct {
	available func() bool
	display   func() (bool, string)
	run       func(context.Context, desktop.Options) error
}

var launcher = appLauncher{
	available: desktop.Available,
	display:   func() (bool, string) { return desktop.HasDisplay(os.Getenv) },
	run:       desktop.Run,
}

// open is `lathe app`: open the app or say why it can't.
func (l appLauncher) open(ctx context.Context, opts desktop.Options) error {
	if !l.available() {
		return desktop.ErrNotBuilt
	}
	if ok, why := l.display(); !ok {
		return fmt.Errorf("can't open the desktop app: %s; run `lathe serve` and open it in a browser instead", why)
	}
	return l.run(ctx, opts)
}

// bareOpens reports whether `lathe` with no arguments should open the app
// rather than print help: only in a desktop build with a usable display. A
// desktop build without one says why on stderr, then help prints as usual.
func (l appLauncher) bareOpens(stderr io.Writer) bool {
	if !l.available() {
		return false
	}
	if ok, why := l.display(); !ok {
		_, _ = fmt.Fprintf(stderr, "lathe: not opening the desktop app: %s.\n", why)
		return false
	}
	return true
}

// execute is Execute's body: bare `lathe` opens the app when it can, and
// everything else (help included) goes to cobra unchanged.
func execute(ctx context.Context, args []string, l appLauncher, stdout, stderr io.Writer) error {
	if len(args) == 0 && l.bareOpens(stderr) {
		if err := l.run(ctx, appOptions("")); err != nil {
			_, _ = fmt.Fprintln(stderr, "Error:", err)
			return err
		}
		return nil
	}
	rootCmd.SetArgs(args)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	return rootCmd.ExecuteContext(ctx)
}

// launchArgs drops the "-psn_<n>_<n>" process serial number that older macOS
// releases pass to an app bundle opened from Finder, so a dock launch counts
// as bare `lathe` instead of failing on an unknown flag.
func launchArgs(goos string, args []string) []string {
	if goos != "darwin" {
		return args
	}
	out := args[:0:0]
	for _, a := range args {
		if strings.HasPrefix(a, "-psn_") {
			continue
		}
		out = append(out, a)
	}
	return out
}

func init() {
	appCmd.Flags().StringVar(&appAgent, "agent", "", "command that starts your coding agent's ACP server (as for `lathe serve --agent`); default: the agent picked in the agent panel")
	rootCmd.AddCommand(appCmd)
}
