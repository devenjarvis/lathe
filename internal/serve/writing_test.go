package serve

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devenjarvis/lathe/internal/acp/acptest"
	"github.com/devenjarvis/lathe/internal/store"
)

func getBody(t *testing.T, u string) (int, string) {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func getProgress(t *testing.T, base, slug string) writingView {
	t.Helper()
	code, body := getBody(t, base+"/-/new/"+slug+"/progress")
	if code != http.StatusOK {
		t.Fatalf("progress = %d %s", code, body)
	}
	var v writingView
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("progress JSON: %v\n%s", err, body)
	}
	return v
}

// TestWritingProgressFollowsTheRun drives a new tutorial through a question:
// the agent reports steps and a source, writes two sections of the draft and
// asks to confirm versions with quick replies. While it waits, the progress
// endpoint, the progress page, Home's cover and /-/worker all show it; a form
// reply (scripts off) sends the answer; the second turn stores Part 1, and
// the desktop shell is told "Part 1 is ready".
func TestWritingProgressFollowsTheRun(t *testing.T) {
	f := &fakeHost{}
	var (
		mu        sync.Mutex
		turns     int
		agentSlug string
	)
	tutorials := ""
	srv, ts, tuts := newTutServer(t, f, func() *acptest.Agent {
		return &acptest.Agent{Reply: func(prompt string) string {
			mu.Lock()
			turns++
			n := turns
			mu.Unlock()
			if n == 1 {
				s, src := writePart1(t, prompt)
				mu.Lock()
				agentSlug = s
				mu.Unlock()
				draft := "# A Game Boy emulator in Rust\n\n## What you'll build\n\nA CPU.\n\n```rust\n## not a heading\n```\n\n## Decode the opcodes\n\nSoon.\n"
				if err := os.WriteFile(filepath.Join(src, "part-01.md"), []byte(draft), 0o644); err != nil {
					t.Error(err)
				}
				dir := filepath.Join(tutorials, s)
				// What lathe_progress records (the MCP tool calls store.RecordWriting).
				for _, u := range []store.WritingUpdate{
					{Step: store.WritingResearch, Sources: []store.Source{{Title: "Pan Docs", URL: "https://gbdev.io/pandocs/"}}},
					{Step: store.WritingVersions, Ask: "confirm versions", Replies: []string{"Sounds right", "No repo", "Other versions…"}},
				} {
					if _, err := store.RecordWriting(dir, u); err != nil {
						t.Error(err)
					}
				}
				return "I'll write against **Rust 1.82**. Sound right?"
			}
			mu.Lock()
			s := agentSlug
			mu.Unlock()
			src := filepath.Join(t.TempDir(), s)
			_ = os.MkdirAll(src, 0o755)
			_ = os.WriteFile(filepath.Join(src, "part-01.md"), []byte("# GB"), 0o644)
			if _, err := store.Store(src, store.StoreOptions{Slug: s, Title: "A Game Boy emulator in Rust"}); err != nil {
				t.Error(err)
			}
			return "Stored."
		}}
	})
	tutorials = tuts
	native := newFakeNative()
	srv.SetNative(native)

	code, out := postNew(t, ts, map[string]any{"topic": "a Game Boy emulator in Rust", "level": "beginner"})
	if code != http.StatusOK || out["mode"] != "agent" {
		t.Fatalf("POST /-/new = %d %v", code, out)
	}
	slug := out["slug"]

	// Wait for the question.
	var v writingView
	deadline := time.Now().Add(5 * time.Second)
	for {
		v = getProgress(t, ts.URL, slug)
		if v.Needs != nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if v.Needs == nil {
		t.Fatalf("no Needs you in %+v", v)
	}
	if v.State != "writing" || !v.Live || v.Run == nil || v.Run.ID != out["runId"] {
		t.Errorf("progress = %+v, want a live run %s", v, out["runId"])
	}
	// The draft sets the floor (Write Part 1) over the reported step.
	if v.Active != 3 || v.Step != "Write Part 1" || v.Steps[0].State != "done" || v.Steps[2].State != "active" || v.Steps[3].State != "todo" {
		t.Errorf("steps = %d %q %+v", v.Active, v.Step, v.Steps)
	}
	if len(v.Sources) != 1 || v.Sources[0].Title != "Pan Docs" {
		t.Errorf("sources = %+v", v.Sources)
	}
	if v.DraftTitle != "A Game Boy emulator in Rust" || strings.Join(v.Sections, "|") != "What you'll build|Decode the opcodes" || !strings.Contains(string(v.DraftHTML), "Decode the opcodes") {
		t.Errorf("draft = %q %q %.200q", v.DraftTitle, v.Sections, v.DraftHTML)
	}
	if v.Needs.Label != "confirm versions" || strings.Join(v.Needs.Replies, "|") != "Sounds right|No repo|Other versions…" ||
		!strings.Contains(v.Needs.Question, "Rust 1.82") || !strings.Contains(string(v.Needs.QuestionHTML), "<strong>Rust 1.82</strong>") {
		t.Errorf("needs = %+v", v.Needs)
	}

	// The progress page renders all of it, with forms for scripts off.
	code, page := getBody(t, ts.URL+"/"+slug+"/")
	for _, want := range []string{
		"Writing Part 1", `aria-current="step"`, "Put it on your shelf", "You can close this window",
		`action="/-/new/` + slug + `/cancel"`, "Stop writing",
		`id="needs"`, "confirm versions", `name="text" value="Sounds right"`, `action="/-/new/` + slug + `/reply"`,
		`href="https://gbdev.io/pandocs/"`, "<li>Decode the opcodes</li>",
		`<noscript><meta http-equiv="refresh" content="5"></noscript>`,
	} {
		if code != http.StatusOK || !strings.Contains(page, want) {
			t.Errorf("progress page (%d) lacks %q", code, want)
		}
	}
	// Home: the dark cover, with the yellow Needs you link.
	_, home := getBody(t, ts.URL+"/")
	for _, want := range []string{`class="tutorial writing-cover`, `data-writing`, `<a class="writing-needs" href="/` + slug + `/#needs">Needs you: confirm versions`, `<span class="writing-cover-step-n">3/4</span>`} {
		if !strings.Contains(home, want) {
			t.Errorf("Home lacks %q", want)
		}
	}
	// The panel learns about it from /-/worker on any page.
	_, worker := getBody(t, ts.URL+"/-/worker")
	if !strings.Contains(worker, `"writing":[{"id":"`+out["runId"]+`","slug":"`+slug+`"`) {
		t.Errorf("/-/worker = %s, want the running new tutorial", worker)
	}

	// A quick reply from the form (scripts off): 303 back to the page.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.PostForm(ts.URL+"/-/new/"+slug+"/reply", url.Values{"text": {"Sounds right"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/"+slug+"/" {
		t.Errorf("form reply = %d → %q, want 303 to the page", resp.StatusCode, resp.Header.Get("Location"))
	}

	evs := eventStream(t, ts, out["runId"], nil)
	var input *runEvent
	for i := range evs {
		if evs[i].Type == "input" {
			input = &evs[i]
		}
	}
	if input == nil || input.Title != "confirm versions" || len(input.Replies) != 3 || !strings.Contains(input.Text, "Rust 1.82") {
		t.Errorf("input event = %+v", input)
	}
	if end := last(evs); end.Type != "done" {
		t.Errorf("last event = %+v", end)
	}
	if v := getProgress(t, ts.URL, slug); v.State != "stored" || v.Href != "/"+slug+"/" {
		t.Errorf("after the store: %+v", v)
	}
	if _, err := os.Stat(filepath.Join(tutorials, slug, store.WritingFile)); !os.IsNotExist(err) {
		t.Errorf("writing.json survived the store (err %v)", err)
	}
	if n := native.nextNotice(t); n[0] != "Part 1 is ready: A Game Boy emulator in Rust" {
		t.Errorf("notification = %q", n)
	}
	// A reply now has nothing to answer.
	resp, _ = client.PostForm(ts.URL+"/-/new/"+slug+"/reply", url.Values{"text": {"again"}})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("reply after the run = %d, want 404", resp.StatusCode)
	}
}

