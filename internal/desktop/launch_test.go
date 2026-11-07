package desktop

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// fakePath finds only the named programs.
func fakePath(have ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		if slices.Contains(have, name) {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
}

func testEnv(goos, sh string, have ...string) launchEnv {
	return launchEnv{goos: goos, shell: sh, stage: "/tmp/lathe-run-1", lookPath: fakePath(have...)}
}

// A hostile command: it must only ever reach a file, never a command line.
const evil = `echo "$(rm -rf ~)" '; rm -rf / #` + "`id`"

func argvHasCommand(p terminalPlan, cmd string) bool {
	for _, st := range p.steps {
		for _, a := range st.argv {
			if strings.Contains(a, cmd) {
				return true
			}
		}
	}
	return false
}

func TestShellKind(t *testing.T) {
	for _, c := range []struct{ goos, sh, want string }{
		{"darwin", "/bin/zsh", "zsh"}, {"darwin", "", "zsh"}, {"linux", "", ""},
		{"linux", "/usr/bin/bash", "bash"}, {"linux", "/usr/bin/fish", ""}, {"darwin", "/opt/homebrew/bin/bash-5.2", "bash"},
	} {
		if got := shellKind(c.goos, c.sh); got != c.want {
			t.Errorf("shellKind(%q, %q) = %q, want %q", c.goos, c.sh, got, c.want)
		}
	}
}

func TestShQuote(t *testing.T) {
	if got := shQuote(`/a b/it's`); got != `'/a b/it'\''s'` {
		t.Errorf("shQuote = %s", got)
	}
}

func TestMacTerminalZshTypesTheCommand(t *testing.T) {
	p, err := planTerminal(testEnv("darwin", "/bin/zsh"), "", "/Users/r/code/zig synth", evil)
	if err != nil {
		t.Fatal(err)
	}
	if !p.outcome.Typed || p.outcome.Clipboard || p.outcome.App != "Terminal" {
		t.Errorf("outcome = %+v", p.outcome)
	}
	if p.files["command"] != evil || (argvHasCommand(p, "rm -rf ~") || argvHasCommand(p, "`id`")) {
		t.Errorf("the command must go to a file only: files=%v steps=%v", p.files, p.steps)
	}
	st := p.steps[0]
	if st.argv[0] != "osascript" || st.argv[2] != terminalScript || !st.wait {
		t.Fatalf("step = %v", st.argv)
	}
	line := st.argv[3]
	for _, want := range []string{"cd '/Users/r/code/zig synth' && clear && ", `print -rz -- "$(<'/tmp/lathe-run-1/command')"`, "rm -rf -- '/tmp/lathe-run-1'"} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q lacks %q", line, want)
		}
	}
}

func TestMacTerminalOtherShells(t *testing.T) {
	p, _ := planTerminal(testEnv("darwin", "/bin/bash"), "terminal", "/w", "make")
	if p.outcome.Typed || !p.outcome.History || !p.outcome.Clipboard || p.clipboard != "make" ||
		!strings.Contains(p.steps[0].argv[3], "history -s") {
		t.Errorf("bash = %+v %v", p.outcome, p.steps)
	}
	p, _ = planTerminal(testEnv("darwin", "/opt/homebrew/bin/fish"), "terminal", "/w", "make")
	if p.outcome.Typed || !p.outcome.Clipboard || len(p.files) != 0 || strings.Join(p.steps[0].argv, " ") != "open -a Terminal /w" {
		t.Errorf("fish = %+v %v", p.outcome, p.steps)
	}
	// Open in Terminal: no command, no files, no clipboard.
	p, _ = planTerminal(testEnv("darwin", "/bin/zsh"), "", "/w", "")
	if p.outcome.Typed || p.outcome.Clipboard || len(p.files) != 0 || strings.Join(p.steps[0].argv, " ") != "open -a Terminal /w" {
		t.Errorf("open = %+v %v", p.outcome, p.steps)
	}
}

func TestITerm(t *testing.T) {
	// One line: write text … newline NO, in any shell.
	p, _ := planTerminal(testEnv("darwin", "/usr/local/bin/fish"), "iterm", "/w", "zig build run")
	a := p.steps[0].argv
	if !p.outcome.Typed || p.outcome.Clipboard || a[2] != itermScript || a[3] != "cd '/w' && clear" || a[4] != "zig build run" {
		t.Errorf("iterm = %+v %q", p.outcome, a)
	}
	if !strings.Contains(itermScript, "newline NO") {
		t.Error("iTerm must type without Return")
	}
	// Several lines in zsh: print -z.
	p, _ = planTerminal(testEnv("darwin", "/bin/zsh"), "iterm", "/w", "a\nb")
	if !p.outcome.Typed || p.files["command"] != "a\nb" || len(p.steps[0].argv) != 4 || argvHasCommand(p, "a\nb") {
		t.Errorf("iterm multi-line zsh = %+v %q", p.outcome, p.steps[0].argv)
	}
	// Several lines in fish: clipboard.
	p, _ = planTerminal(testEnv("darwin", "/usr/local/bin/fish"), "iterm", "/w", "a\nb")
	if p.outcome.Typed || !p.outcome.Clipboard {
		t.Errorf("iterm multi-line fish = %+v", p.outcome)
	}
}

