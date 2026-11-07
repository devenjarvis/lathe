package serve

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devenjarvis/lathe/internal/acp/acptest"
	"github.com/devenjarvis/lathe/internal/store"
)

// newJobServer hosts a fake agent whose prompts call script (standing in for
// the skill's MCP tool calls) and whose reply is the returned text.
func newJobServer(t *testing.T, f *fakeHost, mk func(tutDir string) *acptest.Agent) (*Server, *httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	tutDir := makeTutFixture(t, dir, "series", true)
	srv := NewServer(dir)
	srv.setAgentHost(newAgentHost("fake-acp", f.starter(func() *acptest.Agent { return mk(tutDir) })))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); srv.Close() })
	return srv, ts, tutDir
}

func postJob(t *testing.T, ts *httptest.Server, path, body string) (int, map[string]string) {
	t.Helper()
	resp, err := http.Post(ts.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

var scratchRe = regexp.MustCompile(`scratch dir for this run: (\S+) \(`)

// scratchOf pulls the scratch dir Lathe named in a verify prompt.
func scratchOf(t *testing.T, prompt string) string {
	t.Helper()
	m := scratchRe.FindStringSubmatch(prompt)
	if m == nil {
		t.Fatalf("no scratch dir in prompt:\n%.400s", prompt)
	}
	return m[1]
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestAgentVerifyRunsOverACP(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	f := &fakeHost{}
	var scratch string
	_, ts, tutDir := newJobServer(t, f, func(tutDir string) *acptest.Agent {
		return &acptest.Agent{
			Plan: []string{"Follow part-01.md", "Follow part-02.md"},
			Reply: func(prompt string) string {
				// What the skill does through lathe_verify_result: mark it in
				// flight, build in the scratch dir, record the result with
				// the code snapshot.
				scratch = scratchOf(t, prompt)
				if err := store.RecordVerify(tutDir, store.VerifyRecord{Status: store.StatusVerifying}); err != nil {
					t.Error(err)
				}
				if err := os.WriteFile(filepath.Join(scratch, "main.go"), []byte("package main\n"), 0o644); err != nil {
					t.Error(err)
				}
				git(t, scratch, "init", "--quiet")
				git(t, scratch, "add", "-A")
				git(t, scratch, "-c", "user.name=lathe", "-c", "user.email=lathe@localhost", "commit", "--quiet", "-m", "part-01")
				git(t, scratch, "tag", "part-01")
				if err := store.RecordVerify(tutDir, store.VerifyRecord{Status: store.StatusVerified, Code: scratch}); err != nil {
					t.Error(err)
				}
				if err := store.SaveCode(tutDir, scratch); err != nil {
					t.Error(err)
				}
				return "Verified: every checkpoint passed."
			},
		}
	})

	code, out := postJob(t, ts, "/-/verify/series", "")
	if code != http.StatusOK || out["mode"] != "agent" || out["runId"] == "" {
		t.Fatalf("verify = %d %v, want mode agent", code, out)
	}
	evs := eventStream(t, ts, out["runId"], nil)

	end := last(evs)
	if end.Type != "done" || end.StopReason != "end_turn" || end.Message != "" {
		t.Fatalf("last event = %+v, want done/end_turn with no cleanup note", end)
	}
	if !strings.Contains(end.HTML, "every checkpoint passed") {
		t.Errorf("done html = %q, want the agent's report", end.HTML)
	}
	var plans, tools, updates int
	for _, ev := range evs {
		switch ev.Type {
		case "plan":
			plans++
		case "tool":
			tools++
			if ev.ID == "" || ev.Status != "in_progress" {
				t.Errorf("tool event = %+v, want an id and status", ev)
			}
		case "tool_update":
			updates++
		}
	}
	if plans != 3 || tools != 2 || updates != 2 {
		t.Errorf("plan/tool/tool_update events = %d/%d/%d, want 3/2/2", plans, tools, updates)
	}

	a := f.agent(0)
	prompt := a.Prompts[0]
	for _, want := range []string{"Verify a Tutorial", "<skill name=\"lathe-verify\">", "/lathe-verify series", "lathe_verify_result", tutDir} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
	if a.Cwds[0] != scratch {
		t.Errorf("session cwd = %q, want the scratch dir %q", a.Cwds[0], scratch)
	}

	tut, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatal(err)
	}
	if tut.Status != store.StatusVerified {
		t.Errorf("status = %q, want verified", tut.Status)
	}
	if tags := store.CodeTags(filepath.Join(tutDir, store.CodeDirName)); len(tags) != 1 || tags[0] != "part-01" {
		t.Errorf("snapshot tags = %v, want [part-01]", tags)
	}
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Errorf("scratch dir %s still exists after the run (err %v)", scratch, err)
	}

	// The panel's finished card reads the result and the saved code from
	// /-/status ("Verified · All 2 parts passed … · Open code").
	resp, err := http.Get(ts.URL + "/-/status/series/part-01.md")
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Status string              `json:"status"`
		Result *store.VerifyResult `json:"result"`
		Code   map[string]string   `json:"code"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&st)
	_ = resp.Body.Close()
	if st.Status != "verified" || st.Result == nil || st.Result.Status != store.StatusVerified {
		t.Errorf("status = %q, result = %+v, want verified", st.Status, st.Result)
	}
	if st.Code["tag"] != "part-01" || st.Code["path"] == "" {
		t.Errorf("status code = %v, want the part-01 snapshot", st.Code)
	}
}

func TestAgentVerifyCancelRestoresStatus(t *testing.T) {
	f := &fakeHost{}
	started := make(chan struct{})
	var once sync.Once
	_, ts, tutDir := newJobServer(t, f, func(tutDir string) *acptest.Agent {
		return &acptest.Agent{
			ChunkDelay: 20 * time.Millisecond,
			Reply: func(string) string {
				if err := store.RecordVerify(tutDir, store.VerifyRecord{Status: store.StatusVerifying}); err != nil {
					t.Error(err)
				}
				once.Do(func() { close(started) })
				return strings.Repeat("checking ", 500)
			},
		}
	})
	// A previous run's result is what cancelling restores.
	if err := store.WriteVerifyResult(tutDir, &store.VerifyResult{Status: store.StatusFailed, Part: "part-02.md"}); err != nil {
		t.Fatal(err)
	}

	code, out := postJob(t, ts, "/-/verify/series", "")
	if code != http.StatusOK || out["mode"] != "agent" {
		t.Fatalf("verify = %d %v", code, out)
	}
	<-started

	// While it runs: a second verify or an extend is refused, and the status
	// endpoint names the run so a reloaded page can reattach.
	if code, _ := postJob(t, ts, "/-/extend/series", `{"guidance":""}`); code != http.StatusConflict {
		t.Errorf("extend during a hosted verify = %d, want 409", code)
	}
	resp, err := http.Get(ts.URL + "/-/status/series/part-02.md")
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Status string      `json:"status"`
		Run    *jobRunInfo `json:"run"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&st)
	_ = resp.Body.Close()
	if st.Run == nil || st.Run.ID != out["runId"] || st.Run.Job != jobVerify {
		t.Errorf("status run = %+v, want {%s verify}", st.Run, out["runId"])
	}
	// When it started, so the reattached card's clock is right.
	if st.Run != nil && (st.Run.Started <= 0 || time.Since(time.UnixMilli(st.Run.Started)) > time.Minute) {
		t.Errorf("status run started = %d, want about now", st.Run.Started)
	}
	page, err := http.Get(ts.URL + "/series/part-02.md")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(page.Body)
	_ = page.Body.Close()
	if !strings.Contains(string(body), `data-run-id="`+out["runId"]+`" data-run-job="verify" data-run-started="`) {
		t.Errorf("reading page does not name the running job and when it started")
	}

	// `lathe verify-cancel` path: stop it on the server.
	resp, err = http.Post(ts.URL+"/-/verify/series/cancel", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel = %d, want 204", resp.StatusCode)
	}
	// The run has wound down before the cancel returned.
	tut, _ := store.ReadMetadata(tutDir)
	if tut.Status != store.StatusFailed {
		t.Errorf("status after cancel = %q, want the prior result (failed)", tut.Status)
	}
	evs := eventStream(t, ts, out["runId"], nil)
	end := last(evs)
	if end.Type != "done" || end.StopReason != "cancelled" || !strings.Contains(end.Message, "restored the previous status (failed)") {
		t.Errorf("last event = %+v, want done/cancelled with a restore note", end)
	}

	// No hosted run any more: cancel is a 404, and a new verify can start.
	resp, err = http.Post(ts.URL+"/-/verify/series/cancel", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("second cancel = %d, want 404", resp.StatusCode)
	}
}

