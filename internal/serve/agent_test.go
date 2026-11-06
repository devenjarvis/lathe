package serve

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devenjarvis/lathe/internal/acp"
	"github.com/devenjarvis/lathe/internal/acp/acptest"
)

// fakeHost hosts an in-process acptest agent. Each (re)start gets a fresh
// agent from newAgent, so a crash can be followed by a working restart.
type fakeHost struct {
	mu     sync.Mutex
	agents []*acptest.Agent
	starts int
	fail   error
}

func (f *fakeHost) starter(newAgent func() *acptest.Agent) agentStarter {
	return func(ctx context.Context) (*acp.Client, error) {
		f.mu.Lock()
		f.starts++
		fail := f.fail
		f.mu.Unlock()
		if fail != nil {
			return nil, fail
		}
		a := newAgent()
		f.mu.Lock()
		f.agents = append(f.agents, a)
		f.mu.Unlock()
		toAgentR, toAgentW := io.Pipe()
		fromAgentR, fromAgentW := io.Pipe()
		go func() {
			_ = a.Serve(toAgentR, fromAgentW)
			_ = fromAgentW.Close()
		}()
		c := acp.NewClient(fromAgentR, toAgentW, toAgentW.Close)
		if _, err := c.Initialize(ctx); err != nil {
			return nil, err
		}
		return c, nil
	}
}

func (f *fakeHost) agent(i int) *acptest.Agent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.agents[i]
}

func newAgentServer(t *testing.T, f *fakeHost, reply string) (*Server, *httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	tutDir := makeTutFixture(t, dir, "series", true)
	srv := NewServer(dir)
	srv.setAgentHost(newAgentHost("fake-acp", f.starter(func() *acptest.Agent {
		return &acptest.Agent{Reply: func(string) string { return reply }}
	})))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); srv.Close() })
	return srv, ts, tutDir
}

func askJSON(t *testing.T, ts *httptest.Server, question string) (int, map[string]string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"question": question})
	resp, err := http.Post(ts.URL+"/-/ask/series/part-02.md", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// eventStream reads a run's SSE stream, calling onEvent for each event, until
// the server ends it.
func eventStream(t *testing.T, ts *httptest.Server, runID string, onEvent func(runEvent)) []runEvent {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/-/acp/"+runID+"/events", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}
	var evs []runEvent
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev runEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			t.Fatalf("bad event %q: %v", line, err)
		}
		evs = append(evs, ev)
		if onEvent != nil {
			onEvent(ev)
		}
	}
	return evs
}

func last(evs []runEvent) runEvent {
	if len(evs) == 0 {
		return runEvent{}
	}
	return evs[len(evs)-1]
}

func streamedText(evs []runEvent) string {
	var b strings.Builder
	for _, ev := range evs {
		if ev.Type == "chunk" {
			b.WriteString(ev.Text)
		}
	}
	return b.String()
}

func TestAgentAskStreamsAnswer(t *testing.T) {
	f := &fakeHost{}
	_, ts, tutDir := newAgentServer(t, f, "Look at the **modulo** in §3.")

	code, out := askJSON(t, ts, "why wrap the phase?")
	if code != http.StatusOK || out["mode"] != "agent" || out["runId"] == "" {
		t.Fatalf("ask = %d %v, want mode=agent with a runId", code, out)
	}
	evs := eventStream(t, ts, out["runId"], nil)
	if got := streamedText(evs); got != "Look at the **modulo** in §3." {
		t.Errorf("streamed %q", got)
	}
	done := last(evs)
	if done.Type != "done" || done.StopReason != "end_turn" {
		t.Fatalf("last event = %+v, want done/end_turn", done)
	}
	if !strings.Contains(done.HTML, "<strong>modulo</strong>") {
		t.Errorf("done html not rendered markdown: %q", done.HTML)
	}

	a := f.agent(0)
	if len(a.Cwds) != 1 || a.Cwds[0] != tutDir {
		t.Errorf("session cwd = %v, want %s", a.Cwds, tutDir)
	}
	first := a.Prompts[0]
	for _, want := range []string{"Lathe — Ask About a Part", "/lathe-ask series part-02.md\nwhy wrap the phase?"} {
		if !strings.Contains(first, want) {
			t.Errorf("first prompt missing %q", want)
		}
	}
	if strings.Contains(first, "description:") {
		t.Error("first prompt should carry the skill without its frontmatter")
	}

	// A follow-up on the same part reuses the session and skips the skill text.
	_, out = askJSON(t, ts, "and the second oscillator?")
	eventStream(t, ts, out["runId"], nil)
	if len(a.Cwds) != 1 {
		t.Errorf("follow-up opened a new session: %v", a.Cwds)
	}
	if p := a.Prompts[1]; strings.Contains(p, "Ask About a Part") || !strings.Contains(p, "and the second oscillator?") {
		t.Errorf("follow-up prompt = %q", p)
	}
}

