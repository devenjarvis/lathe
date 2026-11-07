// Package loginenv gives a GUI-launched Lathe the environment a terminal
// launch would have had, so agent detection (internal/agents.Detect, which
// uses exec.LookPath) and the hosted agent launch (internal/acp.Start, which
// runs the command through /bin/sh) find programs like claude-agent-acp,
// codex-acp and npm-installed bins under ~/.nvm or /opt/homebrew/bin.
//
// An app opened from the macOS dock (and from some Linux launchers) inherits
// launchd's minimal PATH (/usr/bin:/bin:/usr/sbin:/sbin), not the one the
// user's shell startup files build. Prepare fixes that in two layers:
//
//  1. the directories the user listed in ~/.lathe/config.json "agent_path"
//     (config.AgentPath), always first: an explicit setting beats anything
//     inferred;
//  2. the login shell's environment, read by running $SHELL as an
//     interactive login shell and printing env between unique markers.
//
// Only PATH (merged) and a short allowlist of other variables (filled in when
// unset, never overridden) are taken from the login shell; see fillVars.
//
// Nothing here may stop the app from starting. Every failure is returned as
// an error for the caller to log, and whatever could be applied (the
// configured dirs) still is.
package loginenv

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/devenjarvis/lathe/internal/config"
)

// DefaultTimeout bounds the whole login-shell probe, fallback included. Slow
// rc files (oh-my-zsh, nvm's eager init) commonly take 0.5–2s; a probe that
// takes longer than this is treated as hung and killed.
const DefaultTimeout = 5 * time.Second

// ResolvingVar is set to "1" in the probed shell's environment so users can
// skip expensive or interactive rc work (`[ -n "$LATHE_RESOLVING_ENVIRONMENT" ]
// && return`), like VS Code's VSCODE_RESOLVING_ENVIRONMENT.
const ResolvingVar = "LATHE_RESOLVING_ENVIRONMENT"

// fillVars are the variables, besides PATH, that Prepare copies from the
// login shell — and only when the process doesn't already have them, so
// anything the launcher set wins. Everything else (prompt settings, shell
// options, terminal variables, the rc files' scratch variables) stays out:
// importing the whole environment would hand the agent a shell session's
// state it never asked for. The list is what an ACP agent and the tools it
// spawns plausibly need and a GUI launch lacks:
//
//   - version managers whose shims or bins need their home to be found
//     (nvm's node is on PATH, but `nvm` and some global installs read
//     NVM_DIR; volta, asdf, pnpm and bun likewise);
//   - network settings a corporate setup puts in an rc file, without which
//     the agent can't reach its model API (proxies, extra CA bundles);
//   - the agents' own API keys, for users who authenticate with an exported
//     key instead of a login. These are the user's own credentials for the
//     user's own agent; Report lists only variable names, never values.
//   - LANG, so tools the agent runs don't fall back to the C locale.
var fillVars = []string{
	"NVM_DIR", "NVM_BIN", "VOLTA_HOME", "ASDF_DIR", "ASDF_DATA_DIR",
	"PNPM_HOME", "BUN_INSTALL",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
	"NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE", "SSL_CERT_DIR",
	"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY",
	"LANG",
}

// Report says what Prepare changed, for the caller's startup log.
type Report struct {
	// Shell is the shell that was probed ("" when the probe didn't run).
	Shell string
	// Interactive is true when the interactive login probe (-i -l)
	// succeeded, false when only the plain login fallback (-l) did.
	Interactive bool
	// Skipped, when non-empty, says why the login shell wasn't probed.
	Skipped string
	// AgentPath is the configured agent_path dirs that were put first.
	AgentPath []string
	// PathAdded lists the PATH entries that weren't there before, in order.
	PathAdded []string
	// Filled names the fillVars variables set from the login shell.
	Filled []string
	// Elapsed is how long the probe took.
	Elapsed time.Duration
}

// String is a one-line summary suitable for a log.
func (r Report) String() string {
	var b strings.Builder
	b.WriteString("loginenv:")
	switch {
	case r.Skipped != "":
		b.WriteString(" login shell skipped (" + r.Skipped + ")")
	case r.Shell != "":
		mode := "-l"
		if r.Interactive {
			mode = "-i -l"
		}
		b.WriteString(" probed " + r.Shell + " " + mode + " in " + r.Elapsed.Round(time.Millisecond).String())
	}
	if len(r.AgentPath) > 0 {
		b.WriteString("; agent_path " + strings.Join(r.AgentPath, string(os.PathListSeparator)))
	}
	if len(r.PathAdded) > 0 {
		b.WriteString("; PATH += " + strings.Join(r.PathAdded, string(os.PathListSeparator)))
	} else {
		b.WriteString("; PATH unchanged")
	}
	if len(r.Filled) > 0 {
		b.WriteString("; set " + strings.Join(r.Filled, ", "))
	}
	return b.String()
}

