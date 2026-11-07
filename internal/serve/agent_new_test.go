package serve

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devenjarvis/lathe/internal/acp"
	"github.com/devenjarvis/lathe/internal/acp/acptest"
	"github.com/devenjarvis/lathe/internal/mcp"
	"github.com/devenjarvis/lathe/internal/queue"
	"github.com/devenjarvis/lathe/internal/store"
)

// newTutServer serves an empty library in a scratch HOME (the MCP tools write
// through config.TutorialsDir, so the server must read the same dir), hosting
// a fake agent when mk is set.
func newTutServer(t *testing.T, f *fakeHost, mk func() *acptest.Agent) (*Server, *httptest.Server, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	tutorials := filepath.Join(home, ".lathe", "tutorials")
	if err := os.MkdirAll(tutorials, 0o755); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(tutorials)
	if mk != nil {
		h := newAgentHost("fake-acp", f.starter(mk))
		h.mcp = latheMCP("/usr/local/bin/lathe")
		srv.setAgentHost(h)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); srv.Close() })
	return srv, ts, tutorials
}

// mcpCall runs one tool on an in-process Lathe MCP server, the way the
// hosted agent's `lathe mcp --slug <slug> --new` subprocess would.
func mcpCall(t *testing.T, srv *mcp.Server, name string, args map[string]any) (string, bool) {
	t.Helper()
	toR, toW := io.Pipe()
	fromR, fromW := io.Pipe()
	go func() { _ = srv.Serve(toR, fromW); _ = fromW.Close() }()
	defer func() { _ = toW.Close() }()
	conn := acp.NewConn(fromR, toW, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := conn.Call(ctx, "tools/call", map[string]any{"name": name, "arguments": args}, &out); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if len(out.Content) != 1 {
		t.Fatalf("%s content = %+v", name, out.Content)
	}
	return out.Content[0].Text, out.IsError
}

var (
	newSlugInPrompt    = regexp.MustCompile(`reserved the slug (\S+) for this tutorial`)
	newScratchInPrompt = regexp.MustCompile(`scratch dir for this run: (\S+) \(`)
)

func promptMatch(t *testing.T, re *regexp.Regexp, prompt string) string {
	t.Helper()
	m := re.FindStringSubmatch(prompt)
	if m == nil {
		t.Fatalf("%s not in prompt:\n%.600s", re, prompt)
	}
	return m[1]
}

// writePart1 does what the skill does before storing: part-01.md in
// <scratch>/<slug>/.
func writePart1(t *testing.T, prompt string) (slug, src string) {
	t.Helper()
	slug = promptMatch(t, newSlugInPrompt, prompt)
	src = filepath.Join(promptMatch(t, newScratchInPrompt, prompt), slug)
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Error(err)
	}
	if err := os.WriteFile(filepath.Join(src, "part-01.md"), []byte("# A synth in Zig\n"), 0o644); err != nil {
		t.Error(err)
	}
	return slug, src
}

func postNew(t *testing.T, ts *httptest.Server, body any) (int, map[string]string) {
	t.Helper()
	raw, _ := json.Marshal(body)
	return postJob(t, ts, "/-/new", string(raw))
}