func TestAgentPermissionFromUI(t *testing.T) {
	f := &fakeHost{}
	_, ts, _ := newAgentServer(t, f, "Done reading.")

	_, out := askJSON(t, ts, "[permission] read part two")
	runID := out["runId"]
	evs := eventStream(t, ts, runID, func(ev runEvent) {
		if ev.Type != "permission" {
			return
		}
		if ev.Title != "Read part-02.md" || len(ev.Options) != 2 {
			t.Errorf("permission event = %+v", ev)
		}
		// A cross-site page can't answer for the reader.
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/-/acp/"+runID+"/permission",
			strings.NewReader(`{"permissionId":"`+ev.ID+`","optionId":"allow"}`))
		req.Header.Set("Origin", "https://evil.example")
		if resp, err := http.DefaultClient.Do(req); err == nil {
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("cross-origin permission = %d, want 403", resp.StatusCode)
			}
			_ = resp.Body.Close()
		}
		resp, err := http.Post(ts.URL+"/-/acp/"+runID+"/permission", "application/json",
			strings.NewReader(`{"permissionId":"`+ev.ID+`","optionId":"allow"}`))
		if err != nil {
			t.Error(err)
			return
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Errorf("permission answer = %d", resp.StatusCode)
		}
	})
	var resolved *runEvent
	for i := range evs {
		if evs[i].Type == "permission_resolved" {
			resolved = &evs[i]
		}
	}
	if resolved == nil || resolved.Choice != "allow" {
		t.Fatalf("no permission_resolved/allow in %+v", evs)
	}
	if got := streamedText(evs); !strings.HasPrefix(got, "(permission granted) ") {
		t.Errorf("text = %q", got)
	}

	// Answering again is a conflict, not a hang.
	resp, _ := http.Post(ts.URL+"/-/acp/"+runID+"/permission", "application/json",
		strings.NewReader(`{"permissionId":"`+resolved.ID+`","optionId":"allow"}`))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("stale permission answer = %d, want 409", resp.StatusCode)
	}
}

