package loginenv

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// maxOutput caps what the probe keeps of the shell's stdout. An env dump is
// a few KiB; an rc file that floods stdout shouldn't cost more than this.
const maxOutput = 4 << 20

// ErrNoMarkers means the shell ran but never printed the env block, usually
// because an rc file exited, exec'd something else or failed before the
// command ran.
var ErrNoMarkers = errors.New("login shell printed no environment")

// incompatible lists shells whose syntax or flags the probe can't use
// (csh/tcsh only allow -l as the sole flag; the rest aren't POSIX-ish).
// For these, the probe asks the platform default shell instead, which still
// picks up the system-wide login setup (e.g. macOS path_helper).
var incompatible = map[string]bool{
	"csh": true, "tcsh": true, "nu": true, "xonsh": true,
	"elvish": true, "pwsh": true, "powershell": true,
}

// Probe runs shell as a login shell and returns its environment. It tries an
// interactive login shell first and, if that fails, a plain login shell, all
// within timeout; see probeWithFallback for why.
func Probe(ctx context.Context, shell string, timeout time.Duration) (map[string]string, error) {
	env, _, err := probeWithFallback(ctx, shell, timeout)
	return env, err
}

// probeWithFallback asks for an interactive login shell (-i -l) first, the
// way VS Code resolves its environment. -l alone isn't enough: zsh reads
// .zshrc only for interactive shells, and that's exactly where the nvm,
// pyenv and conda installers (and most people) put their PATH lines; bash's
// .bashrc is the same story wherever .bash_profile doesn't source it. The
// cost of -i is that interactive rc files do interactive things — prompts,
// `exec tmux`, a `read` — so the shell gets stdin from /dev/null and no
// controlling terminal (its own session), and the env block is fenced by
// markers so prompt junk doesn't matter. If the interactive attempt still
// fails (an rc file exits early, or hangs), the plain login shell (-l) gets
// the rest of the budget: a smaller PATH beats none. The interactive attempt
// may use at most 60% of the budget so the fallback always has some.
func probeWithFallback(ctx context.Context, shell string, timeout time.Duration) (map[string]string, bool, error) {
	if shell == "" {
		return nil, false, errors.New("no shell to probe")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	first, cancelFirst := context.WithTimeout(ctx, timeout*3/5)
	env, err := runShell(first, shell, true)
	cancelFirst()
	if err == nil {
		return env, true, nil
	}
	if ctx.Err() != nil {
		return nil, false, err
	}
	env, err2 := runShell(ctx, shell, false)
	if err2 != nil {
		return nil, false, fmt.Errorf("%w (interactive attempt: %v)", err2, err)
	}
	return env, false, nil
}

// runShell runs one probe. The command prints a begin marker, the
// environment NUL-separated (`env -0`, which GNU, BSD/macOS and busybox env
// all support; plain `env` if not), then an end marker. It sticks to syntax
// bash, zsh, fish, dash and ksh share (`;`, `||`, single quotes, the
// `command` builtin). The markers are random per run and split in two in
// the command text, so neither an rc file nor a shell echoing its own
// command line can fake them.
func runShell(ctx context.Context, shell string, interactive bool) (map[string]string, error) {
	tok, err := token()
	if err != nil {
		return nil, err
	}
	begin, end := "__LATHE_ENV_BEGIN_"+tok+"__", "__LATHE_ENV_END_"+tok+"__"
	script := fmt.Sprintf("printf '%%s%%s' '%s' '%s'; command env -0 || command env; printf '%%s%%s' '%s' '%s'",
		begin[:9], begin[9:], end[:9], end[9:])
	args := []string{"-l", "-c", script}
	if interactive {
		args = append([]string{"-i"}, args...)
	}

	cmd := exec.CommandContext(ctx, shell, args...)
	cmd.Env = append(os.Environ(), ResolvingVar+"=1")
	cmd.Stdin = nil // /dev/null: an rc file's `read` gets EOF, not a hang
	var stdout, stderr limitedBuffer
	stdout.limit, stderr.limit = maxOutput, 4<<10
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	detach(cmd)
	// Wait returns once the group is killed even if something it started
	// escaped the kill and holds stdout open.
	cmd.WaitDelay = 500 * time.Millisecond

	runErr := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%s %s: timed out: %w", shell, strings.Join(args[:len(args)-2], " "), ctx.Err())
	}
	env, ok := parseBlock(stdout.Bytes(), begin, end)
	if ok {
		// The block printed, so the env is good whatever the exit status
		// (some rc files leave a failing status behind).
		return env, nil
	}
	if runErr != nil {
		return nil, fmt.Errorf("%s: %w%s", shell, runErr, stderrTail(stderr.Bytes()))
	}
	return nil, fmt.Errorf("%s: %w%s", shell, ErrNoMarkers, stderrTail(stderr.Bytes()))
}

