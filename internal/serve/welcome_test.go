package serve

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/devenjarvis/lathe/internal/acp/acptest"
	"github.com/devenjarvis/lathe/internal/config"
)

// welcomeServer is a test server over a fresh HOME and an empty library, with
// a fake agent host and the given programs on PATH.
func welcomeServer(t *testing.T, onPath ...string) (*Server, *httptest.Server) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	srv := NewServer(t.TempDir())
	f := &fakeHost{}
	srv.hostFactory = func(command string) *agentHost {
		return newAgentHost(command, f.starter(func() *acptest.Agent {
			return &acptest.Agent{Model: "m", Reply: func(string) string { return "ok" }}
		}))
	}
	found := map[string]bool{}
	for _, p := range onPath {
		found[p] = true
	}
	srv.lookPath = func(file string) (string, error) {
		if found[file] {
			return "/usr/bin/" + file, nil
		}
		return "", errors.New("not found")
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); srv.Close() })
	return srv, ts
}

// noRedirect is a client that hands back redirects instead of following them.
var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// postWelcome posts a urlencoded form, as the page does with scripts off.
func postWelcome(t *testing.T, ts *httptest.Server, form url.Values, header ...string) (int, string, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/-/welcome", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Location"), string(b)
}

func TestWelcomeStepsRender(t *testing.T) {
	_, ts := welcomeServer(t, "claude-agent-acp")
	agent := get(t, ts, "/-/welcome")
	for _, want := range []string{
		"Pick your agent", `aria-current="step"`, `name="step" value="agent"`,
		// The shared agent list, with the found agent pre-picked and
		// install help for the rest.
		`data-agent="claude"`, `name="agent" value="claude" checked`,
		"npm install -g @google/gemini-cli", `data-copy="npm install -g @google/gemini-cli"`,
		"Check again", "Skip setup", `id="newSheet"`,
	} {
		if !strings.Contains(agent, want) {
			t.Errorf("agent step lacks %q", want)
		}
	}
	folder := get(t, ts, "/-/welcome?step=folder")
	for _, want := range []string{"Where should code folders go?", `name="code_root" type="text" value="~/code"`, "~/code/digital-synth-zig"} {
		if !strings.Contains(folder, want) {
			t.Errorf("folder step lacks %q", want)
		}
	}
	// A browser has no folder picker.
	if strings.Contains(folder, `id="welcomePickFolder"`) {
		t.Error("browser folder step offers the native folder picker")
	}
	start := get(t, ts, "/-/welcome?step=start")
	for _, want := range []string{"Make your first tutorial", `name="next" value="home"`, `name="next" value="new"`, "None yet"} {
		if !strings.Contains(start, want) {
			t.Errorf("start step lacks %q", want)
		}
	}
	if strings.Contains(start, "Skip setup") {
		t.Error("the last step repeats Skip setup beside its own Skip")
	}
	// An unknown step is the first one.
	if !strings.Contains(get(t, ts, "/-/welcome?step=nope"), "Pick your agent") {
		t.Error("unknown step did not fall back to the agent step")
	}
}

func TestWelcomeNoAgentFound(t *testing.T) {
	_, ts := welcomeServer(t)
	page := get(t, ts, "/-/welcome")
	if !strings.Contains(page, "No agent found yet") {
		t.Error("agent step doesn't explain that nothing was found")
	}
	if regexp.MustCompile(`name="agent" value="[a-z]+" checked`).MatchString(page) {
		t.Error("agent step pre-picks an agent that isn't installed")
	}
	// Continue without an agent moves on.
	code, loc, _ := postWelcome(t, ts, url.Values{"step": {"agent"}})
	if code != http.StatusSeeOther || loc != "/-/welcome?step=folder" {
		t.Errorf("continue without an agent = %d %q", code, loc)
	}
}