func TestMacOtherTerminalsUseTheClipboard(t *testing.T) {
	for id, app := range map[string]string{"ghostty": "Ghostty", "wezterm": "WezTerm"} {
		p, err := planTerminal(testEnv("darwin", "/bin/zsh"), id, "/w", "ls")
		if err != nil || p.outcome.Typed || !p.outcome.Clipboard || p.clipboard != "ls" || p.steps[0].argv[2] != app {
			t.Errorf("%s = %+v %v %v", id, p.outcome, p.steps, err)
		}
	}
}

func TestLinuxTerminals(t *testing.T) {
	// The first installed terminal, zsh: a one-use ZDOTDIR.
	p, err := planTerminal(testEnv("linux", "/usr/bin/zsh", "kitty", "xterm"), "", "/home/r/w", evil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/bin/kitty", "--directory", "/home/r/w", "env", "ZDOTDIR=/tmp/lathe-run-1", "/usr/bin/zsh", "-i"}
	if !slices.Equal(p.steps[0].argv, want) || p.steps[0].dir != "/home/r/w" || p.steps[0].wait {
		t.Errorf("argv = %q, want %q", p.steps[0].argv, want)
	}
	if !p.outcome.Typed || p.outcome.App != "kitty" || p.files["command"] != evil || (argvHasCommand(p, "rm -rf ~") || argvHasCommand(p, "`id`")) {
		t.Errorf("outcome %+v files %v", p.outcome, p.files)
	}
	rc := p.files[".zshrc"]
	if !strings.Contains(rc, `print -rz -- "$(<"$_lathe_stage/command")"`) || !strings.Contains(rc, `source "$ZDOTDIR/.zshrc"`) ||
		!strings.Contains(p.files[".zshenv"], "_lathe_stage='/tmp/lathe-run-1'") {
		t.Errorf("zsh start-up files = %v", p.files)
	}

	// bash: --rcfile, history, clipboard.
	p, _ = planTerminal(testEnv("linux", "/bin/bash", "gnome-terminal"), "", "/w", "make")
	want = []string{"/usr/bin/gnome-terminal", "--working-directory=/w", "--", "/bin/bash", "--rcfile", "/tmp/lathe-run-1/bashrc", "-i"}
	if !slices.Equal(p.steps[0].argv, want) || !p.outcome.History || !p.outcome.Clipboard || p.outcome.Typed ||
		!strings.Contains(p.files["bashrc"], `history -s "$(< '/tmp/lathe-run-1'/command)"`) {
		t.Errorf("bash = %q %+v %v", p.steps[0].argv, p.outcome, p.files)
	}

	// fish: just the folder, and the clipboard.
	p, _ = planTerminal(testEnv("linux", "/usr/bin/fish", "konsole"), "konsole", "/w", "make")
	if !slices.Equal(p.steps[0].argv, []string{"/usr/bin/konsole", "--workdir", "/w"}) || !p.outcome.Clipboard || p.outcome.Typed {
		t.Errorf("fish = %q %+v", p.steps[0].argv, p.outcome)
	}

	// Every listed terminal has a command line with the folder in it.
	for _, id := range linuxTerminals {
		argv := linuxTerminalArgv(id, "/bin/"+id, "/w", []string{"zsh", "-i"})
		if argv[len(argv)-1] != "-i" || (id != "xterm" && !strings.Contains(strings.Join(argv, " "), "/w")) {
			t.Errorf("%s: %q", id, argv)
		}
	}

	if _, err := planTerminal(testEnv("linux", "/bin/zsh"), "", "/w", "ls"); err == nil {
		t.Error("no terminal installed: want an error")
	}
	if _, err := planTerminal(testEnv("linux", "/bin/zsh", "xterm"), "kitty", "/w", "ls"); err == nil || !strings.Contains(err.Error(), "kitty isn't installed") {
		t.Errorf("missing chosen terminal: %v", err)
	}
}

func TestWindowsTerminals(t *testing.T) {
	p, _ := planTerminal(testEnv("windows", "", "wt.exe"), "", `C:\Users\r\w`, "zig build")
	if p.outcome.App != "Windows Terminal" || !p.outcome.Clipboard || p.outcome.Typed ||
		!slices.Equal(p.steps[0].argv, []string{"/usr/bin/wt.exe", "-d", `C:\Users\r\w`}) {
		t.Errorf("wt = %+v %q", p.outcome, p.steps[0].argv)
	}
	p, _ = planTerminal(testEnv("windows", ""), "", `C:\w`, "")
	if p.outcome.App != "PowerShell" || p.outcome.Clipboard || p.steps[0].dir != `C:\w` || slices.Contains(p.steps[0].argv, `C:\w`) {
		t.Errorf("powershell = %+v %q", p.outcome, p.steps[0].argv)
	}
}

func TestPlanTerminalValidates(t *testing.T) {
	for name, c := range map[string]struct{ terminal, dir, cmd string }{
		"relative dir":  {"", "w", "ls"},
		"newline dir":   {"", "/w\n/x", "ls"},
		"escape":        {"", "/w", "ls \x1b[2J"},
		"unknown":       {"xterm-but-evil; rm", "/w", "ls"},
		"other os term": {"iterm", "/w", "ls"},
	} {
		if _, err := planTerminal(testEnv("linux", "/bin/zsh", "xterm"), c.terminal, c.dir, c.cmd); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestPlanEditor(t *testing.T) {
	st, ok, err := planEditor(testEnv("darwin", ""), "zed", "/w/src/a.zig")
	if err != nil || !ok || !slices.Equal(st.argv, []string{"open", "-a", "Zed", "/w/src/a.zig"}) {
		t.Errorf("mac zed = %q %v %v", st.argv, ok, err)
	}
	st, ok, err = planEditor(testEnv("linux", "", "code"), "vscode", "/w/src/a.zig")
	if err != nil || !ok || !slices.Equal(st.argv, []string{"/usr/bin/code", "/w/src/a.zig"}) {
		t.Errorf("linux code = %q %v %v", st.argv, ok, err)
	}
	if _, _, err := planEditor(testEnv("linux", ""), "vscode", "/w/a"); err == nil || !strings.Contains(err.Error(), "Visual Studio Code isn't installed") {
		t.Errorf("missing editor: %v", err)
	}
	if _, ok, err := planEditor(testEnv("linux", ""), "", "/w/a"); ok || err != nil {
		t.Errorf("default app: ok=%v err=%v", ok, err)
	}
	for _, bad := range [][2]string{{"vim; rm -rf /", "/w/a"}, {"zed", "w/a"}, {"zed", ""}, {"zed", "/w/a\nb"}} {
		if _, _, err := planEditor(testEnv("darwin", ""), bad[0], bad[1]); err == nil {
			t.Errorf("planEditor(%q, %q) accepted", bad[0], bad[1])
		}
	}
}

// The zsh start-up files really put the command at the prompt without
// running it, and load the reader's own .zshrc first. Needs zsh.
func TestZshStartupLeavesTheCommandUnrun(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil || runtime.GOOS == "windows" {
		t.Skip("no zsh")
	}
	home := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	if err := os.WriteFile(filepath.Join(home, ".zshrc"), []byte("LATHE_USER_RC=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stage := t.TempDir()
	files := zshStartup(stage)
	files["command"] = "touch " + marker
	for n, c := range files {
		if err := os.WriteFile(filepath.Join(stage, n), []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// -i with no terminal: zsh reads the start-up files, then exits at EOF
	// on stdin. The buffer stack holds the command; it must not have run.
	cmd := exec.Command(zsh, "-i", "-c", `print -r -- "rc=${LATHE_USER_RC:-no}"; read -rz buf; print -r -- "buf=$buf"`)
	cmd.Env = []string{"HOME=" + home, "ZDOTDIR=" + stage, "PATH=" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("zsh: %v %s", err, out)
	}
	if !strings.Contains(string(out), "rc=1") {
		t.Errorf("the reader's .zshrc wasn't loaded: %s", out)
	}
	// read -z pops the line editor's buffer stack: the command is there.
	if !strings.Contains(string(out), "buf=touch "+marker) {
		t.Errorf("the command isn't at the prompt: %s", out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the command ran")
	}
	if _, err := os.Stat(stage); err == nil {
		t.Error("the stage folder was not deleted")
	}
}

// The bash rcfile loads ~/.bashrc, adds the command to history without
// running it, and deletes the stage folder. Needs bash.
func TestBashStartupLeavesTheCommandUnrun(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil || runtime.GOOS == "windows" {
		t.Skip("no bash")
	}
	home := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte("LATHE_USER_RC=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stage := t.TempDir()
	files := bashStartup(stage)
	files["command"] = "touch " + marker
	for n, c := range files {
		if err := os.WriteFile(filepath.Join(stage, n), []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(bash, "--rcfile", filepath.Join(stage, "bashrc"), "-i")
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "HISTFILE=/dev/null"}
	cmd.Stdin = strings.NewReader("echo rc=$LATHE_USER_RC; history | tail -n 2\n")
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "rc=1") || !strings.Contains(string(out), "touch "+marker) {
		t.Errorf("bash output: %s", out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the command ran")
	}
	if _, err := os.Stat(stage); err == nil {
		t.Error("the stage folder was not deleted")
	}
}
