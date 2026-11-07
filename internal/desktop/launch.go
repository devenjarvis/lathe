package desktop

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/serve"
)

// Open in editor and Run in Terminal (issue #145), as plain Go so the command
// lines are tested without the desktop tag; shell_launch.go runs them.
//
// Run in Terminal opens the reader's terminal at the workspace with the
// command typed at the prompt and never runs it. How it gets typed depends on
// the platform, the terminal and the reader's shell:
//
//   - macOS Terminal: osascript `do script` types a line into a new window's
//     shell. In zsh (the macOS default) that line is `cd <dir> && clear &&
//     print -rz -- "$(<file)"`, and print -z puts the command in the line
//     editor, unrun. In bash it is `history -s`, so ↑ brings the command back
//     (and it goes on the clipboard). Other shells: the window opens at the
//     folder and the command goes on the clipboard.
//   - iTerm: osascript `write text … newline NO` types a one-line command
//     without Return, in any shell. A multi-line command uses the zsh route,
//     else the clipboard.
//   - Linux terminals: the terminal starts `zsh -i` with ZDOTDIR pointing at a
//     one-use folder whose .zshrc loads the reader's own and then runs
//     print -rz; bash gets an --rcfile that loads ~/.bashrc and then
//     history -s (plus the clipboard). Other shells: clipboard.
//   - Ghostty and WezTerm on macOS, and every Windows terminal: they take no
//     typed text from outside, so the terminal opens at the folder and the
//     command goes on the clipboard. The page says which happened.
//
// The command is never put into a shell command line or a script: it is
// written to a file in the one-use folder (mode 0600 in a 0700 folder), and
// the shell reads it into the prompt as text. Only paths Lathe made, and the
// workspace path the server checked, are quoted into command lines.

// launchStep is one program to start.
type launchStep struct {
	argv []string
	dir  string
	// wait: run to completion and report its error (osascript, open, cmd
	// /c start return at once); otherwise start it and reap it later (a
	// terminal that stays up with its window).
	wait bool
}

// terminalPlan is how to open a terminal.
type terminalPlan struct {
	steps []launchStep
	// files are written into the stage folder (name → content) first.
	files map[string]string
	// clipboard, when set, is put on the clipboard.
	clipboard string
	outcome   serve.TerminalOutcome
}

// launchEnv is what planning needs from the machine.
type launchEnv struct {
	goos string
	// shell is the reader's login shell ($SHELL); "" when unknown.
	shell string
	// stage is a fresh private folder for the zsh/bash start-up files and
	// the command; the shell's start-up deletes it.
	stage string
	// lookPath finds a program on PATH.
	lookPath func(string) (string, error)
}

// shellKind is "zsh", "bash" or "" for the shell path sh. On macOS an unknown
// shell is zsh, the default since 10.15.
func shellKind(goos, sh string) string {
	base := strings.ToLower(filepath.Base(sh))
	base = strings.TrimSuffix(base, ".exe")
	switch {
	case base == "zsh" || strings.HasPrefix(base, "zsh-"):
		return "zsh"
	case base == "bash" || strings.HasPrefix(base, "bash-"):
		return "bash"
	case sh == "" && goos == "darwin":
		return "zsh"
	}
	return ""
}

// absFor reports whether p is an absolute path on goos (planned on any
// platform, so the tests can check every one).
func absFor(goos, p string) bool {
	if goos == "windows" {
		return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') ||
			strings.HasPrefix(p, `\\`)
	}
	return strings.HasPrefix(p, "/")
}

// shQuote single-quotes s for a POSIX shell (zsh and bash included).
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// typedLine is the line a macOS terminal's shell is given (do script, write
// text) to move to dir and put the command at the prompt: print -z in zsh,
// history -s in bash. It returns "" for other shells.
func typedLine(kind, dir, stage string) string {
	file := shQuote(path.Join(stage, "command"))
	cd := "cd " + shQuote(dir) + " && clear && "
	switch kind {
	case "zsh":
		return cd + `print -rz -- "$(<` + file + `)"; rm -rf -- ` + shQuote(stage)
	case "bash":
		return cd + `history -s "$(<` + file + `)"; rm -rf -- ` + shQuote(stage)
	}
	return ""
}