func TestAgentExtendRunsOverACP(t *testing.T) {
	f := &fakeHost{}
	_, ts, tutDir := newJobServer(t, f, func(tutDir string) *acptest.Agent {
		return &acptest.Agent{Reply: func(string) string {
			name, err := store.ExtendStart(tutDir)
			if err != nil {
				t.Error(err)
				return "no"
			}
			if err := os.WriteFile(filepath.Join(tutDir, name), []byte("# Part 3\n"), 0o644); err != nil {
				t.Error(err)
			}
			if err := store.ExtendCommit(tutDir, name, nil, "Test Model"); err != nil {
				t.Error(err)
			}
			return "Part 3 adds filters."
		}}
	})

	// Guidance rides along in the invocation; its markers make the fake agent
	// exercise the extend policy: writing the tutorial dir is allowed, and a
	// command runs in the scratch dir, not the tutorial.
	code, out := postJob(t, ts, "/-/extend/series", `{"guidance":"cover filters [write:notes.txt] [run:pwd]"}`)
	if code != http.StatusOK || out["mode"] != "agent" {
		t.Fatalf("extend = %d %v", code, out)
	}
	evs := eventStream(t, ts, out["runId"], nil)
	if end := last(evs); end.Type != "done" || end.StopReason != "end_turn" {
		t.Fatalf("last event = %+v", end)
	}
	text := streamedText(evs)
	if !strings.Contains(text, "(write ok)") || !strings.Contains(text, "(run exit 0: ") || !strings.Contains(text, "lathe-extend-") {
		t.Errorf("policy outcomes = %q, want write ok and pwd in the scratch dir", text)
	}

	a := f.agent(0)
	prompt := a.Prompts[0]
	for _, want := range []string{"<skill name=\"lathe-extend\">", "<skill name=\"lathe\">", "/lathe-extend series cover filters", "lathe_extend_start"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
	real, _ := filepath.EvalSymlinks(tutDir)
	if a.Cwds[0] != real {
		t.Errorf("session cwd = %q, want the tutorial dir %q", a.Cwds[0], real)
	}
	tut, _ := store.ReadMetadata(tutDir)
	if len(tut.Parts) != 3 || tut.Status != store.StatusUnverified || tut.PendingPart != "" {
		t.Errorf("after extend: parts %v status %q pending %q", tut.Parts, tut.Status, tut.PendingPart)
	}
}

func TestAgentExtendCancelDiscardsUnfinishedPart(t *testing.T) {
	f := &fakeHost{}
	started := make(chan string, 1)
	_, ts, tutDir := newJobServer(t, f, func(tutDir string) *acptest.Agent {
		return &acptest.Agent{
			ChunkDelay: 20 * time.Millisecond,
			Reply: func(string) string {
				name, err := store.ExtendStart(tutDir)
				if err != nil {
					t.Error(err)
				}
				_ = os.WriteFile(filepath.Join(tutDir, name), []byte("# half a part"), 0o644)
				started <- name
				return strings.Repeat("writing ", 500)
			},
		}
	})
	_, out := postJob(t, ts, "/-/extend/series", "")
	name := <-started

	// The reader's Stop button.
	resp, err := http.Post(ts.URL+"/-/acp/"+out["runId"]+"/cancel", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	end := last(eventStream(t, ts, out["runId"], nil))
	if end.Type != "done" || end.StopReason != "cancelled" || !strings.Contains(end.Message, "discarded the unfinished part") {
		t.Errorf("last event = %+v", end)
	}
	tut, _ := store.ReadMetadata(tutDir)
	if tut.Status != store.StatusUnverified || tut.PendingPart != "" || len(tut.Parts) != 2 {
		t.Errorf("after cancel: status %q pending %q parts %v", tut.Status, tut.PendingPart, tut.Parts)
	}
	if _, err := os.Stat(filepath.Join(tutDir, name)); !os.IsNotExist(err) {
		t.Errorf("unfinished %s was kept (err %v)", name, err)
	}
}

func TestAgentJobCrashRestoresAndHandsOff(t *testing.T) {
	f := &fakeHost{}
	_, ts, tutDir := newJobServer(t, f, func(tutDir string) *acptest.Agent {
		return &acptest.Agent{Reply: func(string) string {
			_ = store.RecordVerify(tutDir, store.VerifyRecord{Status: store.StatusVerifying})
			return "x"
		}}
	})
	// [crash] in the extend guidance kills the fake agent mid-run.
	_, out := postJob(t, ts, "/-/extend/series", `{"guidance":"[crash]"}`)
	end := last(eventStream(t, ts, out["runId"], nil))
	if end.Type != "error" || end.Command != "/lathe-extend series [crash]" {
		t.Fatalf("last event = %+v, want error with the handoff command", end)
	}
	if !strings.Contains(end.Message, "before it finished writing the part") {
		t.Errorf("error message = %q", end.Message)
	}
	tut, _ := store.ReadMetadata(tutDir)
	if tut.Status == store.StatusExtending {
		t.Errorf("status still extending after a crash")
	}
}

func TestAgentJobStartFailureHandsOffWithNotice(t *testing.T) {
	f := &fakeHost{fail: errors.New("no such agent")}
	_, ts, _ := newJobServer(t, f, func(string) *acptest.Agent { return &acptest.Agent{} })
	code, out := postJob(t, ts, "/-/verify/series", "")
	if code != http.StatusOK || out["mode"] != "handoff" || out["command"] != "/lathe-verify series" {
		t.Fatalf("verify = %d %v, want the handoff", code, out)
	}
	if !strings.Contains(out["notice"], "could not start") {
		t.Errorf("notice = %q", out["notice"])
	}
}
