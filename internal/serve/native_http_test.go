package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devenjarvis/lathe/internal/acp/acptest"
	"github.com/devenjarvis/lathe/internal/queue"
	"github.com/devenjarvis/lathe/internal/store"
)

// fakeNative records what the server asks of the desktop shell.
type fakeNative struct {
	mu       sync.Mutex
	pick     string // what PickFolder returns
	pickErr  error
	openErr  error
	picks    [][2]string // title, start
	windows  [][2]string // path, title
	urls     []string
	notified chan [2]string
}

func newFakeNative() *fakeNative { return &fakeNative{notified: make(chan [2]string, 8)} }

func (f *fakeNative) PickFolder(title, start string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.picks = append(f.picks, [2]string{title, start})
	return f.pick, f.pickErr
}

func (f *fakeNative) Notify(title, body string) { f.notified <- [2]string{title, body} }

func (f *fakeNative) OpenURL(u string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.urls = append(f.urls, u)
	return f.openErr
}

func (f *fakeNative) OpenWindow(path, title string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.windows = append(f.windows, [2]string{path, title})
	return f.openErr
}

// nextNotice waits for the next notification.
func (f *fakeNative) nextNotice(t *testing.T) [2]string {
	t.Helper()
	select {
	case n := <-f.notified:
		return n
	case <-time.After(5 * time.Second):
		t.Fatal("no notification")
		return [2]string{}
	}
}

// noNotice asserts nothing was notified within a short window.
func (f *fakeNative) noNotice(t *testing.T) {
	t.Helper()
	select {
	case n := <-f.notified:
		t.Errorf("unexpected notification %q / %q", n[0], n[1])
	case <-time.After(150 * time.Millisecond):
	}
}

