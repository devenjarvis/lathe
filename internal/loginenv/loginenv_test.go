package loginenv

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeEnv is an in-memory process environment for Options.LookupEnv/Setenv.
type fakeEnv map[string]string

func (e fakeEnv) lookup(k string) (string, bool) { v, ok := e[k]; return v, ok }
func (e fakeEnv) set(k, v string) error          { e[k] = v; return nil }

func (e fakeEnv) opts(o Options) Options {
	o.LookupEnv, o.Setenv = e.lookup, e.set
	if o.AgentPath == nil {
		o.AgentPath = []string{}
	}
	return o
}

// fakeShell writes an executable /bin/sh script standing in for the user's
// login shell. body runs first (rc-file noise, exports, failures); then the
// script records its flags in <dir>/args and evals the probe command (its
// last argument), as `sh -c` would.
func fakeShell(t *testing.T, body string) (shell, argsFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake shells are /bin/sh scripts")
	}
	dir := t.TempDir()
	shell = filepath.Join(dir, "fakesh")
	argsFile = filepath.Join(dir, "args")
	script := "#!/bin/sh\n" +
		"for a; do last=$a; done\n" +
		body + "\n" +
		"printf '%s ' \"$@\" >> '" + argsFile + "'; echo >> '" + argsFile + "'\n" +
		"eval \"$last\"\n"
	if err := os.WriteFile(shell, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return shell, argsFile
}

func TestPrepareMergesLoginPathAndAgentPath(t *testing.T) {
	shell, argsFile := fakeShell(t, `
echo "Welcome back! __LATHE_ENV_BEGIN_ not a real marker"
printf 'motd without newline'
export PATH=/login/nvm/bin:/opt/homebrew/bin:/usr/bin:/bin
export NVM_DIR=/home/u/.nvm
export ANTHROPIC_API_KEY=from-login
export PS1='junk$ '`)
	env := fakeEnv{"PATH": "/usr/bin:/bin:/usr/sbin:/sbin", "ANTHROPIC_API_KEY": "from-launcher"}
	rep, err := env.opts(Options{
		Shell:     shell,
		GOOS:      "darwin",
		AgentPath: []string{"/custom/agents", "/opt/homebrew/bin"},
	}).Prepare(context.Background())
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	want := "/custom/agents:/opt/homebrew/bin:/login/nvm/bin:/usr/bin:/bin:/usr/sbin:/sbin"
	if env["PATH"] != want {
		t.Errorf("PATH = %q\nwant  %q", env["PATH"], want)
	}
	if !slices.Equal(rep.PathAdded, []string{"/custom/agents", "/opt/homebrew/bin", "/login/nvm/bin"}) {
		t.Errorf("PathAdded = %v", rep.PathAdded)
	}
	if env["NVM_DIR"] != "/home/u/.nvm" {
		t.Errorf("NVM_DIR = %q, want it filled from the login shell", env["NVM_DIR"])
	}
	if env["ANTHROPIC_API_KEY"] != "from-launcher" {
		t.Errorf("ANTHROPIC_API_KEY = %q: a variable the process already has must not be overridden", env["ANTHROPIC_API_KEY"])
	}
	if _, ok := env["PS1"]; ok {
		t.Error("PS1 imported: only PATH and the allowlist may be")
	}
	// The probe inherits the test process's real environment, so other
	// allowlisted variables (a CI proxy) may be filled too.
	if !slices.Contains(rep.Filled, "NVM_DIR") || slices.Contains(rep.Filled, "ANTHROPIC_API_KEY") {
		t.Errorf("Filled = %v, want NVM_DIR and not ANTHROPIC_API_KEY", rep.Filled)
	}
	if !rep.Interactive || rep.Shell != shell {
		t.Errorf("report = %+v, want an interactive probe of %s", rep, shell)
	}
	args, _ := os.ReadFile(argsFile)
	if !strings.HasPrefix(string(args), "-i -l -c ") {
		t.Errorf("shell flags = %q, want -i -l -c", args)
	}
	if s := rep.String(); !strings.Contains(s, "-i -l") || !strings.Contains(s, "NVM_DIR") || strings.Contains(s, "from-login") {
		t.Errorf("Report.String() = %q", s)
	}
}