func TestWritingStopFormAndGoneProgress(t *testing.T) {
	f := &fakeHost{}
	started := make(chan string, 1)
	var once sync.Once
	srv, ts, _ := newTutServer(t, f, func() *acptest.Agent {
		return &acptest.Agent{ChunkDelay: 20 * time.Millisecond, Reply: func(prompt string) string {
			s, _ := writePart1(t, prompt)
			once.Do(func() { started <- s })
			return strings.Repeat("researching ", 500)
		}}
	})
	native := newFakeNative()
	srv.SetNative(native)
	_, out := postNew(t, ts, map[string]any{"topic": "parsers in Go", "level": "beginner", "tools": []map[string]string{{"name": "go", "version": "1.23"}}})
	slug := <-started
	if v := getProgress(t, ts.URL, slug); v.Active != 3 || v.Needs != nil {
		t.Errorf("progress = %+v, want the draft's step and no question", v)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.PostForm(ts.URL+"/-/new/"+slug+"/cancel", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Errorf("Stop writing form = %d → %q, want 303 to the library", resp.StatusCode, resp.Header.Get("Location"))
	}
	_ = eventStream(t, ts, out["runId"], nil)
	if v := getProgress(t, ts.URL, slug); v.State != "gone" {
		t.Errorf("after stop: %+v, want gone", v)
	}
	// The reader stopped it: no notification.
	native.noNotice(t)
	if code, _ := getBody(t, ts.URL+"/-/new/..%2fx/progress"); code != http.StatusNotFound {
		t.Errorf("bad slug progress = %d, want 404", code)
	}
}

func TestWritingStepsFromPinnedVersionsAndLeftovers(t *testing.T) {
	srv, ts, tutorials := newTutServer(t, nil, nil)
	_ = srv
	tut, dir, err := store.ReserveNew(tutorials, store.NewReservation{Topic: "a synth in Zig", Tools: []store.Tool{{Name: "zig", Version: "0.13.0"}}})
	if err != nil {
		t.Fatal(err)
	}
	// Pinned versions: Pin versions is done before any report. No hosted
	// run is writing it (a leftover from a restart): not live.
	v := getProgress(t, ts.URL, tut.Slug)
	if v.Active != 2 || v.Live || v.Needs != nil {
		t.Errorf("pinned leftover = %+v", v)
	}
	if _, err := store.RecordWriting(dir, store.WritingUpdate{Step: store.WritingShelve}); err != nil {
		t.Fatal(err)
	}
	if v := getProgress(t, ts.URL, tut.Slug); v.Active != 4 || v.Step != "Put it on your shelf" {
		t.Errorf("after shelve: %+v", v)
	}
	_, page := getBody(t, ts.URL+"/"+tut.Slug+"/")
	if !strings.Contains(page, "isn’t writing this any more") || !strings.Contains(page, `action="/-/delete/`+tut.Slug+`"`) || strings.Contains(page, `id="writingStop"`) {
		t.Errorf("leftover page should offer Move to Trash, not Stop writing")
	}
	_, home := getBody(t, ts.URL+"/")
	if !strings.Contains(home, `data-live="false"`) || !strings.Contains(home, `writing-cover-state">Stopped`) {
		t.Errorf("Home cover for a leftover should read Stopped")
	}
}

func TestDraftOutlineAndReadDraft(t *testing.T) {
	title, secs := draftOutline([]byte("# T\n\n## One ##\n~~~\n## no\n~~~\n```go\n## no\n```\n## Two\n### sub\n"))
	if title != "T" || strings.Join(secs, "|") != "One|Two" {
		t.Errorf("outline = %q %q", title, secs)
	}
	scratch := t.TempDir()
	if real, err := filepath.EvalSymlinks(scratch); err == nil {
		scratch = real
	}
	dir := filepath.Join(scratch, "x")
	_ = os.MkdirAll(dir, 0o755)
	secret := filepath.Join(t.TempDir(), "secret.md")
	_ = os.WriteFile(secret, []byte("# secret"), 0o644)
	if err := os.Symlink(secret, filepath.Join(dir, "part-01.md")); err != nil {
		t.Skip(err)
	}
	if b := readDraft(dir, scratch); b != nil {
		t.Errorf("readDraft followed a link out of the scratch dir: %q", b)
	}
	_ = os.Remove(filepath.Join(dir, "part-01.md"))
	_ = os.WriteFile(filepath.Join(dir, "part-01.md"), []byte("# ok"), 0o644)
	if b := readDraft(dir, scratch); string(b) != "# ok" {
		t.Errorf("readDraft = %q", b)
	}
}
