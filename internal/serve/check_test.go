package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/devenjarvis/lathe/internal/acp/acptest"
	"github.com/devenjarvis/lathe/internal/queue"
	"github.com/devenjarvis/lathe/internal/store"
)

// checkFixture writes a two-part tutorial whose part-02 has a Checkpoint, a
// learner workspace with one file, and records the workspace. checkpoint is
// the Checkpoint section's body (the fake agent runs any [read:…], [write:…]
// and [run:…] markers in it, since Lathe puts the section in the prompt).
func checkFixture(t *testing.T, checkpoint string) (dir, tutDir, ws string) {
	t.Helper()
	dir = t.TempDir()
	tutDir = makeTutFixture(t, dir, "series", true)
	part := "# Part two\n\nType this in.\n\n## Checkpoint\n\n" + checkpoint + "\n\n## Exercises\n\n- [ ] one\n"
	if err := os.WriteFile(filepath.Join(tutDir, "part-02.md"), []byte(part), 0o644); err != nil {
		t.Fatal(err)
	}
	ws = t.TempDir()
	if real, err := filepath.EvalSymlinks(ws); err == nil {
		ws = real
	}
	if err := os.WriteFile(filepath.Join(ws, "main.go"), []byte("package main // learner\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetWorkspace(tutDir, ws); err != nil {
		t.Fatal(err)
	}
	return dir, tutDir, ws
}

func newCheckServer(t *testing.T, dir string, f *fakeHost, mk func() *acptest.Agent) (*Server, *httptest.Server) {
	t.Helper()
	srv := NewServer(dir)
	if f != nil {
		srv.setAgentHost(newAgentHost("fake-acp", f.starter(mk)))
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); srv.Close() })
	return srv, ts
}

var copyRe = regexp.MustCompile(`Lathe copied it to (\S+) \(`)