func TestAgentNewStoresIntoTheReservation(t *testing.T) {
	f := &fakeHost{}
	var (
		mu                   sync.Mutex
		duringStatus         store.Status
		duringWriting        bool
		refusal, storeResult string
		scratch              string
		baseURL              string // set before the first POST
	)
	ws := t.TempDir()
	srv, ts, tutorials := newTutServer(t, f, func() *acptest.Agent {
		return &acptest.Agent{
			Plan: []string{"Pin versions", "Write Part 1", "Store"},
			Reply: func(prompt string) string {
				slug, src := writePart1(t, prompt)
				mu.Lock()
				defer mu.Unlock()
				scratch = filepath.Dir(src)
				// The reservation is in the library while the agent works.
				dir, _ := store.TutorialPath(slug)
				if tut, err := store.ReadMetadata(dir); err == nil {
					duringStatus = tut.Status
				}
				if resp, err := http.Get(baseURL + "/?view=writing"); err == nil {
					body, _ := io.ReadAll(resp.Body)
					_ = resp.Body.Close()
					duringWriting = strings.Contains(string(body), `data-slug="`+slug+`"`) && strings.Contains(string(body), "Writing Part 1")
				}
				// The session's MCP server: scoped to the reservation, new mode.
				m := &mcp.Server{Slug: slug, New: true}
				refusal, _ = mcpCall(t, m, "lathe_store", map[string]any{"path": src, "slug": "someone-else"})
				text, isErr := mcpCall(t, m, "lathe_store", map[string]any{"path": src, "title": "A Synth In Zig", "voice": "companion", "tools": []map[string]string{{"name": "zig", "version": "0.13.0"}}})
				if isErr {
					t.Errorf("scoped store failed: %s", text)
				}
				storeResult = text
				return "Part 1 builds a sine oscillator. Stored as " + slug + "."
			},
		}
	})
	baseURL = ts.URL
	sh, err := store.UpdateShelves(srv.shelvesPath(), func(s *store.Shelves) error { _, err := s.Create("Audio"); return err })
	if err != nil {
		t.Fatal(err)
	}
	shelf := sh.Shelves[0].ID

	code, out := postNew(t, ts, map[string]any{
		"topic": "build a digital synth in Zig", "level": "beginner", "voice": "companion",
		"workspace": ws, "tools": []map[string]string{{"name": "Zig", "version": "0.13.0"}}, "shelf": shelf,
	})
	if code != http.StatusOK || out["mode"] != "agent" || out["runId"] == "" || out["slug"] != "digital-synth-zig" {
		t.Fatalf("POST /-/new = %d %v, want mode agent with the reserved slug", code, out)
	}
	evs := eventStream(t, ts, out["runId"], nil)
	if end := last(evs); end.Type != "done" || end.StopReason != "end_turn" || end.Message != "" || !strings.Contains(end.HTML, "sine oscillator") {
		t.Fatalf("last event = %+v, want done/end_turn with the summary and no cleanup note", end)
	}
	var plans, tools int
	for _, ev := range evs {
		switch ev.Type {
		case "plan":
			plans++
		case "tool":
			tools++
		}
	}
	if plans == 0 || tools != 3 {
		t.Errorf("plan/tool events = %d/%d, want plans and 3 tool calls", plans, tools)
	}

	mu.Lock()
	gotStatus, gotWriting, gotRefusal, gotStore, gotScratch := duringStatus, duringWriting, refusal, storeResult, scratch
	mu.Unlock()
	if gotStatus != store.StatusWriting || !gotWriting {
		t.Errorf("while running: status %q, listed under Being written %v", gotStatus, gotWriting)
	}
	if !strings.Contains(gotRefusal, "scoped to") {
		t.Errorf("store into another slug = %q, want refused", gotRefusal)
	}
	if !strings.Contains(gotStore, "digital-synth-zig") {
		t.Errorf("store result = %q", gotStore)
	}
	if _, err := os.Stat(filepath.Join(tutorials, "someone-else")); !os.IsNotExist(err) {
		t.Errorf("the refused store created another tutorial (err %v)", err)
	}

	tutDir := filepath.Join(tutorials, "digital-synth-zig")
	tut, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatal(err)
	}
	if tut.Status != store.StatusUnverified || len(tut.Parts) != 1 || tut.Title != "A Synth In Zig" || tut.Voice != "companion" {
		t.Errorf("stored tutorial = %+v", tut)
	}
	if got, _ := store.ReadShelves(srv.shelvesPath()); got.ShelfOf("digital-synth-zig").ID != shelf {
		t.Errorf("tutorial is on shelf %q, want %q", got.ShelfOf("digital-synth-zig").ID, shelf)
	}
	if w, err := store.ReadWorkspace(tutDir); err != nil || w == nil || w.Path != ws {
		t.Errorf("workspace = %+v, %v; want %s", w, err, ws)
	}
	if _, err := os.Stat(gotScratch); !os.IsNotExist(err) {
		t.Errorf("scratch dir %s still exists (err %v)", gotScratch, err)
	}

	a := f.agent(0)
	prompt := a.Prompts[0]
	for _, want := range []string{
		`<skill name="lathe">`, `<voice name="companion">`, "Experience level: beginner", "zig 0.13.0 — pinned",
		"lathe_store", "Workspace: " + ws,
		"/lathe build a digital synth in Zig --level beginner --voice companion --workspace " + ws + " --tool zig:0.13.0",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
	if a.Cwds[0] != gotScratch {
		t.Errorf("session cwd = %q, want the scratch dir %q", a.Cwds[0], gotScratch)
	}
	var servers []struct {
		Args []string `json:"args"`
	}
	_ = json.Unmarshal(a.MCPServers[0], &servers)
	if len(servers) != 1 || strings.Join(servers[0].Args, " ") != "mcp --slug digital-synth-zig --new" {
		t.Errorf("mcpServers = %s, want lathe mcp --slug digital-synth-zig --new", a.MCPServers[0])
	}

	// The same topic again reserves the next slug.
	code, out = postNew(t, ts, map[string]any{"topic": "build a digital synth in Zig", "level": "beginner"})
	if code != http.StatusOK || out["slug"] != "digital-synth-zig-2" {
		t.Errorf("second POST /-/new = %d %v, want digital-synth-zig-2", code, out)
	}
	eventStream(t, ts, out["runId"], nil)
}