func TestWelcomeFlow(t *testing.T) {
	srv, ts := welcomeServer(t, "claude-agent-acp")

	// 1. The agent: started and remembered.
	code, loc, body := postWelcome(t, ts, url.Values{"step": {"agent"}, "agent": {"claude"}})
	if code != http.StatusSeeOther || loc != "/-/welcome?step=folder" {
		t.Fatalf("agent step = %d %q %s", code, loc, body)
	}
	if h := srv.hostedAgent(); h == nil || h.id != "claude" {
		t.Fatalf("agent step did not start Claude Code")
	}
	if c, _ := config.ReadConfig(); c.Agent != "claude" {
		t.Errorf("config agent = %q", c.Agent)
	}
	if code, _, body := postWelcome(t, ts, url.Values{"step": {"agent"}, "agent": {"nope"}}); code != http.StatusBadRequest || !strings.Contains(body, "isn&#39;t one Lathe knows") {
		t.Errorf("unknown agent = %d, want 400 with the message on the step", code)
	}

	// 2. The code folder: validated, then saved.
	code, _, body = postWelcome(t, ts, url.Values{"step": {"folder"}, "code_root": {"relative/dir"}})
	if code != http.StatusBadRequest || !strings.Contains(body, "must be a full path") || !strings.Contains(body, "Where should code folders go?") || !strings.Contains(body, `value="relative/dir"`) {
		t.Errorf("relative code folder = %d, want 400 on the folder step, keeping what was typed", code)
	}
	code, loc, _ = postWelcome(t, ts, url.Values{"step": {"folder"}, "code_root": {"~/src/learn"}})
	if code != http.StatusSeeOther || loc != "/-/welcome?step=start" {
		t.Fatalf("folder step = %d %q", code, loc)
	}
	if got := config.LoadSettings().CodeRoot; got != "~/src/learn" {
		t.Errorf("code_root = %q", got)
	}
	if config.Onboarded() {
		t.Error("onboarded before the last step")
	}

	// The summary names both.
	start := get(t, ts, "/-/welcome?step=start")
	if !strings.Contains(start, "<strong>Claude Code</strong>") || !strings.Contains(start, "~/src/learn") {
		t.Error("start step doesn't sum up the agent and folder")
	}
	if !strings.Contains(start, `class="welcome-step is-done"`) {
		t.Error("earlier steps aren't marked done")
	}

	// 3. Make your first tutorial: the /-/new page with scripts off, the
	// sheet over Home with them.
	code, loc, _ = postWelcome(t, ts, url.Values{"step": {"start"}, "next": {"new"}})
	if code != http.StatusSeeOther || loc != "/-/new" {
		t.Errorf("first tutorial (no script) = %d %q", code, loc)
	}
	if !config.Onboarded() {
		t.Error("finishing did not record onboarded")
	}
	code, loc, _ = postWelcome(t, ts, url.Values{"step": {"start"}, "next": {"new"}, "sheet": {"1"}})
	if code != http.StatusSeeOther || loc != "/#new" {
		t.Errorf("first tutorial (script) = %d %q", code, loc)
	}
}

func TestWelcomeSkip(t *testing.T) {
	_, ts := welcomeServer(t)
	code, loc, _ := postWelcome(t, ts, url.Values{"step": {"start"}, "next": {"home"}})
	if code != http.StatusSeeOther || loc != "/" {
		t.Errorf("skip = %d %q", code, loc)
	}
	if !config.Onboarded() {
		t.Error("skip did not record onboarded")
	}
}

func TestWelcomeRefusesCrossOrigin(t *testing.T) {
	_, ts := welcomeServer(t)
	code, _, _ := postWelcome(t, ts, url.Values{"step": {"start"}, "next": {"home"}}, "Origin", "https://evil.example")
	if code != http.StatusForbidden {
		t.Errorf("cross-origin post = %d, want 403", code)
	}
	if config.Onboarded() {
		t.Error("a cross-origin post changed the config")
	}
	if code, _, _ := postWelcome(t, ts, url.Values{"step": {"bogus"}}); code != http.StatusBadRequest {
		t.Errorf("unknown step = %d, want 400", code)
	}
}

// The desktop app's first launch goes to the setup; plain lathe serve only
// offers it on Home.
func TestWelcomeOnFirstLaunch(t *testing.T) {
	srv, ts := welcomeServer(t)
	home := func() (int, string, string) {
		resp, err := noRedirect.Get(ts.URL + "/")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Location"), string(b)
	}

	// Browser: Home, with the callout.
	code, _, body := home()
	if code != http.StatusOK || !strings.Contains(body, `href="/-/welcome"`) || !strings.Contains(body, "Set up Lathe") {
		t.Errorf("browser first launch = %d, want Home offering the setup", code)
	}

	// App: redirected, but only for plain Home.
	srv.SetNative(newFakeNative())
	if code, loc, _ := home(); code != http.StatusSeeOther || loc != "/-/welcome" {
		t.Errorf("app first launch = %d %q, want the setup", code, loc)
	}
	if !strings.Contains(get(t, ts, "/?view=all"), "No tutorials yet") {
		t.Error("another view redirected too")
	}
	// The desktop folder step has the picker.
	if !strings.Contains(get(t, ts, "/-/welcome?step=folder"), `id="welcomePickFolder"`) {
		t.Error("app folder step lacks Choose folder…")
	}

	// Done: no redirect, no callout.
	if err := config.SetOnboarded(true); err != nil {
		t.Fatal(err)
	}
	code, _, body = home()
	if code != http.StatusOK || strings.Contains(body, "Set up Lathe") {
		t.Errorf("after setup = %d, still offering it", code)
	}
}

// A library that already has tutorials never shows the setup on its own.
func TestWelcomeNotForExistingLibrary(t *testing.T) {
	_, ts, _ := settingsServer(t)
	page := get(t, ts, "/")
	if strings.Contains(page, "Set up Lathe") {
		t.Error("Home offers the setup to a library with tutorials")
	}
	// It can still be reopened from Settings.
	if !strings.Contains(get(t, ts, "/-/settings"), `href="/-/welcome"`) {
		t.Error("Settings › Agent has no way back to the setup")
	}
}

func TestWelcomeNativePickCodeRoot(t *testing.T) {
	srv, ts := welcomeServer(t)
	n := newFakeNative()
	n.pick = "/home/me/src"
	srv.SetNative(n)
	code, body := postSettings(t, ts, "/-/native/pick-folder", `{"for":"code-root","path":"~/code"}`)
	if code != http.StatusOK || !strings.Contains(body, "/home/me/src") {
		t.Fatalf("code-root pick = %d %s", code, body)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.picks) != 1 || n.picks[0][0] != "Choose where code folders go" {
		t.Errorf("picks = %v", n.picks)
	}
}
