package serve

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
)

// settingsServer is a test server over a fresh HOME with one series tutorial.
func settingsServer(t *testing.T) (*Server, *httptest.Server, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	tutDir := makeTutFixture(t, dir, "series", true)
	srv := NewServer(dir)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); srv.Close() })
	return srv, ts, tutDir
}

func postSettings(t *testing.T, ts *httptest.Server, path, body string, header ...string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var b bytes.Buffer
	_, _ = b.ReadFrom(resp.Body)
	return resp.StatusCode, b.String()
}

func TestSettingsPageRendersEveryTab(t *testing.T) {
	_, ts, _ := settingsServer(t)
	page := get(t, ts, "/-/settings")
	for _, id := range []string{"agent", "voices", "library", "reading", "shortcuts"} {
		if !strings.Contains(page, `<section id="`+id+`"`) || !strings.Contains(page, `href="#`+id+`"`) {
			t.Errorf("settings page lacks the %s tab", id)
		}
	}
	for _, want := range []string{"Claude Code", "plainspoken", "Keep verified code", "Remember my place", "Match system", "/lathe-work"} {
		if !strings.Contains(page, want) {
			t.Errorf("settings page lacks %q", want)
		}
	}
	// Library has the auto-verify toggle, off by default.
	if !strings.Contains(page, "Verify after writing a part") || !strings.Contains(page, `data-setting="auto_verify">`) {
		t.Error("settings page lacks the auto-verify toggle, unchecked")
	}
	// A browser has no shell: no native-only controls.
	if strings.Contains(page, "Show in Finder") || strings.Contains(page, `id="agentPathPick"`) {
		t.Error("browser settings page shows desktop-only controls")
	}
}