func TestCheckRunsOnACopyOfTheWorkspace(t *testing.T) {
	dir, tutDir, ws := checkFixture(t, "Run it. [write:WS/main.go] [write:main.go] [run:cat main.go]")
	// Point the write marker at the learner's real file.
	src, _ := os.ReadFile(filepath.Join(tutDir, "part-02.md"))
	if err := os.WriteFile(filepath.Join(tutDir, "part-02.md"), []byte(strings.ReplaceAll(string(src), "WS/", ws+"/")), 0o644); err != nil {
		t.Fatal(err)
	}

	f := &fakeHost{}
	var scratch string
	_, ts := newCheckServer(t, dir, f, func() *acptest.Agent {
		return &acptest.Agent{
			Plan: []string{"Read the part", "Run the checkpoint", "Record"},
			Reply: func(prompt string) string {
				if m := copyRe.FindStringSubmatch(prompt); m != nil {
					scratch = m[1]
				}
				// What the skill does through lathe_check_result.
				if err := store.RecordCheck(tutDir, &store.CheckResult{
					Part: "part-02.md", Status: store.CheckFail,
					Summary:  "The checkpoint prints nothing yet.",
					Findings: []store.CheckFinding{{File: "main.go", Nudge: "What does `main` call?", Pointer: "Step 2.", Where: "main.go, line 1.", Reveal: "```go\nfunc main() { run() }\n```"}},
				}); err != nil {
					t.Error(err)
				}
				return "Done: your hints are under the checkpoint."
			},
		}
	})

	code, out := postJob(t, ts, "/-/check/series/part-02.md", "")
	if code != http.StatusOK || out["mode"] != "agent" || out["runId"] == "" {
		t.Fatalf("check = %d %v, want mode agent", code, out)
	}
	evs := eventStream(t, ts, out["runId"], nil)
	end := last(evs)
	if end.Type != "done" || end.Message != "" {
		t.Fatalf("last event = %+v, want done with no note", end)
	}

	a := f.agent(0)
	prompt := a.Prompts[0]
	for _, want := range []string{"Check My Work", `<skill name="lathe-check">`, "/lathe-check series part-02.md", "lathe_check_result", ws, "<checkpoint>", "No verified code snapshot"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
	if scratch == "" || a.Cwds[0] != scratch || scratch == ws {
		t.Fatalf("session cwd = %q, scratch = %q; want the copy, not the workspace %q", a.Cwds[0], scratch, ws)
	}

	// The write into the learner's workspace was refused, the one into the
	// copy allowed, and the command ran in the copy (and saw the copied file).
	var refusedWS, wroteCopy, ran bool
	for _, ev := range evs {
		if ev.Type != "activity" || ev.Activity == nil {
			continue
		}
		act := ev.Activity
		switch {
		case act.Kind == "write" && act.Denied && strings.HasPrefix(act.Path, ws):
			refusedWS = true
		case act.Kind == "write" && !act.Denied && strings.HasPrefix(act.Path, scratch):
			wroteCopy = true
		case act.Kind == "terminal_exit" && strings.Contains(act.Output, "written by acptest"):
			ran = true
		}
	}
	if !refusedWS || !wroteCopy || !ran {
		t.Errorf("refused workspace write=%v, wrote copy=%v, ran in copy=%v; want all true", refusedWS, wroteCopy, ran)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "main.go")); string(b) != "package main // learner\n" {
		t.Errorf("the learner's main.go changed: %q", b)
	}
	if entries, _ := os.ReadDir(ws); len(entries) != 1 {
		t.Errorf("the workspace gained files: %v", entries)
	}
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Errorf("the scratch copy %s was not removed", scratch)
	}

	// The result reads back rendered, with the ladder.
	resp, err := http.Get(ts.URL + "/-/check/series/part-02.md")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var got struct {
		CheckedAt string `json:"checkedAt"`
		Status    string `json:"status"`
		HTML      string `json:"html"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Not quite yet", "What does <code>main</code> call?", "Another hint", "Show me where", "Reveal the reference code", "Lower confidence"} {
		if !strings.Contains(got.HTML, want) {
			t.Errorf("result html is missing %q:\n%s", want, got.HTML)
		}
	}
	if got.Status != "fail" || got.CheckedAt == "" {
		t.Errorf("status/checkedAt = %q/%q", got.Status, got.CheckedAt)
	}
}

func TestCheckPromptCarriesTheReferenceDiff(t *testing.T) {
	dir, tutDir, _ := checkFixture(t, "Run `go run .`")
	repo := t.TempDir()
	git(t, repo, "init", "--quiet")
	for i, body := range []string{"one\n", "one\ntwo\n"} {
		if err := os.WriteFile(filepath.Join(repo, "main.txt"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		git(t, repo, "add", "-A")
		git(t, repo, "-c", "user.name=l", "-c", "user.email=l@l", "commit", "--quiet", "-m", "p")
		git(t, repo, "tag", []string{"part-01", "part-02"}[i])
	}
	if err := store.SaveCode(tutDir, repo); err != nil {
		t.Fatal(err)
	}
	f := &fakeHost{}
	_, ts := newCheckServer(t, dir, f, func() *acptest.Agent { return &acptest.Agent{} })
	code, out := postJob(t, ts, "/-/check/series/part-02.md", "")
	if code != http.StatusOK || out["mode"] != "agent" {
		t.Fatalf("check = %d %v", code, out)
	}
	end := last(eventStream(t, ts, out["runId"], nil))
	if !strings.Contains(end.Message, "without recording a result") {
		t.Errorf("done message = %q, want the no-result note", end.Message)
	}
	prompt := f.agent(0).Prompts[0]
	for _, want := range []string{"<reference-diff>", "+two", "part-01, part-02", `confidence "reference"`} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
}

func TestCheckFallsBackToWorkerThenHandoff(t *testing.T) {
	dir, _, ws := checkFixture(t, "Run it.")
	srv, ts := newCheckServer(t, dir, nil, nil)

	code, out := postJob(t, ts, "/-/check/series/part-02.md", "")
	if code != http.StatusOK || out["mode"] != "handoff" || out["command"] != "/lathe-check series part-02.md" {
		t.Fatalf("no worker: %d %v, want the handoff command", code, out)
	}

	srv.queue.MarkWorkerSeen()
	code, out = postJob(t, ts, "/-/check/series/part-02.md", "")
	if code != http.StatusOK || out["mode"] != "queued" {
		t.Fatalf("with worker: %d %v, want queued", code, out)
	}
	job, ok := srv.queue.Get(out["jobId"])
	if !ok || job.Type != queue.JobCheck || job.Part != "part-02.md" || job.Path != ws || job.Slug != "series" {
		t.Errorf("queued job = %+v", job)
	}
}

func TestCheckNeedsAWorkspaceAndAKnownPart(t *testing.T) {
	dir := t.TempDir()
	makeTutFixture(t, dir, "series", true)
	_, ts := newCheckServer(t, dir, nil, nil)
	resp, err := http.Post(ts.URL+"/-/check/series/part-01.md", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("no workspace: status %d, want 400", resp.StatusCode)
	}
	for _, path := range []string{"/-/check/series/part-09.md", "/-/check/series/metadata.json", "/-/check/nope/part-01.md"} {
		resp, err := http.Post(ts.URL+path, "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, resp.StatusCode)
		}
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/-/check/series/part-01.md", nil)
	req.Header.Set("Origin", "http://evil.example")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site: status %d, want 403", resp.StatusCode)
	}
}

func TestCheckSectionRendersUnderTheCheckpoint(t *testing.T) {
	dir, tutDir, _ := checkFixture(t, "Run `go run .` and see `ok`.")
	if err := store.RecordCheck(tutDir, &store.CheckResult{
		Part: "part-02.md", Status: store.CheckPass,
		Divergences: []store.CheckDivergence{{File: "main.go", Note: "You used a map.", LaterPart: "part-02.md"}},
	}); err != nil {
		t.Fatal(err)
	}
	_, ts := newCheckServer(t, dir, nil, nil)

	page := get(t, ts, "/series/part-02.md")
	cp := strings.Index(page, `id="checkpoint"`)
	box := strings.Index(page, `id="checkWork"`)
	ex := strings.Index(page, `id="exercises"`)
	if cp < 0 || box < cp || ex < box {
		t.Fatalf("checkpoint/check/exercises at %d/%d/%d; want the check block between them", cp, box, ex)
	}
	for _, want := range []string{"Your checkpoint passes", "These are choices, not mistakes", "You used a map.", `action="/-/check/series/part-02.md"`, "Check again"} {
		if !strings.Contains(page, want) {
			t.Errorf("page is missing %q", want)
		}
	}
	if strings.Contains(page, "check-set-ws") {
		t.Error("the set-workspace button shows although a workspace is set")
	}

	// A part with no Checkpoint section gets no block.
	if p1 := get(t, ts, "/series/part-01.md"); strings.Contains(p1, `id="checkWork"`) {
		t.Error("part-01 has no checkpoint but got a check block")
	}
}

func get(t *testing.T, ts *httptest.Server, path string) string {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var b strings.Builder
	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d", path, resp.StatusCode)
	}
	return b.String()
}