// Options configures Prepare. The zero value is what the desktop app wants;
// the fields exist so tests (and `lathe serve`) can narrow it.
type Options struct {
	// GOOS overrides runtime.GOOS. The probe is skipped on windows, which
	// has no login shell to ask.
	GOOS string
	// Shell overrides the shell to probe (default $SHELL, then the user's
	// account shell, then /bin/zsh on darwin and /bin/sh elsewhere).
	Shell string
	// Timeout bounds the probe (default DefaultTimeout).
	Timeout time.Duration
	// AgentPath overrides the configured agent_path dirs (nil reads
	// config.AgentPath; use an empty non-nil slice for none).
	AgentPath []string
	// SkipProbe applies only the agent_path dirs. `lathe serve` uses it: a
	// terminal launch already has the login PATH.
	SkipProbe bool

	// LookupEnv and Setenv default to os.LookupEnv and os.Setenv.
	LookupEnv func(string) (string, bool)
	Setenv    func(key, value string) error
}

// Prepare is the desktop app's startup call: it puts the configured
// agent_path dirs and the login shell's PATH (plus fillVars) into this
// process's environment, which every agent detection and agent launch after
// it inherits. Call it once, before the first agents.Detect or acp.Start. A
// non-nil error is for logging only: the Report still says what was applied.
func Prepare(ctx context.Context) (Report, error) {
	return Options{}.Prepare(ctx)
}

// ApplyAgentPath prepends only the configured agent_path dirs to PATH,
// without probing a shell. `lathe serve` calls it before resolving the agent
// so a terminal launch honours the same setting as the desktop app.
func ApplyAgentPath() (Report, error) {
	return Options{SkipProbe: true}.Prepare(context.Background())
}

// Prepare runs with these options; see the package-level Prepare.
func (o Options) Prepare(ctx context.Context) (Report, error) {
	lookup, setenv := o.LookupEnv, o.Setenv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	if setenv == nil {
		setenv = os.Setenv
	}
	goos := o.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	var rep Report
	rep.AgentPath = o.AgentPath
	if rep.AgentPath == nil {
		rep.AgentPath = config.AgentPath()
	}

	var login map[string]string
	var probeErr error
	switch {
	case o.SkipProbe:
		rep.Skipped = "not requested"
	case goos == "windows":
		rep.Skipped = "windows has no login shell"
	default:
		timeout := o.Timeout
		if timeout <= 0 {
			timeout = DefaultTimeout
		}
		rep.Shell = o.Shell
		if rep.Shell == "" {
			rep.Shell = userShell(goos, lookup)
		}
		start := time.Now()
		login, rep.Interactive, probeErr = probeWithFallback(ctx, rep.Shell, timeout)
		rep.Elapsed = time.Since(start)
	}

	current, _ := lookup("PATH")
	var loginPath []string
	if p, ok := login["PATH"]; ok {
		loginPath = filepath.SplitList(p)
	}
	merged, added := MergePath(current, rep.AgentPath, loginPath)
	rep.PathAdded = added
	if merged != current {
		if err := setenv("PATH", merged); err != nil {
			return rep, err
		}
	}
	for _, k := range fillVars {
		v, ok := login[k]
		if !ok || v == "" {
			continue
		}
		if _, set := lookup(k); set {
			continue
		}
		if err := setenv(k, v); err != nil {
			return rep, err
		}
		rep.Filled = append(rep.Filled, k)
	}
	return rep, probeErr
}

// MergePath builds a PATH from the front lists, in order, followed by the
// entries of current that aren't already present. Duplicates (compared after
// filepath.Clean) keep their first position, and empty entries are dropped:
// an empty PATH entry means the current directory, which no launcher meant
// to search. added lists the entries current didn't have.
func MergePath(current string, front ...[]string) (merged string, added []string) {
	have := map[string]bool{}
	for _, e := range filepath.SplitList(current) {
		if e != "" {
			have[filepath.Clean(e)] = true
		}
	}
	seen := map[string]bool{}
	var out []string
	add := func(e string) {
		if e == "" {
			return
		}
		key := filepath.Clean(e)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, e)
		if !have[key] {
			added = append(added, e)
		}
	}
	for _, list := range front {
		for _, e := range list {
			add(e)
		}
	}
	for _, e := range filepath.SplitList(current) {
		add(e)
	}
	return strings.Join(out, string(os.PathListSeparator)), added
}