// zshStartup are the one-use ZDOTDIR's files: load the reader's own zsh
// start-up, then put the command at the prompt, then delete the folder.
func zshStartup(stage string) map[string]string {
	q := shQuote(stage)
	return map[string]string{
		".zshenv": "# Lathe (Run in Terminal): load your zsh setup, then type the command in without running it.\n" +
			"_lathe_stage=" + q + "\n" +
			"ZDOTDIR=\"$HOME\"\n" +
			"[[ -f \"$ZDOTDIR/.zshenv\" ]] && source \"$ZDOTDIR/.zshenv\"\n" +
			"_lathe_zdotdir=\"$ZDOTDIR\"\n" +
			"ZDOTDIR=\"$_lathe_stage\"\n",
		".zshrc": "ZDOTDIR=\"$_lathe_zdotdir\"\n" +
			"[[ -f \"$ZDOTDIR/.zshrc\" ]] && source \"$ZDOTDIR/.zshrc\"\n" +
			"print -rz -- \"$(<\"$_lathe_stage/command\")\"\n" +
			"rm -rf -- \"$_lathe_stage\"\n" +
			"unset _lathe_stage _lathe_zdotdir\n",
	}
}

// bashStartup is the one-use --rcfile: the reader's ~/.bashrc, then the
// command as the newest history entry (bash can't pre-fill the prompt).
func bashStartup(stage string) map[string]string {
	q := shQuote(stage)
	return map[string]string{
		"bashrc": "# Lathe (Run in Terminal): load your bash setup; the command is one Up arrow away.\n" +
			"[ -f \"$HOME/.bashrc\" ] && . \"$HOME/.bashrc\"\n" +
			"history -s \"$(< " + q + "/command)\"\n" +
			"rm -rf -- " + q + "\n",
	}
}

const terminalScript = `on run argv
	tell application "Terminal"
		activate
		do script (item 1 of argv)
	end tell
end run`

const itermScript = `on run argv
	tell application "iTerm"
		activate
		set w to (create window with default profile)
		tell current session of w
			write text (item 1 of argv)
			if (count of argv) > 1 then write text (item 2 of argv) newline NO
		end tell
	end tell
end run`

// linuxTerminals is the default order on Linux: the first installed wins.
var linuxTerminals = []string{"gnome-terminal", "konsole", "xfce4-terminal", "kitty", "alacritty", "wezterm", "ghostty", "foot", "xterm"}

// errNotInstalled words a terminal or editor that isn't there.
func errNotInstalled(label, bin string) error {
	return fmt.Errorf("%s isn't installed here (no %s on your PATH). Pick another in Settings › Reading", label, bin)
}

// planTerminal plans opening terminal (a config.Terminals id; "" = the
// platform default) at dir with command typed in (empty: just open it).
func planTerminal(env launchEnv, terminal, dir, command string) (terminalPlan, error) {
	if !absFor(env.goos, dir) || strings.ContainsAny(dir, "\x00\n\r") {
		return terminalPlan{}, errors.New("not a workspace folder")
	}
	if command != "" {
		c, err := serve.CleanCommand(command)
		if err != nil {
			return terminalPlan{}, err
		}
		command = c
	}
	if terminal != "" {
		if _, ok := config.TerminalByID(env.goos, terminal); !ok {
			return terminalPlan{}, fmt.Errorf("unknown terminal %q", terminal)
		}
	}
	switch env.goos {
	case "darwin":
		return planMacTerminal(env, terminal, dir, command)
	case "windows":
		return planWindowsTerminal(env, terminal, dir, command)
	default:
		return planLinuxTerminal(env, terminal, dir, command)
	}
}

func clipboardOnly(p terminalPlan, command string) terminalPlan {
	if command != "" {
		p.clipboard = command
		p.outcome.Clipboard = true
	}
	return p
}