func TestAgentNewPolicyAndEndingWithoutStoreRemovesReservation(t *testing.T) {
	f := &fakeHost{}
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, ts, tutorials := newTutServer(t, f, func() *acptest.Agent {
		return &acptest.Agent{Reply: func(string) string { return "I gave up." }}
	})
	// The topic's markers make the fake agent exercise the new policy.
	topic := "notes [read:" + ws + "/go.mod] [write:" + ws + "/hack.txt] [read:" + tutorials + "] [run:pwd]"
	code, out := postNew(t, ts, map[string]any{"topic": topic, "slug": "notes", "level": "beginner", "tools": []map[string]string{{"name": "go", "version": "1.22"}}, "workspace": ws})
	if code != http.StatusOK || out["mode"] != "agent" || out["slug"] != "notes" {
		t.Fatalf("POST /-/new = %d %v", code, out)
	}
	// Its turn ended without storing, so the run waits for the reader (an
	// input event); the reader stops it instead of replying.
	evs := eventStream(t, ts, out["runId"], func(ev runEvent) {
		if ev.Type == "input" {
			resp, err := http.Post(ts.URL+"/-/acp/"+out["runId"]+"/cancel", "application/json", nil)
			if err != nil {
				t.Error(err)
				return
			}
			_ = resp.Body.Close()
		}
	})
	text := streamedText(evs)
	for _, want := range []string{"(read ok", "(write refused", "(run exit 0: ", "lathe-new-"} {
		if !strings.Contains(text, want) {
			t.Errorf("policy outcomes %q are missing %q", text, want)
		}
	}
	if strings.Count(text, "(read ok") != 2 || strings.Count(text, "(read refused") != 2 {
		t.Errorf("reads = %q, want the workspace allowed and the tutorials dir refused (each marker appears twice in the prompt)", text)
	}
	if _, err := os.Stat(filepath.Join(ws, "hack.txt")); !os.IsNotExist(err) {
		t.Errorf("the agent wrote into the workspace (err %v)", err)
	}
	end := last(evs)
	if end.Type != "done" || end.StopReason != "cancelled" || !strings.Contains(end.Message, "removed the reservation") {
		t.Errorf("last event = %+v, want done/cancelled with the removal note", end)
	}
	if _, err := os.Stat(filepath.Join(tutorials, "notes")); !os.IsNotExist(err) {
		t.Errorf("an unstored reservation was left behind (err %v)", err)
	}
}