func TestProbeSurvivesNewlinesInValues(t *testing.T) {
	shell, _ := fakeShell(t, `export MULTI='line one
line two=looks like an entry'
export AFTER=ok`)
	env, err := Probe(context.Background(), shell, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if env["MULTI"] != "line one\nline two=looks like an entry" {
		t.Errorf("MULTI = %q", env["MULTI"])
	}
	if env["AFTER"] != "ok" {
		t.Errorf("AFTER = %q", env["AFTER"])
	}
	if env[ResolvingVar] != "1" {
		t.Errorf("%s = %q, want 1 inside the probe", ResolvingVar, env[ResolvingVar])
	}
}

func TestParseBlock(t *testing.T) {
	const b, e = "<B>", "<E>"
	t.Run("nul separated", func(t *testing.T) {
		out := "junk<B>A=1\x00B=two\nlines\x00C=\x00<E>trailer"
		env, ok := parseBlock([]byte(out), b, e)
		if !ok || env["A"] != "1" || env["B"] != "two\nlines" || env["C"] != "" || len(env) != 3 {
			t.Errorf("env = %q ok=%v", env, ok)
		}
	})
	t.Run("newline fallback", func(t *testing.T) {
		out := "<B>PATH=/a:/b\nMULTI=x\n  y\nZ=9\n<E>"
		env, ok := parseBlock([]byte(out), b, e)
		if !ok || env["PATH"] != "/a:/b" || env["MULTI"] != "x\n  y" || env["Z"] != "9" {
			t.Errorf("env = %q ok=%v", env, ok)
		}
	})
	t.Run("missing markers", func(t *testing.T) {
		for _, out := range []string{"PATH=/a", "<B>PATH=/a", "PATH=/a<E>"} {
			if _, ok := parseBlock([]byte(out), b, e); ok {
				t.Errorf("parseBlock(%q) ok, want no block", out)
			}
		}
	})
}

func TestMergePath(t *testing.T) {
	tests := []struct {
		name    string
		current string
		front   [][]string
		want    string
		added   []string
	}{
		{"no front keeps current", "/usr/bin:/bin", nil, "/usr/bin:/bin", nil},
		{"front first, deduped", "/usr/bin:/bin", [][]string{{"/x", "/usr/bin"}, {"/y", "/x/"}},
			"/x:/usr/bin:/y:/bin", []string{"/x", "/y"}},
		{"empties dropped", "/usr/bin::/bin:", [][]string{{"", "/x"}}, "/x:/usr/bin:/bin", []string{"/x"}},
		{"duplicates in current collapse", "/a:/b:/a", nil, "/a:/b", nil},
		{"empty current", "", [][]string{{"/a"}}, "/a", []string{"/a"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Skip("unix path lists")
			}
			got, added := MergePath(tc.current, tc.front...)
			if got != tc.want || !slices.Equal(added, tc.added) {
				t.Errorf("MergePath = %q %v, want %q %v", got, added, tc.want, tc.added)
			}
		})
	}
}

func TestProbeTimeoutKillsTheProcessGroup(t *testing.T) {
	// The rc file hangs, and leaves a background child holding stdout: the
	// probe must still return within its budget.
	shell, _ := fakeShell(t, "sleep 30 &\nsleep 30")
	start := time.Now()
	_, err := Probe(context.Background(), shell, 600*time.Millisecond)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("probe took %v, want it bounded by the timeout", d)
	}
}

func TestPrepareFallsBackToPlainLoginShell(t *testing.T) {
	// An interactive rc file that exits before the command runs.
	shell, argsFile := fakeShell(t, `case "$1" in -i) echo "bye from .zshrc"; exit 3;; esac
export PATH=/login/bin:/usr/bin`)
	env := fakeEnv{"PATH": "/usr/bin"}
	rep, err := env.opts(Options{Shell: shell, GOOS: "linux"}).Prepare(context.Background())
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if rep.Interactive {
		t.Error("Interactive = true, want the -l fallback")
	}
	if env["PATH"] != "/login/bin:/usr/bin" {
		t.Errorf("PATH = %q", env["PATH"])
	}
	// Only the fallback records its flags: the -i run exits in body first.
	args, _ := os.ReadFile(argsFile)
	if !strings.HasPrefix(string(args), "-l -c ") {
		t.Errorf("fallback flags = %q, want -l -c", args)
	}
}

func TestPrepareNoMarkersIsAnErrorButAppliesAgentPath(t *testing.T) {
	shell, _ := fakeShell(t, "echo 'PATH=/fake'; exit 0")
	env := fakeEnv{"PATH": "/usr/bin"}
	rep, err := env.opts(Options{Shell: shell, GOOS: "linux", AgentPath: []string{"/mine"}}).Prepare(context.Background())
	if !errors.Is(err, ErrNoMarkers) {
		t.Fatalf("err = %v, want ErrNoMarkers", err)
	}
	if env["PATH"] != "/mine:/usr/bin" || !slices.Equal(rep.PathAdded, []string{"/mine"}) {
		t.Errorf("PATH = %q, added %v: agent_path must apply even when the probe fails", env["PATH"], rep.PathAdded)
	}
}

func TestPrepareMissingShell(t *testing.T) {
	env := fakeEnv{"PATH": "/usr/bin"}
	_, err := env.opts(Options{Shell: filepath.Join(t.TempDir(), "nope"), GOOS: "linux"}).Prepare(context.Background())
	if err == nil {
		t.Fatal("Prepare with a missing shell returned nil error")
	}
	if env["PATH"] != "/usr/bin" {
		t.Errorf("PATH changed to %q", env["PATH"])
	}
}