func TestSettingsSaveAndRead(t *testing.T) {
	_, ts, _ := settingsServer(t)
	code, body := postSettings(t, ts, "/-/settings", `{"appearance":"dark","text_size":"large","line_width":"wide","remember_place":false,"notify_check":false,"code_root":"~/src"}`)
	if code != http.StatusOK {
		t.Fatalf("POST /-/settings = %d %s", code, body)
	}
	var st config.Settings
	if err := json.Unmarshal([]byte(body), &st); err != nil {
		t.Fatal(err)
	}
	if st.Appearance != "dark" || st.TextSize != "large" || st.LineWidth != "wide" || st.RememberPlace || st.NotifyCheck || !st.NotifyVerify || st.CodeRoot != "~/src" {
		t.Errorf("saved settings = %+v", st)
	}
	if got := config.LoadSettings(); got != st {
		t.Errorf("config.json has %+v, response said %+v", got, st)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/-/settings", nil)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var again config.Settings
	if err := json.NewDecoder(resp.Body).Decode(&again); err != nil || again != st {
		t.Errorf("GET /-/settings JSON = %+v, %v", again, err)
	}

	// Turning a default-on key back on removes it from config.json.
	if code, body := postSettings(t, ts, "/-/settings", `{"remember_place":true,"notify_check":true,"text_size":"default"}`); code != http.StatusOK {
		t.Fatalf("POST = %d %s", code, body)
	}
	c, _ := config.ReadConfig()
	if c.RememberPlace != nil || c.Notify != nil || c.TextSize != "" {
		t.Errorf("defaults not dropped from config.json: %+v", c)
	}
}

func TestSettingsSaveRejects(t *testing.T) {
	_, ts, _ := settingsServer(t)
	for _, body := range []string{
		`{"appearance":"sepia"}`,
		`{"text_size":"huge"}`,
		`{"line_width":"endless"}`,
		`{"code_root":"relative/dir"}`,
		`{"auto_verfy":true}`, // unknown keys are refused
		`not json`,
	} {
		if code, _ := postSettings(t, ts, "/-/settings", body); code != http.StatusBadRequest {
			t.Errorf("POST %s = %d, want 400", body, code)
		}
	}
	if code, _ := postSettings(t, ts, "/-/settings", `{"appearance":"dark"}`, "Origin", "https://evil.example"); code != http.StatusForbidden {
		t.Errorf("cross-origin save = %d, want 403", code)
	}
	if config.LoadSettings().Appearance != "" {
		t.Error("a refused save changed config.json")
	}
}

func TestPagesApplyServedAppearanceAndReadingSize(t *testing.T) {
	_, ts, _ := settingsServer(t)
	if code, body := postSettings(t, ts, "/-/settings", `{"appearance":"dark","text_size":"larger","line_width":"narrow"}`); code != http.StatusOK {
		t.Fatalf("POST = %d %s", code, body)
	}
	for _, path := range []string{"/", "/series/part-01.md", "/-/settings"} {
		page := get(t, ts, path)
		if !strings.Contains(page, `"appearance":"dark"`) {
			t.Errorf("%s: pre-paint script lacks the served appearance", path)
		}
		if !strings.Contains(page, `html{font-size:125%}:root{--measure:58ch}`) {
			t.Errorf("%s: no reading size/measure style", path)
		}
		// localStorage stays the fallback for a reader who never chose.
		if !strings.Contains(page, "localStorage.getItem('lathe-theme')") {
			t.Errorf("%s: lost the localStorage fallback", path)
		}
	}
}

func TestRememberPlaceOffOpensTheFirstPart(t *testing.T) {
	_, ts, tutDir := settingsServer(t)
	if err := store.WriteProgress(tutDir, &store.Progress{Part: "part-02.md", Ratio: .5, UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	where := func() string {
		resp, err := client.Get(ts.URL + "/series/")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.Header.Get("Location")
	}
	if got := where(); got != "/series/part-02.md" {
		t.Errorf("remember on: redirect = %q", got)
	}
	if code, body := postSettings(t, ts, "/-/settings", `{"remember_place":false}`); code != http.StatusOK {
		t.Fatalf("POST = %d %s", code, body)
	}
	if got := where(); got != "/series/part-01.md" {
		t.Errorf("remember off: redirect = %q", got)
	}
}

func TestWorkspacePlaceholderUsesCodeRoot(t *testing.T) {
	_, ts, _ := settingsServer(t)
	if page := get(t, ts, "/series/part-01.md"); !strings.Contains(page, `placeholder="~/code/series"`) {
		t.Error("default workspace placeholder is not ~/code/<slug>")
	}
	if code, body := postSettings(t, ts, "/-/settings", `{"code_root":"~/src"}`); code != http.StatusOK {
		t.Fatalf("POST = %d %s", code, body)
	}
	if page := get(t, ts, "/series/part-01.md"); !strings.Contains(page, `placeholder="~/src/series"`) {
		t.Error("workspace placeholder ignores code_root")
	}
}

func TestAgentPathAddRemove(t *testing.T) {
	_, ts, _ := settingsServer(t)
	home, _ := os.UserHomeDir()
	bin := filepath.Join(home, "tools", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", os.Getenv("PATH")) // restored after ApplyAgentPath edits it
	code, body := postSettings(t, ts, "/-/settings/agent-path", `{"add":"`+bin+`"}`)
	if code != http.StatusOK || !strings.Contains(body, `"~/tools/bin"`) {
		t.Fatalf("add = %d %s", code, body)
	}
	if got := config.AgentPath(); len(got) != 1 || got[0] != bin {
		t.Errorf("AgentPath() = %v", got)
	}
	if !strings.Contains(os.Getenv("PATH"), bin) {
		t.Error("an added folder is not on PATH")
	}
	// Adding it again doesn't duplicate it.
	if _, body := postSettings(t, ts, "/-/settings/agent-path", `{"add":"~/tools/bin"}`); strings.Count(body, "tools/bin") != 1 {
		t.Errorf("re-add = %s", body)
	}
	if page := get(t, ts, "/-/settings"); !strings.Contains(page, "~/tools/bin") {
		t.Error("settings page doesn't list the folder")
	}
	for _, bad := range []string{`{"add":"~/nope"}`, `{"add":"rel/dir"}`, `{}`, `{"add":"/x","remove":"/y"}`} {
		if code, _ := postSettings(t, ts, "/-/settings/agent-path", bad); code != http.StatusBadRequest {
			t.Errorf("agent-path %s = %d, want 400", bad, code)
		}
	}
	code, body = postSettings(t, ts, "/-/settings/agent-path", `{"remove":"~/tools/bin"}`)
	if code != http.StatusOK || body != "{\"agentPath\":[]}\n" {
		t.Errorf("remove = %d %q", code, body)
	}
	if code, _ := postSettings(t, ts, "/-/settings/agent-path", `{"add":"`+bin+`"}`, "Origin", "https://evil.example"); code != http.StatusForbidden {
		t.Errorf("cross-origin add = %d, want 403", code)
	}
}

func TestSettingsAgentRestart(t *testing.T) {
	srv, ts, _, f := panelServer(t, "model-a", "claude-agent-acp")
	if code, _ := postSettings(t, ts, "/-/agent/restart", `{}`); code != http.StatusConflict {
		t.Errorf("restart with no agent = %d, want 409", code)
	}
	if code, body := postJSONBody(t, ts.URL+"/-/agent", map[string]string{"id": "claude"}); code != http.StatusOK {
		t.Fatalf("pick = %d %s", code, body)
	}
	old := srv.hostedAgent()
	code, body := postSettings(t, ts, "/-/agent/restart", `{}`)
	if code != http.StatusOK || !strings.Contains(body, `"name":"Claude Code"`) {
		t.Fatalf("restart = %d %s", code, body)
	}
	h := srv.hostedAgent()
	if h == old || h.command != old.command || h.id != "claude" {
		t.Errorf("restart did not swap in a fresh host for the same agent")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.mu.Lock()
		n := f.starts
		f.mu.Unlock()
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent started %d times, want a restart", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if code, _ := postSettings(t, ts, "/-/agent/restart", `{}`, "Origin", "https://evil.example"); code != http.StatusForbidden {
		t.Errorf("cross-origin restart = %d, want 403", code)
	}
	if page := get(t, ts, "/-/settings"); !strings.Contains(page, `class="agent-choice settings-agent is-current" data-agent="claude"`) {
		t.Error("settings page doesn't mark the running agent")
	}
}

func TestShortcutsTab(t *testing.T) {
	_, ts, _ := settingsServer(t)
	page := get(t, ts, "/-/settings")
	for _, want := range []string{"<h3>Library</h3>", "<h3>Agent</h3>", "<h3>Reading</h3>", "<dt>Next part</dt>", `id="latheShortcuts"`} {
		if !strings.Contains(page, want) {
			t.Errorf("shortcuts tab lacks %q", want)
		}
	}
	if strings.Count(page, "<h3>Reading</h3>") != 1 {
		t.Error("a group's rows are not kept together")
	}
	// Stubs (no built action yet) are not listed.
	if strings.Contains(page, "<dt>Go to anything…</dt>") {
		t.Error("shortcuts tab lists a stub")
	}
}

func TestShortcutGroupsFollowPlatform(t *testing.T) {
	find := func(goos, label string) string {
		for _, g := range shortcutGroups(goos) {
			for _, it := range g.Items {
				if it.Label == label {
					return it.Combo
				}
			}
		}
		return ""
	}
	if got := find("darwin", "Next part"); got != "⌘]" {
		t.Errorf("darwin Next part = %q, want ⌘]", got)
	}
	if got := find("linux", "Next part"); got != "Ctrl+]" {
		t.Errorf("linux Next part = %q, want Ctrl+]", got)
	}
}

func TestSettingsNativeAffordances(t *testing.T) {
	srv, ts, _ := settingsServer(t)
	if code, _ := postSettings(t, ts, "/-/native/reveal", `{"what":"tutorials"}`); code != http.StatusNotFound {
		t.Errorf("reveal without a shell = %d, want 404", code)
	}
	n := &revealNative{fakeNative: newFakeNative()}
	srv.SetNative(n)
	page := get(t, ts, "/-/settings")
	if !strings.Contains(page, `id="revealTutorials"`) || !strings.Contains(page, `id="agentPathPick"`) || !strings.Contains(page, "/-/native/window") {
		t.Error("desktop settings page lacks Show in Finder, Choose folder… or the window opener")
	}
	if code, body := postSettings(t, ts, "/-/native/reveal", `{"what":"tutorials"}`); code != http.StatusNoContent {
		t.Fatalf("reveal = %d %s", code, body)
	}
	if len(n.revealed) != 1 || n.revealed[0] != srv.tutorialsDir {
		t.Errorf("revealed %v", n.revealed)
	}
	if code, _ := postSettings(t, ts, "/-/native/reveal", `{"what":"/etc"}`); code != http.StatusBadRequest {
		t.Errorf("reveal of another folder = %d, want 400", code)
	}

	// The Settings window, at a tab, opens in an app window.
	for _, p := range []string{"/-/settings", "/-/settings#reading"} {
		if code, body := postSettings(t, ts, "/-/native/window", `{"path":"`+p+`"}`); code != http.StatusNoContent {
			t.Errorf("window %s = %d %s", p, code, body)
		}
	}
	for _, p := range []string{"/-/settings#nope", "/-/settingsx", "/-/settings/../x"} {
		if code, _ := postSettings(t, ts, "/-/native/window", `{"path":"`+p+`"}`); code != http.StatusBadRequest {
			t.Errorf("window %s = %d, want 400", p, code)
		}
	}
	n.mu.Lock()
	if len(n.windows) != 2 || n.windows[1] != [2]string{"/-/settings#reading", "Settings"} {
		t.Errorf("windows = %v", n.windows)
	}
	n.mu.Unlock()

	// Picking an agent search folder needs no tutorial.
	n.pick = "/opt/agents/bin"
	if code, body := postSettings(t, ts, "/-/native/pick-folder", `{"for":"agent-path"}`); code != http.StatusOK || !strings.Contains(body, "/opt/agents/bin") {
		t.Errorf("agent-path pick = %d %s", code, body)
	}
}

func TestNotifySettingsSilenceAJob(t *testing.T) {
	srv, _, tutDir := settingsServer(t)
	n := newFakeNative()
	srv.SetNative(n)
	if err := config.UpdateConfig(func(c *config.Config) error {
		c.Notify = &config.NotifyConfig{Verify: config.Bool(false)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	srv.notifyJob(jobOutcome{job: jobVerify, tutDir: tutDir})
	n.noNotice(t)
	srv.notifyJob(jobOutcome{job: jobExtend, tutDir: tutDir})
	if got := n.nextNotice(t); !strings.HasPrefix(got[0], "Extend finished") {
		t.Errorf("extend notice = %q", got)
	}
}

// revealNative is a shell that can also show folders.
type revealNative struct {
	*fakeNative
	revealed []string
}

func (r *revealNative) RevealFolder(path string) error {
	r.revealed = append(r.revealed, path)
	return nil
}