func planMacTerminal(env launchEnv, terminal, dir, command string) (terminalPlan, error) {
	kind := shellKind("darwin", env.shell)
	switch terminal {
	case "", "terminal":
		p := terminalPlan{outcome: serve.TerminalOutcome{App: "Terminal"}}
		line := typedLine(kind, dir, env.stage)
		if command == "" || line == "" {
			p.steps = []launchStep{{argv: []string{"open", "-a", "Terminal", dir}, wait: true}}
			return clipboardOnly(p, command), nil
		}
		p.steps = []launchStep{{argv: []string{"osascript", "-e", terminalScript, line}, wait: true}}
		p.files = map[string]string{"command": command}
		if kind == "zsh" {
			p.outcome.Typed = true
		} else {
			p.outcome.History = true
			p = clipboardOnly(p, command)
		}
		return p, nil
	case "iterm":
		p := terminalPlan{outcome: serve.TerminalOutcome{App: "iTerm"}}
		cd := "cd " + shQuote(dir) + " && clear"
		switch {
		case command == "":
			p.steps = []launchStep{{argv: []string{"osascript", "-e", itermScript, cd}, wait: true}}
		case !strings.Contains(command, "\n"):
			// write text … newline NO: typed, not run, in any shell.
			p.steps = []launchStep{{argv: []string{"osascript", "-e", itermScript, cd, command}, wait: true}}
			p.outcome.Typed = true
		case kind == "zsh" || kind == "bash":
			p.steps = []launchStep{{argv: []string{"osascript", "-e", itermScript, typedLine(kind, dir, env.stage)}, wait: true}}
			p.files = map[string]string{"command": command}
			if kind == "zsh" {
				p.outcome.Typed = true
			} else {
				p.outcome.History = true
				p = clipboardOnly(p, command)
			}
		default:
			p.steps = []launchStep{{argv: []string{"osascript", "-e", itermScript, cd}, wait: true}}
			p = clipboardOnly(p, command)
		}
		return p, nil
	case "ghostty":
		p := terminalPlan{outcome: serve.TerminalOutcome{App: "Ghostty"},
			steps: []launchStep{{argv: []string{"open", "-na", "Ghostty", "--args", "--working-directory=" + dir}, wait: true}}}
		return clipboardOnly(p, command), nil
	case "wezterm":
		p := terminalPlan{outcome: serve.TerminalOutcome{App: "WezTerm"},
			steps: []launchStep{{argv: []string{"open", "-na", "WezTerm", "--args", "start", "--cwd", dir}, wait: true}}}
		return clipboardOnly(p, command), nil
	}
	return terminalPlan{}, fmt.Errorf("unknown terminal %q", terminal)
}

// linuxTerminalArgv is the command line that opens terminal bin at dir
// running cmd (nil: the reader's default shell).
func linuxTerminalArgv(id, bin, dir string, cmd []string) []string {
	var argv []string
	switch id {
	case "gnome-terminal":
		argv = []string{bin, "--working-directory=" + dir}
		if cmd != nil {
			argv = append(argv, "--")
		}
	case "konsole":
		argv = []string{bin, "--workdir", dir}
		if cmd != nil {
			argv = append(argv, "-e")
		}
	case "xfce4-terminal":
		argv = []string{bin, "--working-directory=" + dir}
		if cmd != nil {
			argv = append(argv, "-x")
		}
	case "kitty":
		argv = []string{bin, "--directory", dir}
	case "alacritty":
		argv = []string{bin, "--working-directory", dir}
		if cmd != nil {
			argv = append(argv, "-e")
		}
	case "wezterm":
		argv = []string{bin, "start", "--cwd", dir}
		if cmd != nil {
			argv = append(argv, "--")
		}
	case "ghostty":
		argv = []string{bin, "--working-directory=" + dir}
		if cmd != nil {
			argv = append(argv, "-e")
		}
	case "foot":
		argv = []string{bin, "--working-directory=" + dir}
	default: // xterm: the folder comes from the process's own
		argv = []string{bin}
		if cmd != nil {
			argv = append(argv, "-e")
		}
	}
	return append(argv, cmd...)
}