// parseBlock extracts the environment printed between the markers.
// NUL-separated output (env -0) is exact. Newline-separated output (the
// plain env fallback) can't tell a newline inside a value from the next
// entry, so a line that doesn't look like KEY=… continues the previous value
// — right for every multi-line value except one whose later line itself
// looks like an assignment, which PATH never does.
func parseBlock(out []byte, begin, end string) (map[string]string, bool) {
	i := bytes.Index(out, []byte(begin))
	if i < 0 {
		return nil, false
	}
	body := out[i+len(begin):]
	j := bytes.Index(body, []byte(end))
	if j < 0 {
		return nil, false
	}
	body = body[:j]
	env := map[string]string{}
	if bytes.IndexByte(body, 0) >= 0 {
		for _, e := range bytes.Split(body, []byte{0}) {
			if k, v, ok := splitEntry(string(e)); ok {
				env[k] = v
			}
		}
		return env, true
	}
	var last string
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 64<<10), maxOutput)
	for sc.Scan() {
		line := sc.Text()
		if k, v, ok := splitEntry(line); ok {
			env[k] = v
			last = k
		} else if last != "" {
			env[last] += "\n" + line
		}
	}
	return env, true
}

// splitEntry splits KEY=VALUE, requiring a plausible variable name so a
// continuation line of a multi-line value isn't mistaken for an entry.
func splitEntry(e string) (string, string, bool) {
	k, v, ok := strings.Cut(e, "=")
	if !ok || k == "" {
		return "", "", false
	}
	for _, r := range k {
		if r != '_' && r != '%' && (r < '0' || r > '9') && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
			return "", "", false
		}
	}
	return k, v, true
}

// userShell finds the shell to probe: $SHELL, else the account's shell,
// else the platform default. A shell the probe can't drive is swapped for
// the platform default.
func userShell(goos string, lookup func(string) (string, bool)) string {
	def := "/bin/sh"
	if goos == "darwin" {
		def = "/bin/zsh"
	}
	sh, _ := lookup("SHELL")
	if sh == "" {
		sh = accountShell(goos)
	}
	if sh == "" || incompatible[filepath.Base(sh)] {
		return def
	}
	return sh
}

// accountShell reads the user's shell from the account database: dscl on
// macOS, /etc/passwd elsewhere. "" when it can't tell.
func accountShell(goos string) string {
	user := os.Getenv("USER")
	if user == "" {
		return ""
	}
	if goos == "darwin" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "/usr/bin/dscl", ".", "-read", "/Users/"+user, "UserShell").Output()
		if err != nil {
			return ""
		}
		// "UserShell: /bin/zsh"
		_, sh, _ := strings.Cut(strings.TrimSpace(string(out)), ":")
		return strings.TrimSpace(sh)
	}
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Split(line, ":")
		if len(f) == 7 && f[0] == user {
			return strings.TrimSpace(f[6])
		}
	}
	return ""
}

func token() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func stderrTail(b []byte) string {
	s := strings.TrimSpace(string(b))
	if s == "" {
		return ""
	}
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if len(s) > 200 {
		s = s[len(s)-200:]
	}
	return " (stderr: " + s + ")"
}

// limitedBuffer keeps the first limit bytes written and discards the rest,
// still reporting success so the shell isn't killed by a write error.
type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.Len(); room > 0 {
		if len(p) > room {
			_, _ = b.Buffer.Write(p[:room])
		} else {
			_, _ = b.Buffer.Write(p)
		}
	}
	return len(p), nil
}
