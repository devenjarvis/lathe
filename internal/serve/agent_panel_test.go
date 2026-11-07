package serve

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devenjarvis/lathe/internal/acp/acptest"
	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
)

// panelServer is a server with no hosted agent whose picker builds fake
// agents (reporting model) and sees only the programs in onPath.
func panelServer(t *testing.T, model string, onPath ...string) (*Server, *httptest.Server, string, *fakeHost) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	tutDir := makeTutFixture(t, dir, "series", true)
	srv := NewServer(dir)
	f := &fakeHost{}
	srv.hostFactory = func(command string) *agentHost {
		return newAgentHost(command, f.starter(func() *acptest.Agent {
			return &acptest.Agent{Model: model, Reply: func(string) string { return "ok" }}
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
	return srv, ts, tutDir, f
}

func postJSONBody(t *testing.T, url string, v any) (int, string) {
	t.Helper()
	body, _ := json.Marshal(v)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func TestAgentsListsDetection(t *testing.T) {
	_, ts, _, _ := panelServer(t, "", "claude", "claude-agent-acp")
	resp, err := http.Get(ts.URL + "/-/agents")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Agents []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Found bool   `json:"found"`
			Ready bool   `json:"ready"`
		} `json:"agents"`
		Current *agentChoice `json:"current"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Current != nil {
		t.Errorf("current = %+v before any pick, want none", out.Current)
	}
	if len(out.Agents) == 0 || out.Agents[0].ID != "claude" || !out.Agents[0].Found || !out.Agents[0].Ready {
		t.Errorf("agents = %+v, want claude first, found and ready", out.Agents)
	}
	for _, a := range out.Agents[1:] {
		if a.Found {
			t.Errorf("%s reported found with nothing on PATH", a.ID)
		}
	}
}

func TestAgentPickStartsAndRemembers(t *testing.T) {
	srv, ts, _, f := panelServer(t, "Claude Opus 5.5", "claude-agent-acp")

	code, body := postJSONBody(t, ts.URL+"/-/agent", map[string]string{"id": "claude"})
	if code != http.StatusOK {
		t.Fatalf("pick = %d %s", code, body)
	}
	if got := config.Agent(); got != "claude" {
		t.Errorf("config agent = %q, want claude", got)
	}
	h := srv.hostedAgent()
	if h == nil || h.command != "claude-agent-acp" || h.name != "Claude Code" || h.id != "claude" {
		t.Fatalf("hosted = %+v", h)
	}

	// The model shows once the agent has opened a session.
	code, out := askJSON(t, ts, "why?")
	if code != http.StatusOK || out["mode"] != "agent" {
		t.Fatalf("ask after pick = %d %v", code, out)
	}
	eventStream(t, ts, out["runId"], nil)
	st := h.status()
	if st.Name != "Claude Code" || st.Model != "Claude Opus 5.5" {
		t.Errorf("status = %+v, want name and model", st)
	}

	// Picking another agent swaps the host; run ids keep counting.
	if code, body := postJSONBody(t, ts.URL+"/-/agent", map[string]string{"id": "gemini"}); code != http.StatusOK {
		t.Fatalf("second pick = %d %s", code, body)
	}
	h2 := srv.hostedAgent()
	if h2 == h || h2.command != "gemini --experimental-acp" {
		t.Fatalf("host after switch = %+v", h2)
	}
	if h2.lastCounter() < 1 {
		t.Errorf("new host counter = %d, want it to continue from the old host", h2.lastCounter())
	}
	if got := config.Agent(); got != "gemini" {
		t.Errorf("config agent = %q, want gemini", got)
	}
	_ = f
}

func TestAgentPickRefusals(t *testing.T) {
	_, ts, _, _ := panelServer(t, "")
	if code, _ := postJSONBody(t, ts.URL+"/-/agent", map[string]string{"id": "rm -rf"}); code != http.StatusBadRequest {
		t.Errorf("unknown id = %d, want 400", code)
	}
	if got := config.Agent(); got != "" {
		t.Errorf("a refused pick was saved: %q", got)
	}

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/-/agent", strings.NewReader(`{"id":"claude"}`))
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site pick = %d, want 403", resp.StatusCode)
	}
}

func TestAgentPickRefusedWhileBusy(t *testing.T) {
	srv, ts, _, _ := panelServer(t, "")
	srv.hostFactory = func(command string) *agentHost {
		f := &fakeHost{}
		return newAgentHost(command, f.starter(func() *acptest.Agent {
			return &acptest.Agent{ChunkDelay: 50 * time.Millisecond, Reply: func(string) string { return strings.Repeat("word ", 40) }}
		}))
	}
	if code, body := postJSONBody(t, ts.URL+"/-/agent", map[string]string{"id": "claude"}); code != http.StatusOK {
		t.Fatalf("pick = %d %s", code, body)
	}
	code, out := askJSON(t, ts, "slow?")
	if code != http.StatusOK || out["mode"] != "agent" {
		t.Fatalf("ask = %d %v", code, out)
	}
	if code, _ := postJSONBody(t, ts.URL+"/-/agent", map[string]string{"id": "codex"}); code != http.StatusConflict {
		t.Errorf("switch while answering = %d, want 409", code)
	}
	if got := srv.hostedAgent().command; got != "claude-agent-acp" {
		t.Errorf("host changed while busy: %q", got)
	}
}

func TestWorkspaceEndpoint(t *testing.T) {
	_, ts, tutDir, _ := panelServer(t, "")
	target := filepath.Join(t.TempDir(), "synth")

	code, body := postJSONBody(t, ts.URL+"/-/workspace/series", map[string]string{"path": target})
	if code != http.StatusOK {
		t.Fatalf("workspace = %d %s", code, body)
	}
	var out map[string]any
	_ = json.Unmarshal([]byte(body), &out)
	if out["path"] != target || out["created"] != true || out["gitInit"] != true {
		t.Errorf("response = %v", out)
	}
	if _, err := os.Stat(filepath.Join(target, ".git")); err != nil {
		t.Errorf("no git repo in the new workspace: %v", err)
	}
	if ws, _ := store.ReadWorkspace(tutDir); ws == nil || ws.Path != target {
		t.Errorf("stored workspace = %+v", ws)
	}

	// The reading page shows it in the panel footer.
	resp, err := http.Get(ts.URL + "/series/part-01.md")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(page), `data-workspace="`+target+`"`) {
		t.Errorf("page does not carry the workspace %s", target)
	}

	if code, _ := postJSONBody(t, ts.URL+"/-/workspace/series", map[string]string{"path": "relative/dir"}); code != http.StatusBadRequest {
		t.Errorf("relative path = %d, want 400", code)
	}
	if code, _ := postJSONBody(t, ts.URL+"/-/workspace/nope", map[string]string{"path": target}); code != http.StatusNotFound {
		t.Errorf("unknown tutorial = %d, want 404", code)
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/-/workspace/series", strings.NewReader(`{"path":"/tmp/x"}`))
	req.Header.Set("Origin", "https://evil.example")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site workspace = %d, want 403", resp.StatusCode)
	}
}

func TestPagePanelStates(t *testing.T) {
	srv, ts, _, _ := panelServer(t, "")
	get := func() string {
		resp, err := http.Get(ts.URL + "/series/part-01.md")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	page := get()
	for _, want := range []string{
		`id="agentPanel"`, `id="agentToggle"`, `data-last-part="part-02.md"`, `data-agent-name=""`,
		`<span class="agent-toggle-label" id="agentToggleName">Agent</span><kbd class="agent-kbd agent-toggle-key" data-kbd aria-hidden="true">⌘J</kbd>`,
		`<span id="agentChipName">Pick an agent</span>`,
		`<span class="agent-menu-label" id="agentPickLabel">Pick an agent</span>`,
		`id="agentRestart" class="agent-menu-item" role="menuitem" tabindex="-1" data-menu="restart" hidden>`,
		`<div id="agentWorkspace" class="agent-context" hidden>`,
		"Bring your agent", "/lathe-work",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("first-run page missing %q", want)
		}
	}
	srv.SetAgent("claude-agent-acp")
	page = get()
	for _, want := range []string{
		`data-agent-name="Claude Code"`,
		`<span class="agent-toggle-label" id="agentToggleName">Claude Code</span>`,
		`<span id="agentChipName">Claude Code</span>`,
		`<span class="agent-menu-label" id="agentPickLabel">Switch agent</span>`,
		`id="agentRestart" class="agent-menu-item" role="menuitem" tabindex="-1" data-menu="restart">Restart agent</button>`,
		`<div id="agentWorkspace" class="agent-context">`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page with an agent missing %q", want)
		}
	}
}

// The panel's structure (#142): one header chip whose menu holds the agent's
// settings, a context strip, and one composer whose + menu holds the actions.
func TestPagePanelStructure(t *testing.T) {
	_, ts, _, _ := panelServer(t, "")
	resp, err := http.Get(ts.URL + "/series/part-01.md")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	page := string(b)

	for _, want := range []string{
		// Header chip and its menu.
		`id="agentChip" class="agent-chip" aria-haspopup="menu" aria-expanded="false" aria-controls="agentChipMenu"`,
		`id="agentChipMenu" class="agent-menu agent-chip-menu" role="menu" aria-labelledby="agentChip" hidden`,
		`id="agentMenuModel"`, `id="agentMenuAccess"`, `aria-describedby="agentIsolationDetail"`,
		`data-menu="workspace">Workspace…</button>`,
		`>Open in new window</span><kbd class="agent-kbd agent-menu-key" data-kbd aria-hidden="true">⌥⌘J</kbd>`,
		`<a href="/-/settings#agent" class="agent-menu-item" role="menuitem"`,
		`>Agent settings…</span><kbd class="agent-kbd agent-menu-key" data-kbd aria-hidden="true">⌘,</kbd>`,
		// Context strip.
		`id="agentPlace" class="agent-place" data-part-label="Part 1"`,
		`id="workspaceChange" class="agent-ws-btn" aria-controls="workspaceForm" aria-expanded="false"`,
		`<code id="workspaceShort" data-empty>Set a workspace</code>`,
		// Composer and its action menu.
		`id="agentActionsButton" class="agent-plus" aria-haspopup="menu" aria-expanded="false" aria-controls="agentActionMenu"`,
		`placeholder="Ask about Part 1…"`,
		`id="agentActionMenu" class="agent-menu agent-action-menu" role="menu" aria-label="Agent actions" hidden`,
		`data-agent-action="check" aria-labelledby="actCheckLabel" aria-describedby="actCheckDesc"`,
		`>Check my work</span><kbd class="agent-kbd agent-menu-key" data-kbd aria-hidden="true">⇧⌘K</kbd>`,
		`>Verify tutorial</span><kbd class="agent-kbd agent-menu-key" data-kbd aria-hidden="true">⇧⌘V</kbd>`,
		`>Write Part 3</span><kbd class="agent-kbd agent-menu-key" data-kbd aria-hidden="true">⇧⌘N</kbd>`,
		// The one suggestion (on by default; .PanelSuggestions).
		`id="agentSuggestion" class="agent-suggestion"`,
		// The job slots the panel's actions feed stay where they were.
		`id="verifyLive"`, `id="extendLive"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	for _, gone := range []string{
		`class="agent-actions"`, `data-agent-action="ask"`, `id="agentPickName"`,
		`id="agentPopout" class="icon-btn"`, `id="saveProgressButton"`, `<footer class="agent-workspace"`,
	} {
		if strings.Contains(page, gone) {
			t.Errorf("page still has %q", gone)
		}
	}
}

// With suggestions turned off the page carries no suggestion slot at all.
func TestPanelSuggestionsOff(t *testing.T) {
	srv, ts, _, _ := panelServer(t, "")
	srv.noPanelSuggestions = true
	resp, err := http.Get(ts.URL + "/series/part-01.md")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if strings.Contains(string(b), `id="agentSuggestion"`) {
		t.Error("suggestions are off but the page has a suggestion slot")
	}
}

func TestAgentRestart(t *testing.T) {
	srv, ts, _, f := panelServer(t, "")
	if code, _ := postJSONBody(t, ts.URL+"/-/agent/restart", map[string]string{}); code != http.StatusConflict {
		t.Errorf("restart with no agent = %d, want 409", code)
	}
	if code, body := postJSONBody(t, ts.URL+"/-/agent", map[string]string{"id": "claude"}); code != http.StatusOK {
		t.Fatalf("pick = %d %s", code, body)
	}
	old := srv.hostedAgent()
	code, out := askJSON(t, ts, "why?")
	if code != http.StatusOK || out["mode"] != "agent" {
		t.Fatalf("ask = %d %v", code, out)
	}
	eventStream(t, ts, out["runId"], nil)

	code, body := postJSONBody(t, ts.URL+"/-/agent/restart", map[string]string{})
	if code != http.StatusOK {
		t.Fatalf("restart = %d %s", code, body)
	}
	h := srv.hostedAgent()
	if h == old || h.command != old.command || h.id != "claude" || h.name != "Claude Code" {
		t.Fatalf("host after restart = %+v (old %+v)", h, old)
	}
	if h.lastCounter() < 1 {
		t.Errorf("restarted host counter = %d, want it to continue", h.lastCounter())
	}
	// The fresh host starts its own agent process.
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.mu.Lock()
		starts := f.starts
		f.mu.Unlock()
		if starts >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent starts = %d after restart, want 2", starts)
		}
		time.Sleep(10 * time.Millisecond)
	}

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/-/agent/restart", strings.NewReader(`{}`))
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site restart = %d, want 403", resp.StatusCode)
	}
}