func TestAgentNewCancelRemovesReservation(t *testing.T) {
	f := &fakeHost{}
	started := make(chan string, 1)
	var once sync.Once
	_, ts, tutorials := newTutServer(t, f, func() *acptest.Agent {
		return &acptest.Agent{
			ChunkDelay: 20 * time.Millisecond,
			Reply: func(prompt string) string {
				slug, _ := writePart1(t, prompt)
				once.Do(func() { started <- slug })
				return strings.Repeat("researching ", 500)
			},
		}
	})
	code, out := postNew(t, ts, map[string]any{"topic": "a compiler in Rust", "level": "experienced"})
	if code != http.StatusOK || out["mode"] != "agent" {
		t.Fatalf("POST /-/new = %d %v", code, out)
	}
	slug := <-started
	if slug != "compiler-rust" || out["slug"] != slug {
		t.Fatalf("slug = %q (response %q), want compiler-rust", slug, out["slug"])
	}

	// While it runs, the reservation is busy: no verify or extend, and an
	// explicit request for the same slug is a conflict.
	for _, path := range []string{"/-/verify/" + slug, "/-/extend/" + slug} {
		if code, _ := postJob(t, ts, path, `{}`); code != http.StatusConflict {
			t.Errorf("POST %s during a new run = %d, want 409", path, code)
		}
	}
	if code, _ := postNew(t, ts, map[string]any{"topic": "again", "slug": slug}); code != http.StatusConflict {
		t.Errorf("POST /-/new for the running slug = %d, want 409", code)
	}
	if resp, err := http.Get(ts.URL + "/" + slug + "/"); err == nil {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "You can close this window") || !strings.Contains(string(body), `action="/-/new/`+slug+`/cancel"`) {
			t.Errorf("GET /%s/ = %d %.300q, want the progress page with Stop writing", slug, resp.StatusCode, body)
		}
	}

	resp, err := http.Post(ts.URL+"/-/new/"+slug+"/cancel", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel = %d, want 204", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(tutorials, slug)); !os.IsNotExist(err) {
		t.Errorf("reservation still there after cancel (err %v)", err)
	}
	end := last(eventStream(t, ts, out["runId"], nil))
	if end.Type != "done" || end.StopReason != "cancelled" || !strings.Contains(end.Message, "removed the reservation") {
		t.Errorf("last event = %+v, want done/cancelled with the removal note", end)
	}
	// Cancelling a verify of it is not the same job.
	resp, _ = http.Post(ts.URL+"/-/verify/"+slug+"/cancel", "application/json", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("verify cancel = %d, want 404", resp.StatusCode)
	}
}