func TestPrepareSkipsProbeOnWindows(t *testing.T) {
	shell, argsFile := fakeShell(t, "exit 1")
	env := fakeEnv{"PATH": "/usr/bin"}
	rep, err := env.opts(Options{Shell: shell, GOOS: "windows", AgentPath: []string{"/mine"}}).Prepare(context.Background())
	if err != nil {
		t.Fatalf("Prepare on windows: %v", err)
	}
	if rep.Skipped == "" || rep.Shell != "" {
		t.Errorf("report = %+v, want the probe skipped", rep)
	}
	if _, err := os.Stat(argsFile); err == nil {
		t.Error("the shell ran on windows")
	}
	if env["PATH"] != "/mine:/usr/bin" {
		t.Errorf("PATH = %q, want agent_path still applied", env["PATH"])
	}
}

func TestSkipProbeAppliesOnlyAgentPath(t *testing.T) {
	shell, argsFile := fakeShell(t, "export PATH=/login")
	env := fakeEnv{"PATH": "/usr/bin"}
	_, err := env.opts(Options{Shell: shell, SkipProbe: true, AgentPath: []string{"/mine"}}).Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if env["PATH"] != "/mine:/usr/bin" {
		t.Errorf("PATH = %q", env["PATH"])
	}
	if _, err := os.Stat(argsFile); err == nil {
		t.Error("SkipProbe ran the shell")
	}
}

func TestPrepareReadsConfiguredAgentPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".lathe"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"agent_path":["~/agents/bin"]}`
	if err := os.WriteFile(filepath.Join(home, ".lathe", "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	env := fakeEnv{"PATH": "/usr/bin"}
	rep, err := Options{SkipProbe: true, LookupEnv: env.lookup, Setenv: env.set}.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "agents", "bin")
	if !slices.Equal(rep.AgentPath, []string{want}) || !strings.HasPrefix(env["PATH"], want+string(os.PathListSeparator)) {
		t.Errorf("AgentPath = %v, PATH = %q", rep.AgentPath, env["PATH"])
	}
}

func TestUserShell(t *testing.T) {
	look := func(sh string) func(string) (string, bool) {
		return func(k string) (string, bool) {
			if k == "SHELL" && sh != "" {
				return sh, true
			}
			return "", false
		}
	}
	if got := userShell("linux", look("/usr/bin/fish")); got != "/usr/bin/fish" {
		t.Errorf("userShell($SHELL=fish) = %q", got)
	}
	if got := userShell("darwin", look("/bin/tcsh")); got != "/bin/zsh" {
		t.Errorf("userShell(tcsh, darwin) = %q, want the /bin/zsh default", got)
	}
	if got := userShell("linux", look("/usr/bin/nu")); got != "/bin/sh" {
		t.Errorf("userShell(nu, linux) = %q, want /bin/sh", got)
	}
}

// TestRealShells runs the probe against the real bash, zsh and fish when
// they're installed, each with a throwaway HOME whose startup files print
// junk and add a PATH entry the way installers do (zsh: .zshrc, which only
// an interactive shell reads; bash: .bash_profile; fish: config.fish).
func TestRealShells(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no login shells on windows")
	}
	cases := []struct {
		shell string
		file  string
		rc    string
	}{
		{"bash", ".bash_profile", "echo bash-profile-junk\nexport PATH=\"/rc/bash/bin:$PATH\"\n"},
		{"zsh", ".zshrc", "echo zshrc-junk\nexport PATH=\"/rc/zsh/bin:$PATH\"\n"},
		{"fish", ".config/fish/config.fish", "echo fish-junk\nset -gx PATH /rc/fish/bin $PATH\n"},
	}
	for _, tc := range cases {
		t.Run(tc.shell, func(t *testing.T) {
			bin, err := exec.LookPath(tc.shell)
			if err != nil {
				t.Skipf("%s not installed", tc.shell)
			}
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			rc := filepath.Join(home, tc.file)
			if err := os.MkdirAll(filepath.Dir(rc), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(rc, []byte(tc.rc), 0o644); err != nil {
				t.Fatal(err)
			}
			env, interactive, err := probeWithFallback(context.Background(), bin, 10*time.Second)
			if err != nil {
				t.Fatalf("probe %s: %v", bin, err)
			}
			want := "/rc/" + tc.shell + "/bin"
			if !slices.Contains(filepath.SplitList(env["PATH"]), want) {
				t.Errorf("PATH = %q, want it to contain %s", env["PATH"], want)
			}
			if !interactive {
				t.Errorf("%s: interactive probe failed, used the -l fallback", tc.shell)
			}
		})
	}
}