func planLinuxTerminal(env launchEnv, terminal, dir, command string) (terminalPlan, error) {
	id, bin := terminal, ""
	if id == "" {
		for _, cand := range linuxTerminals {
			if p, err := env.lookPath(cand); err == nil {
				id, bin = cand, p
				break
			}
		}
		if id == "" {
			return terminalPlan{}, errors.New("no terminal app found on your PATH. Pick one in Settings › Reading")
		}
	} else {
		p, err := env.lookPath(id)
		if err != nil {
			return terminalPlan{}, errNotInstalled(config.TerminalLabel("linux", id), id)
		}
		bin = p
	}
	plan := terminalPlan{outcome: serve.TerminalOutcome{App: config.TerminalLabel("linux", id)}}
	var cmd []string
	if command != "" {
		switch shellKind("linux", env.shell) {
		case "zsh":
			plan.files = zshStartup(env.stage)
			plan.files["command"] = command
			cmd = []string{"env", "ZDOTDIR=" + env.stage, env.shell, "-i"}
			plan.outcome.Typed = true
		case "bash":
			plan.files = bashStartup(env.stage)
			plan.files["command"] = command
			cmd = []string{env.shell, "--rcfile", path.Join(env.stage, "bashrc"), "-i"}
			plan.outcome.History = true
			plan = clipboardOnly(plan, command)
		default:
			plan = clipboardOnly(plan, command)
		}
	}
	plan.steps = []launchStep{{argv: linuxTerminalArgv(id, bin, dir, cmd), dir: dir}}
	return plan, nil
}

func planWindowsTerminal(env launchEnv, terminal, dir, command string) (terminalPlan, error) {
	if terminal == "" {
		terminal = "powershell"
		if _, err := env.lookPath("wt.exe"); err == nil {
			terminal = "wt"
		}
	}
	p := terminalPlan{outcome: serve.TerminalOutcome{App: config.TerminalLabel("windows", terminal)}}
	switch terminal {
	case "wt":
		bin, err := env.lookPath("wt.exe")
		if err != nil {
			return terminalPlan{}, errNotInstalled("Windows Terminal", "wt.exe")
		}
		p.steps = []launchStep{{argv: []string{bin, "-d", dir}, dir: dir}}
	case "powershell":
		// start opens a new console window in the inherited folder; no
		// path goes through cmd's parser.
		p.steps = []launchStep{{argv: []string{"cmd.exe", "/c", "start", "", "powershell.exe", "-NoExit"}, dir: dir, wait: true}}
	case "cmd":
		p.steps = []launchStep{{argv: []string{"cmd.exe", "/c", "start", "", "cmd.exe"}, dir: dir, wait: true}}
	default:
		return terminalPlan{}, fmt.Errorf("unknown terminal %q", terminal)
	}
	// Neither console takes typed text from another program.
	return clipboardOnly(p, command), nil
}

// editorPlan is how to open path in editor (a config.Editors id). ok=false
// with no error means "use the system's default app" (editor "").
func planEditor(env launchEnv, editor, path string) (launchStep, bool, error) {
	if !absFor(env.goos, path) || strings.ContainsAny(path, "\x00\n\r") {
		return launchStep{}, false, errors.New("not a workspace path")
	}
	if editor == "" {
		return launchStep{}, false, nil
	}
	e, ok := config.EditorByID(editor)
	if !ok {
		return launchStep{}, false, fmt.Errorf("unknown editor %q", editor)
	}
	if env.goos == "darwin" {
		// An app opened from the Dock has no shell PATH; `open -a` finds
		// the app by name wherever it is installed.
		return launchStep{argv: []string{"open", "-a", e.MacApp, path}, wait: true}, true, nil
	}
	bin, err := env.lookPath(e.Command)
	if err != nil {
		return launchStep{}, false, errNotInstalled(e.Label, e.Command)
	}
	return launchStep{argv: []string{bin, path}}, true, nil
}