func TestAgentNewQuestionWaitsForTheReadersReply(t *testing.T) {
	f := &fakeHost{}
	var turns int
	var mu sync.Mutex
	_, ts, tutorials := newTutServer(t, f, func() *acptest.Agent {
		return &acptest.Agent{Reply: func(prompt string) string {
			mu.Lock()
			turns++
			n := turns
			mu.Unlock()
			if n == 1 {
				return "I found **Zig 0.13.0** installed. Write against that?"
			}
			// The reply arrives as the next prompt in the same session.
			if prompt != "Yes, 0.13.0." {
				t.Errorf("second prompt = %q, want the reader's reply", prompt)
			}
			home, _ := os.UserHomeDir()
			src := filepath.Join(home, "src", "synth")
			_ = os.MkdirAll(src, 0o755)
			_ = os.WriteFile(filepath.Join(src, "part-01.md"), []byte("# Synth"), 0o644)
			if _, err := store.Store(src, store.StoreOptions{Slug: "synth"}); err != nil {
				t.Error(err)
			}
			return "Stored."
		}}
	})
	code, out := postNew(t, ts, map[string]any{"topic": "synth", "level": "beginner"})
	if code != http.StatusOK {
		t.Fatalf("POST /-/new = %d %v", code, out)
	}
	reply := func(text string) int {
		resp, err := http.Post(ts.URL+"/-/acp/"+out["runId"]+"/reply", "application/json", strings.NewReader(`{"text":"`+text+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	var sawInput bool
	evs := eventStream(t, ts, out["runId"], func(ev runEvent) {
		if ev.Type == "input" && !sawInput {
			sawInput = true
			if code := reply("Yes, 0.13.0."); code != http.StatusNoContent {
				t.Errorf("reply = %d, want 204", code)
			}
		}
	})
	if !sawInput {
		t.Fatal("no input event after the agent's question")
	}
	var sawReply bool
	for _, ev := range evs {
		if ev.Type == "reply" && ev.Text == "Yes, 0.13.0." {
			sawReply = true
		}
	}
	end := last(evs)
	if !sawReply || end.Type != "done" || end.StopReason != "end_turn" || end.Message != "" {
		t.Errorf("reply event %v, last event %+v; want the reply echoed and done with no note", sawReply, end)
	}
	if !strings.Contains(end.HTML, "Zig 0.13.0") || !strings.Contains(end.HTML, "Stored.") {
		t.Errorf("done html = %q, want both turns", end.HTML)
	}
	if tut, err := store.ReadMetadata(filepath.Join(tutorials, "synth")); err != nil || tut.Status != store.StatusUnverified {
		t.Errorf("after the reply: %+v, %v; want it stored", tut, err)
	}
	if code := reply("too late"); code != http.StatusConflict {
		t.Errorf("reply to a finished run = %d, want 409", code)
	}
}

func TestNewRefusesCrossOriginAndBadInput(t *testing.T) {
	f := &fakeHost{}
	_, ts, tutorials := newTutServer(t, f, func() *acptest.Agent { return &acptest.Agent{} })
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/-/new", strings.NewReader(`{"topic":"x"}`))
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin POST /-/new = %d, want 403", resp.StatusCode)
	}
	for _, body := range []map[string]any{
		{"topic": "  "},
		{"topic": "x", "voice": "no-such-voice"},
		{"topic": "x", "shelf": "no-such-shelf"},
		{"topic": "x", "slug": "Bad Slug"},
		{"topic": "x", "workspace": "relative/dir"},
		{"topic": "x", "workspace": tutorials},
		{"topic": "x", "tools": []map[string]string{{"name": "zig", "version": "0.13 beta"}}},
	} {
		if code, _ := postNew(t, ts, body); code != http.StatusBadRequest {
			t.Errorf("POST /-/new %v = %d, want 400", body, code)
		}
	}
	if entries, _ := os.ReadDir(tutorials); len(entries) != 0 {
		t.Errorf("refused requests left %d entries in the library", len(entries))
	}
	f.mu.Lock()
	starts := f.starts
	f.mu.Unlock()
	if starts != 0 {
		t.Errorf("refused requests started the agent %d times", starts)
	}
}

func TestNewFallsBackToWorkerThenHandoff(t *testing.T) {
	// No agent, no worker: the /lathe command with the pre-answered inputs.
	srv, ts, tutorials := newTutServer(t, nil, nil)
	body := map[string]any{"topic": "build a digital synth in Zig", "level": "some familiarity", "voice": "companion",
		"tools": []map[string]string{{"name": "zig", "version": "0.13.0"}}, "repo": "git@github.com:me/synth.git", "repoBranch": "main"}
	want := `/lathe build a digital synth in Zig --level "some familiarity" --voice companion --tool zig:0.13.0 --repo git@github.com:me/synth.git --repo-branch main`
	code, out := postNew(t, ts, body)
	if code != http.StatusOK || out["mode"] != "handoff" || out["command"] != want {
		t.Fatalf("POST /-/new = %d %v, want handoff %q", code, out, want)
	}

	// A /lathe-work worker gets a queued job carrying the same command.
	srv.queue.MarkWorkerSeen()
	code, out = postNew(t, ts, body)
	if code != http.StatusOK || out["mode"] != "queued" || out["jobId"] == "" {
		t.Fatalf("POST /-/new with a worker = %d %v, want queued", code, out)
	}
	job, ok := srv.queue.Get(out["jobId"])
	if !ok || job.Type != queue.JobNew || job.Command != want || job.Slug != "" {
		t.Errorf("queued job = %+v, want type new with the command", job)
	}
	if entries, _ := os.ReadDir(tutorials); len(entries) != 0 {
		t.Errorf("the worker/handoff paths reserved %d tutorials; the worker's session stores its own", len(entries))
	}
}

func TestNewAgentStartFailureHandsOffWithoutAReservation(t *testing.T) {
	f := &fakeHost{fail: io.ErrUnexpectedEOF}
	_, ts, tutorials := newTutServer(t, f, func() *acptest.Agent { return &acptest.Agent{} })
	code, out := postNew(t, ts, map[string]any{"topic": "synth", "level": "beginner"})
	if code != http.StatusOK || out["mode"] != "handoff" || !strings.HasPrefix(out["command"], "/lathe synth --level beginner") {
		t.Fatalf("POST /-/new = %d %v, want the handoff", code, out)
	}
	if !strings.Contains(out["notice"], "could not start") {
		t.Errorf("notice = %q", out["notice"])
	}
	if entries, _ := os.ReadDir(tutorials); len(entries) != 0 {
		t.Errorf("a failed start left %d entries in the library", len(entries))
	}
}