func nativePost(t *testing.T, srv *Server, path, body, origin string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func TestNativeEndpointsNeedAShell(t *testing.T) {
	dir := t.TempDir()
	makeTutFixture(t, dir, "series", true)
	srv := NewServer(dir)
	for path, body := range map[string]string{
		"/-/native/pick-folder": `{"slug":"series"}`,
		"/-/native/window":      `{"path":"/series/part-01.md?agent=popout"}`,
		"/-/native/open-url":    `{"url":"https://example.com/"}`,
	} {
		if w := nativePost(t, srv, path, body, ""); w.Code != http.StatusNotFound {
			t.Errorf("%s without a shell = %d, want 404", path, w.Code)
		}
	}
}

func TestNativePickFolder(t *testing.T) {
	dir := t.TempDir()
	tutDir := makeTutFixture(t, dir, "series", true)
	srv := NewServer(dir)
	n := newFakeNative()
	n.pick = "/home/reader/code/series"
	srv.SetNative(n)

	w := nativePost(t, srv, "/-/native/pick-folder", `{"slug":"series"}`, "wails://localhost")
	if w.Code != http.StatusOK {
		t.Fatalf("pick = %d %s", w.Code, w.Body)
	}
	var out map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out["path"] != n.pick {
		t.Errorf("path = %q, want %q", out["path"], n.pick)
	}
	home, _ := os.UserHomeDir()
	if len(n.picks) != 1 || n.picks[0][0] != "Choose a workspace for Test Tutorial" || n.picks[0][1] != home {
		t.Errorf("PickFolder calls = %v, want one starting at home %q", n.picks, home)
	}

	// With a workspace set, the dialog starts there; a cancel is "".
	ws := t.TempDir()
	if _, err := store.SetWorkspace(tutDir, ws); err != nil {
		t.Fatal(err)
	}
	n.pick = ""
	w = nativePost(t, srv, "/-/native/pick-folder", `{"slug":"series"}`, "http://wails.localhost")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"path":""`) {
		t.Errorf("cancelled pick = %d %s, want 200 with an empty path", w.Code, w.Body)
	}
	if got := n.picks[len(n.picks)-1][1]; got != ws {
		t.Errorf("second pick started at %q, want the workspace %q", got, ws)
	}

	n.pickErr = errors.New("no display")
	if w := nativePost(t, srv, "/-/native/pick-folder", `{"slug":"series"}`, ""); w.Code != http.StatusInternalServerError {
		t.Errorf("failing picker = %d, want 500", w.Code)
	}
	n.pickErr = nil

	calls := len(n.picks)
	for name, tc := range map[string]struct {
		body, origin string
		want         int
	}{
		"cross-site":   {`{"slug":"series"}`, "https://evil.example", http.StatusForbidden},
		"traversal":    {`{"slug":"../series"}`, "", http.StatusNotFound},
		"empty slug":   {`{"slug":""}`, "", http.StatusNotFound},
		"unknown slug": {`{"slug":"nope"}`, "", http.StatusNotFound},
		"bad json":     {`{`, "", http.StatusBadRequest},
		"oversize":     {`{"slug":"` + strings.Repeat("a", 5<<10) + `"}`, "", http.StatusRequestEntityTooLarge},
	} {
		if w := nativePost(t, srv, "/-/native/pick-folder", tc.body, tc.origin); w.Code != tc.want {
			t.Errorf("%s: %d, want %d", name, w.Code, tc.want)
		}
	}
	if len(n.picks) != calls {
		t.Errorf("a refused request opened the picker")
	}
}

func TestNativeWindowOpensOnlyKnownWindows(t *testing.T) {
	dir := t.TempDir()
	makeTutFixture(t, dir, "series", true)
	srv := NewServer(dir)
	n := newFakeNative()
	srv.SetNative(n)

	w := nativePost(t, srv, "/-/native/window", `{"path":"/series/part-02.md?agent=popout"}`, "wails://localhost")
	if w.Code != http.StatusNoContent {
		t.Fatalf("window = %d %s", w.Code, w.Body)
	}
	if len(n.windows) != 1 || n.windows[0] != [2]string{"/series/part-02.md?agent=popout", "Agent · Test Tutorial"} {
		t.Errorf("OpenWindow calls = %v", n.windows)
	}

	for _, p := range []string{
		"", "series/part-01.md?agent=popout", "//evil.example/series/part-01.md?agent=popout",
		"https://evil.example/series/part-01.md?agent=popout", "javascript:alert(1)",
		"/series/part-01.md?agent=popout&x=1", "/series/part-01.md?agent=popout#x",
		"/series/part-09.md?agent=popout", "/series/metadata.json?agent=popout", "/nope/part-01.md?agent=popout",
		"/../series/part-01.md?agent=popout", "/series/../series/part-01.md?agent=popout",
		"/series%2Fpart-01.md?agent=popout", "/\\evil.example/x?agent=popout", "/series/part-01.md/x?agent=popout",
		"/-/delete/series?agent=popout",
	} {
		body, _ := json.Marshal(map[string]string{"path": p})
		if w := nativePost(t, srv, "/-/native/window", string(body), ""); w.Code != http.StatusBadRequest {
			t.Errorf("path %q = %d, want 400", p, w.Code)
		}
	}
	if w := nativePost(t, srv, "/-/native/window", `{"path":"/series/part-01.md?agent=popout"}`, "http://evil.example"); w.Code != http.StatusForbidden {
		t.Errorf("cross-site window = %d, want 403", w.Code)
	}
	if len(n.windows) != 1 {
		t.Errorf("a refused request opened a window: %v", n.windows)
	}

	n.openErr = errors.New("boom")
	if w := nativePost(t, srv, "/-/native/window", `{"path":"/series/part-01.md?agent=popout"}`, ""); w.Code != http.StatusInternalServerError {
		t.Errorf("failing OpenWindow = %d, want 500", w.Code)
	}
}

func TestNativeOpenURLOnlyExternalLinks(t *testing.T) {
	dir := t.TempDir()
	srv := NewServer(dir)
	n := newFakeNative()
	srv.SetNative(n)

	for _, u := range []string{"https://go.dev/doc/", "http://example.com/a?b=c#d", "mailto:someone@example.com"} {
		body, _ := json.Marshal(map[string]string{"url": u})
		if w := nativePost(t, srv, "/-/native/open-url", string(body), "wails://localhost"); w.Code != http.StatusNoContent {
			t.Errorf("open %q = %d %s", u, w.Code, w.Body)
		}
	}
	if len(n.urls) != 3 || n.urls[0] != "https://go.dev/doc/" {
		t.Errorf("OpenURL calls = %v", n.urls)
	}

	for _, u := range []string{
		"", "/series/part-01.md", "//example.com/x", "file:///etc/passwd", "javascript:alert(1)",
		"vscode://open?x", "https:", "https:///nohost", "mailto:", "https://example.com/\nx", "ftp://example.com/",
	} {
		body, _ := json.Marshal(map[string]string{"url": u})
		if w := nativePost(t, srv, "/-/native/open-url", string(body), ""); w.Code != http.StatusBadRequest {
			t.Errorf("url %q = %d, want 400", u, w.Code)
		}
	}
	if w := nativePost(t, srv, "/-/native/open-url", `{"url":"https://example.com/"}`, "http://evil.example"); w.Code != http.StatusForbidden {
		t.Errorf("cross-site open-url = %d, want 403", w.Code)
	}
	if len(n.urls) != 3 {
		t.Errorf("a refused request opened a URL: %v", n.urls)
	}
}

func TestSameOriginAcceptsTheDesktopApp(t *testing.T) {
	for _, origin := range []string{"wails://localhost", "http://wails.localhost", "http://localhost:4242"} {
		r := httptest.NewRequest(http.MethodPost, "/-/workspace/x", nil)
		r.Host = "127.0.0.1:4242"
		r.Header.Set("Origin", origin)
		if !sameOrigin(r) {
			t.Errorf("origin %q refused", origin)
		}
	}
	for _, origin := range []string{"http://wails.localhost.evil.example", "http://evil.wails.localhost.example"} {
		r := httptest.NewRequest(http.MethodPost, "/-/workspace/x", nil)
		r.Host = "127.0.0.1:4242"
		r.Header.Set("Origin", origin)
		if sameOrigin(r) {
			t.Errorf("origin %q accepted", origin)
		}
	}
}

func TestNativeMarkupOnlyWithAShell(t *testing.T) {
	dir := t.TempDir()
	makeTutFixture(t, dir, "series", true)
	srv := NewServer(dir)
	srv.setAgentHost(newAgentHost("fake-acp", (&fakeHost{}).starter(func() *acptest.Agent { return &acptest.Agent{} })))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); srv.Close() })

	native := []string{` data-native>`, `id="workspacePick"`, `id="workspaceSetupPick"`, "/-/native/window", "/-/native/pick-folder", "/-/native/open-url"}
	plain := get(t, ts, "/series/part-01.md")
	list := get(t, ts, "/")
	for _, m := range native {
		if strings.Contains(plain, m) || strings.Contains(list, m) {
			t.Errorf("browser page carries native markup %q", m)
		}
	}
	if !strings.Contains(plain, "window.open(url, 'lathe-agent-' + slug") {
		t.Error("browser page lost the window.open pop-out")
	}

	srv.SetNative(newFakeNative())
	page := get(t, ts, "/series/part-01.md")
	for _, m := range native {
		if !strings.Contains(page, m) {
			t.Errorf("desktop page is missing %q", m)
		}
	}
	if strings.Contains(page, "window.open(") {
		t.Error("desktop page still pops out with window.open")
	}
	if !strings.Contains(page, `<html lang="en" data-native>`) {
		t.Error("desktop page has no data-native on <html>")
	}
	if l := get(t, ts, "/"); !strings.Contains(l, `<html lang="en" data-native>`) || !strings.Contains(l, "/-/native/open-url") {
		t.Error("desktop list page has no data-native on <html> or no external-link handler")
	}
}

// newNativeJobServer is newJobServer with a fake desktop shell installed.
func newNativeJobServer(t *testing.T, mk func(tutDir string) *acptest.Agent) (*fakeNative, *httptest.Server, string) {
	t.Helper()
	srv, ts, tutDir := newJobServer(t, &fakeHost{}, mk)
	n := newFakeNative()
	srv.SetNative(n)
	return n, ts, tutDir
}

func TestNotifyWhenAHostedVerifyFinishes(t *testing.T) {
	n, ts, _ := newNativeJobServer(t, func(tutDir string) *acptest.Agent {
		return &acptest.Agent{Reply: func(string) string {
			if err := store.RecordVerify(tutDir, store.VerifyRecord{Status: store.StatusVerifying}); err != nil {
				t.Error(err)
			}
			if err := store.RecordVerify(tutDir, store.VerifyRecord{Status: store.StatusFailed, Part: "part-02.md", FailedStep: 3, Error: "go build failed"}); err != nil {
				t.Error(err)
			}
			return "It failed."
		}}
	})
	_, out := postJob(t, ts, "/-/verify/series", "")
	eventStream(t, ts, out["runId"], nil)
	got := n.nextNotice(t)
	if got != [2]string{"Verify failed: Test Tutorial", "part-02.md, step 3: go build failed"} {
		t.Errorf("notice = %q", got)
	}
}

func TestNotifyVerifiedAndExtend(t *testing.T) {
	var step atomic.Int32
	n, ts, _ := newNativeJobServer(t, func(tutDir string) *acptest.Agent {
		return &acptest.Agent{Reply: func(string) string {
			if step.Add(1) == 1 {
				if err := store.RecordVerify(tutDir, store.VerifyRecord{Status: store.StatusVerified}); err != nil {
					t.Error(err)
				}
				return "ok"
			}
			name, err := store.ExtendStart(tutDir)
			if err != nil {
				t.Error(err)
				return "no"
			}
			if err := os.WriteFile(filepath.Join(tutDir, name), []byte("# Filters and sorting\n"), 0o644); err != nil {
				t.Error(err)
			}
			if err := store.ExtendCommit(tutDir, name, nil, "Test Model"); err != nil {
				t.Error(err)
			}
			return "added"
		}}
	})
	_, out := postJob(t, ts, "/-/verify/series", "")
	eventStream(t, ts, out["runId"], nil)
	if got := n.nextNotice(t); got != [2]string{"Verified: Test Tutorial", "Every part ran end to end."} {
		t.Errorf("verify notice = %q", got)
	}
	_, out = postJob(t, ts, "/-/extend/series", `{"guidance":""}`)
	eventStream(t, ts, out["runId"], nil)
	if got := n.nextNotice(t); got != [2]string{"Extend finished: Test Tutorial", "Part 3 is ready: Filters and sorting"} {
		t.Errorf("extend notice = %q", got)
	}
}

func TestNotifyStoppedExtendButNotACancelledOne(t *testing.T) {
	started := make(chan struct{}, 1)
	var slow atomic.Bool
	n, ts, _ := newNativeJobServer(t, func(tutDir string) *acptest.Agent {
		return &acptest.Agent{ChunkDelay: 20 * time.Millisecond, Reply: func(string) string {
			if _, err := store.ExtendStart(tutDir); err != nil {
				t.Error(err)
			}
			started <- struct{}{}
			if slow.Load() {
				return strings.Repeat("writing ", 500)
			}
			return "I gave up."
		}}
	})

	// Ends without committing: Lathe discards the part and says so.
	_, out := postJob(t, ts, "/-/extend/series", `{"guidance":""}`)
	<-started
	eventStream(t, ts, out["runId"], nil)
	got := n.nextNotice(t)
	if got[0] != "Extend stopped: Test Tutorial" || !strings.Contains(got[1], "discarded the unfinished part") {
		t.Errorf("stopped notice = %q", got)
	}

	// The reader stops it: no notification.
	slow.Store(true)
	_, out = postJob(t, ts, "/-/extend/series", `{"guidance":""}`)
	<-started
	resp, err := http.Post(ts.URL+"/-/acp/"+out["runId"]+"/cancel", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	eventStream(t, ts, out["runId"], nil)
	n.noNotice(t)
}

func TestNotifyWhenAHostedCheckFinishes(t *testing.T) {
	dir, tutDir, _ := checkFixture(t, "Run `go run .` and see `ok`.")
	srv, ts := newCheckServer(t, dir, &fakeHost{}, func() *acptest.Agent {
		return &acptest.Agent{Reply: func(string) string {
			if err := store.RecordCheck(tutDir, &store.CheckResult{Part: "part-02.md", Status: store.CheckPass, Summary: "Prints ok."}); err != nil {
				t.Error(err)
			}
			return "Passes."
		}}
	})
	n := newFakeNative()
	srv.SetNative(n)
	_, out := postJob(t, ts, "/-/check/series/part-02.md", "")
	eventStream(t, ts, out["runId"], nil)
	if got := n.nextNotice(t); got != [2]string{"Check finished: Part two", "Your checkpoint passes. Prints ok."} {
		t.Errorf("check notice = %q", got)
	}
}

func TestNotifyAskDoesNot(t *testing.T) {
	dir := t.TempDir()
	makeTutFixture(t, dir, "series", true)
	srv := NewServer(dir)
	srv.setAgentHost(newAgentHost("fake-acp", (&fakeHost{}).starter(func() *acptest.Agent {
		return &acptest.Agent{Reply: func(string) string { return "an answer" }}
	})))
	n := newFakeNative()
	srv.SetNative(n)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); srv.Close() })
	_, out := postJob(t, ts, "/-/ask/series/part-01.md", `{"question":"why?"}`)
	if out["runId"] == "" {
		t.Fatalf("ask = %v", out)
	}
	eventStream(t, ts, out["runId"], nil)
	n.noNotice(t)
}

func TestNotifyWhenAWorkerJobIsDone(t *testing.T) {
	dir := t.TempDir()
	tutDir := makeTutFixture(t, dir, "series", true)
	srv := NewServer(dir)
	n := newFakeNative()
	srv.SetNative(n)

	done := func(job queue.Job) {
		t.Helper()
		id := srv.queue.Enqueue(job)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, ok := srv.queue.Claim(ctx); !ok {
			t.Fatal("no job to claim")
		}
		for i := 0; i < 2; i++ { // a retried done notifies once
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/-/work/"+id+"/done", nil))
			if w.Code != http.StatusNoContent {
				t.Fatalf("done = %d", w.Code)
			}
		}
	}

	if err := store.RecordVerify(tutDir, store.VerifyRecord{Status: store.StatusVerified}); err != nil {
		t.Fatal(err)
	}
	done(queue.Job{Type: queue.JobVerify, Slug: "series"})
	if got := n.nextNotice(t); got[0] != "Verified: Test Tutorial" {
		t.Errorf("worker verify notice = %q", got)
	}
	n.noNotice(t)

	// A check job that recorded nothing says so.
	done(queue.Job{Type: queue.JobCheck, Slug: "series", Part: "part-01.md"})
	if got := n.nextNotice(t); got[0] != "Check stopped: part-01.md" {
		t.Errorf("worker check notice = %q", got)
	}

	// Asks never notify; nor does anything without a shell.
	done(queue.Job{Type: queue.JobAsk, Slug: "series", Part: "part-01.md"})
	n.noNotice(t)
	srv.SetNative(nil)
	done(queue.Job{Type: queue.JobVerify, Slug: "series"})
	n.noNotice(t)
}