func TestAgentCrashFallsBackAndRestarts(t *testing.T) {
	f := &fakeHost{}
	srv, ts, _ := newAgentServer(t, f, "Fresh answer.")

	_, out := askJSON(t, ts, "[crash] boom")
	evs := eventStream(t, ts, out["runId"], nil)
	end := last(evs)
	if end.Type != "error" || !strings.Contains(end.Message, "exited") {
		t.Fatalf("last event = %+v, want an error saying the agent exited", end)
	}
	if end.Command != "/lathe-ask series part-02.md\n[crash] boom" {
		t.Errorf("fallback command = %q", end.Command)
	}
	if st := srv.agent.status(); st.State != "down" || st.Error == "" {
		t.Errorf("status after crash = %+v, want down with a reason", st)
	}

	// /-/worker reports it for the drawer's indicator.
	resp, _ := http.Get(ts.URL + "/-/worker")
	var worker struct {
		Agent agentStatus `json:"agent"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&worker)
	_ = resp.Body.Close()
	if worker.Agent.State != "down" || worker.Agent.Command != "fake-acp" {
		t.Errorf("/-/worker agent = %+v", worker.Agent)
	}

	// The next question starts a fresh agent with a fresh, primed session.
	_, out = askJSON(t, ts, "try again")
	evs = eventStream(t, ts, out["runId"], nil)
	if last(evs).Type != "done" || streamedText(evs) != "Fresh answer." {
		t.Fatalf("after restart: %+v", evs)
	}
	if f.starts != 2 {
		t.Errorf("starts = %d, want 2", f.starts)
	}
	if p := f.agent(1).Prompts[0]; !strings.Contains(p, "Ask About a Part") {
		t.Error("restarted agent's session was not primed with the skill")
	}
}

func TestAgentStartFailureHandsOff(t *testing.T) {
	f := &fakeHost{fail: errors.New("exec: \"claude-agent-acp\": not found")}
	_, ts, _ := newAgentServer(t, f, "")
	code, out := askJSON(t, ts, "hello?")
	if code != http.StatusOK || out["mode"] != "handoff" {
		t.Fatalf("ask = %d %v, want handoff", code, out)
	}
	if out["command"] != "/lathe-ask series part-02.md\nhello?" {
		t.Errorf("command = %q", out["command"])
	}
	if !strings.Contains(out["notice"], "not found") {
		t.Errorf("notice = %q, want the start error", out["notice"])
	}
}

func TestAgentBusyAndCancel(t *testing.T) {
	f := &fakeHost{}
	_, ts, _ := newAgentServer(t, f, strings.Repeat("word ", 400))

	_, out := askJSON(t, ts, "[slow] long one")
	runID := out["runId"]

	// A second question on the same part while the first streams is refused.
	code, _ := askJSON(t, ts, "another")
	if code != http.StatusConflict {
		t.Errorf("concurrent ask = %d, want 409", code)
	}

	var once sync.Once
	evs := eventStream(t, ts, runID, func(ev runEvent) {
		if ev.Type != "chunk" {
			return
		}
		once.Do(func() {
			resp, err := http.Post(ts.URL+"/-/acp/"+runID+"/cancel", "application/json", nil)
			if err == nil {
				_ = resp.Body.Close()
			}
		})
	})
	end := last(evs)
	if end.Type != "done" || end.StopReason != "cancelled" {
		t.Fatalf("last event = %+v, want done/cancelled", end)
	}

	// Finished: a reconnect past the end gets 204 so EventSource stops, and
	// one with an earlier Last-Event-ID resumes after it.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/-/acp/"+runID+"/events", nil)
	req.Header.Set("Last-Event-ID", "100000")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("reconnect after finish = %d, want 204", resp.StatusCode)
	}
	req.Header.Set("Last-Event-ID", "0")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if strings.Contains(string(raw), "id: 0\n") || !strings.Contains(string(raw), "id: 1\n") {
		t.Errorf("resume did not start after Last-Event-ID 0:\n%.200s", raw)
	}

	// The part is free again.
	if code, out := askJSON(t, ts, "ok now"); code != http.StatusOK || out["mode"] != "agent" {
		t.Errorf("ask after cancel = %d %v", code, out)
	}
}

func TestAgentEndpointsWithoutAgent(t *testing.T) {
	dir := t.TempDir()
	makeTutFixture(t, dir, "series", true)
	srv := NewServer(dir)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/-/acp/1/events"},
		{http.MethodPost, "/-/acp/1/permission"},
		{http.MethodPost, "/-/acp/1/cancel"},
	} {
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}")))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", tc.method, tc.path, w.Code)
		}
	}
	// And Ask keeps its old behaviour.
	w := postAsk(t, srv, "series", "part-01.md", []byte(`{"question":"hi"}`))
	if !strings.Contains(w.Body.String(), `"mode":"handoff"`) || strings.Contains(w.Body.String(), "notice") {
		t.Errorf("no-agent ask = %s", w.Body.String())
	}
}

// Real agents split a turn into several messages around tool calls. Each
// message must start its own markdown block, or "…part 2.## Heading" renders
// as one paragraph.
func TestAgentMessagesAroundToolCallsStaySeparate(t *testing.T) {
	f := &fakeHost{}
	_, ts, _ := newAgentServer(t, f, "## Short answer\n\nIt wraps the phase.")
	_, out := askJSON(t, ts, "[tool] why?")
	evs := eventStream(t, ts, out["runId"], nil)
	done := last(evs)
	if done.Type != "done" {
		t.Fatalf("last = %+v", done)
	}
	for _, want := range []string{"<p>Let me check part 2.</p>", "<h2", "Short answer</h2>"} {
		if !strings.Contains(done.HTML, want) {
			t.Errorf("done html missing %q:\n%s", want, done.HTML)
		}
	}
}

// The drawer shows formatted markdown while the answer streams, not raw
// asterisks until the end.
func TestAgentStreamsRenderedHTML(t *testing.T) {
	f := &fakeHost{}
	srv, ts, _ := newAgentServer(t, f, "")
	srv.agent.start = f.starter(func() *acptest.Agent {
		return &acptest.Agent{
			ChunkDelay: 30 * time.Millisecond,
			Reply:      func(string) string { return "Look at **the modulo** first. " + strings.Repeat("Then more words. ", 20) },
		}
	})
	_, out := askJSON(t, ts, "why?")
	evs := eventStream(t, ts, out["runId"], nil)
	var sawLive bool
	for _, ev := range evs {
		if ev.Type == "html" && strings.Contains(ev.HTML, "<strong>the modulo</strong>") {
			sawLive = true
			break
		}
	}
	if !sawLive {
		t.Error("no rendered html event before done")
	}
	if last(evs).Type != "done" {
		t.Errorf("last = %+v", last(evs))
	}
}

func TestAskRunSplitsOnMessageID(t *testing.T) {
	r := &askRun{notify: make(chan struct{}), perms: map[string]chan string{}}
	chunk := func(id, text string) {
		raw, _ := json.Marshal(map[string]string{"type": "text", "text": text})
		r.Update(acp.SessionUpdate{Kind: "agent_message_chunk", MessageID: id, Content: raw})
	}
	chunk("m1", "First ")
	chunk("m1", "message.")
	chunk("m2", "- a list item")
	chunk("", " continues")
	if got, want := r.answer(), "First message.\n\n- a list item continues"; got != want {
		t.Errorf("answer = %q, want %q", got, want)
	}
}

func TestStaleHTMLSnapshotsAreDropped(t *testing.T) {
	r := &askRun{notify: make(chan struct{}), perms: map[string]chan string{}}
	r.push(runEvent{Type: "html", HTML: "<p>a</p>"})
	r.push(runEvent{Type: "chunk", Text: "b"})
	r.push(runEvent{Type: "html", HTML: "<p>ab</p>"})
	evs, _, _ := r.since(0)
	if len(evs) != 3 || evs[0].Type != "html_stale" || evs[0].HTML != "" || evs[2].HTML != "<p>ab</p>" {
		t.Errorf("events = %+v", evs)
	}
}

func TestAgentSessionGetsLatheMCPServer(t *testing.T) {
	t.Setenv("HOME", "/home/reader")
	f := &fakeHost{}
	srv, ts, _ := newAgentServer(t, f, "ok")
	srv.agent.mcp = latheMCP("/opt/bin/lathe")

	_, out := askJSON(t, ts, "what is a phase accumulator?")
	eventStream(t, ts, out["runId"], nil)

	a := f.agent(0)
	if len(a.MCPServers) != 1 {
		t.Fatalf("session/new calls = %d, want 1", len(a.MCPServers))
	}
	var got []acp.MCPServer
	if err := json.Unmarshal(a.MCPServers[0], &got); err != nil {
		t.Fatalf("mcpServers = %s: %v", a.MCPServers[0], err)
	}
	want := acp.MCPServer{
		Name:    "lathe",
		Command: "/opt/bin/lathe",
		Args:    []string{"mcp", "--slug", "series"},
		Env:     []acp.EnvVariable{{Name: "HOME", Value: "/home/reader"}},
	}
	if len(got) != 1 || got[0].Name != want.Name || got[0].Command != want.Command ||
		strings.Join(got[0].Args, " ") != strings.Join(want.Args, " ") ||
		len(got[0].Env) != 1 || got[0].Env[0] != want.Env[0] {
		t.Errorf("mcpServers = %+v, want [%+v]", got, want)
	}
}

func TestAgentSessionWithoutMCPSendsEmptyList(t *testing.T) {
	f := &fakeHost{}
	_, ts, _ := newAgentServer(t, f, "ok")
	_, out := askJSON(t, ts, "hi")
	eventStream(t, ts, out["runId"], nil)
	if got := string(f.agent(0).MCPServers[0]); got != "[]" {
		t.Errorf("mcpServers = %s, want [] (the field is required)", got)
	}
}

func workerAgentStatus(t *testing.T, ts *httptest.Server) agentStatus {
	t.Helper()
	resp, err := http.Get(ts.URL + "/-/worker")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Agent agentStatus `json:"agent"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out.Agent
}

func TestAgentAskPolicyEnforced(t *testing.T) {
	f := &fakeHost{}
	srv, ts, tutDir := newAgentServer(t, f, "answer")
	if _, err := srv.agent.ensure(); err != nil {
		t.Fatal(err)
	}
	if iso := workerAgentStatus(t, ts).Isolation; iso == nil || iso.State != "unknown" {
		t.Errorf("isolation before any file access = %+v, want unknown", iso)
	}

	outside := filepath.Join(filepath.Dir(tutDir), "outside.txt")
	_, out := askJSON(t, ts, "[read:part-02.md] [write:"+outside+"] [run:touch pwned]")
	evs := eventStream(t, ts, out["runId"], nil)
	text := streamedText(evs)
	for _, want := range []string{"(read ok:", "(write refused:", "(run refused:"} {
		if !strings.Contains(text, want) {
			t.Errorf("reply %q lacks %q", text, want)
		}
	}
	for _, p := range []string{outside, filepath.Join(tutDir, "pwned")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("ask policy let the agent create %s", p)
		}
	}

	var acts []acp.Activity
	for _, ev := range evs {
		if ev.Type == "activity" && ev.Activity != nil {
			acts = append(acts, *ev.Activity)
		}
	}
	if len(acts) != 3 || acts[0].Kind != "read" || acts[0].Denied ||
		acts[1].Kind != "write" || !acts[1].Denied || acts[2].Kind != "terminal" || !acts[2].Denied {
		t.Errorf("activity events = %+v", acts)
	}
	if !strings.Contains(f.agent(0).Prompts[0], "may not write files or run commands") {
		t.Error("first prompt should tell the agent the Ask policy")
	}
	if iso := workerAgentStatus(t, ts).Isolation; iso == nil || iso.State != "enforced" {
		t.Errorf("isolation = %+v, want enforced", iso)
	}
}

func TestAgentOwnToolsReportSoftIsolation(t *testing.T) {
	f := &fakeHost{}
	_, ts, _ := newAgentServer(t, f, "answer")
	_, out := askJSON(t, ts, "[owntool] what does part 1 say?")
	eventStream(t, ts, out["runId"], nil)
	if iso := workerAgentStatus(t, ts).Isolation; iso == nil || iso.State != "soft" {
		t.Errorf("isolation = %+v, want soft", iso)
	}
}
