package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devenjarvis/lathe/internal/acp"
	"github.com/devenjarvis/lathe/internal/acp/acptest"
	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/mcp"
	"github.com/devenjarvis/lathe/internal/queue"
	"github.com/devenjarvis/lathe/internal/store"
)

// addTut stores a minimal one-part tutorial titled title.
func addTut(t *testing.T, tutorials, slug, title string) {
	t.Helper()
	dir := filepath.Join(tutorials, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "part-01.md"), []byte("# "+title), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteMetadata(dir, &store.Tutorial{Slug: slug, Title: title, Status: store.StatusUnverified, Parts: []string{"part-01.md"}, Created: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

// noRedirect posts a form and returns the status and Location.
func postShelfForm(t *testing.T, ts string, path string, form url.Values) (int, string) {
	t.Helper()
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.PostForm(ts+path, form)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("Location")
}

func TestShelfSortRunsOnTheAgentAndAppliesAfterReview(t *testing.T) {
	f := &fakeHost{}
	var (
		mu     sync.Mutex
		prompt string
	)
	srv, ts, tutorials := newTutServer(t, f, func() *acptest.Agent {
		return &acptest.Agent{
			Plan: []string{"Read the library", "Decide shelves", "Record"},
			Reply: func(p string) string {
				mu.Lock()
				prompt = p
				mu.Unlock()
				// The session's MCP server: propose mode, which can't move anything.
				m := &mcp.Server{Propose: mcp.ProposeSort}
				for _, tool := range m.Tools() {
					if tool.Name != "lathe_shelf_list" && tool.Name != "lathe_shelf_propose" {
						t.Errorf("a propose-mode server offered %s", tool.Name)
					}
				}
				for slug, shelf := range map[string]string{"lox": "Interpreters", "forth": "Interpreters", "synth": "Audio"} {
					if text, isErr := mcpCall(t, m, "lathe_shelf_propose", map[string]any{"slug": slug, "shelf": shelf}); isErr {
						t.Errorf("propose %s: %s", slug, text)
					}
				}
				return "Proposed two shelves."
			},
		}
	})
	addTut(t, tutorials, "lox", "Crafting a Lox interpreter")
	addTut(t, tutorials, "forth", "A Forth in Zig")
	addTut(t, tutorials, "synth", "A digital synth")
	if _, err := store.UpdateShelves(srv.shelvesPath(), func(s *store.Shelves) error {
		sh, err := s.Create("Audio")
		if err != nil {
			return err
		}
		return s.Assign("synth", sh.ID)
	}); err != nil {
		t.Fatal(err)
	}

	code, out := postJob(t, ts, "/-/shelves/sort", "{}")
	if code != http.StatusOK || out["mode"] != "agent" {
		t.Fatalf("POST /-/shelves/sort = %d %v", code, out)
	}
	evs := eventStream(t, ts, out["runId"], nil)
	if e := last(evs); e.Type != "done" || e.Message != "" {
		t.Fatalf("final event = %+v", e)
	}
	mu.Lock()
	for _, want := range []string{"/lathe-shelf sort", `"slug": "lox"`, `"name": "Audio"`, "Lathe — Shelve the Library", "lathe_shelf_propose"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	mu.Unlock()
	var mcpArgs []acp.MCPServer
	_ = json.Unmarshal(f.agent(0).MCPServers[0], &mcpArgs)
	if len(mcpArgs) != 1 || strings.Join(mcpArgs[0].Args, " ") != "mcp --propose sort" {
		t.Errorf("session MCP servers = %+v", mcpArgs)
	}
	if s, _ := store.ReadShelves(srv.shelvesPath()); s.ShelfOf("lox").ID != store.UnsortedShelfID {
		t.Fatal("the run moved a tutorial before the reader applied it")
	}

	page := get(t, ts, "/-/settings")
	for _, want := range []string{`id="sortReview"`, "2 tutorials would move", "1 new shelf: Interpreters", `value="lox" checked`, "1 stay where they are", "Apply 2 moves"} {
		if !strings.Contains(page, want) {
			t.Errorf("Settings review lacks %q", want)
		}
	}

	// Untick forth: only lox moves.
	code, loc := postShelfForm(t, ts.URL, "/-/shelves/sort/apply", url.Values{"slug": {"lox"}})
	if code != http.StatusSeeOther || loc != "/-/settings?sorted=1#library" {
		t.Fatalf("apply = %d %s", code, loc)
	}
	s, _ := store.ReadShelves(srv.shelvesPath())
	if s.ShelfOf("lox").Name != "Interpreters" || !s.ShelfOf("forth").Unsorted() || s.ShelfOf("synth").Name != "Audio" {
		t.Errorf("after apply: %+v", s.Placements)
	}
	if page := get(t, ts, "/-/settings?sorted=1"); !strings.Contains(page, "Moved 1 tutorial.") || strings.Contains(page, `id="sortReview"`) {
		t.Error("after apply the review should be gone and the note shown")
	}
}

func TestShelfSortFallsBackToWorkerThenHandoff(t *testing.T) {
	srv, ts, tutorials := newTutServer(t, nil, nil)
	if code, _ := postJob(t, ts, "/-/shelves/sort", "{}"); code != http.StatusBadRequest {
		t.Errorf("sorting an empty library = %d, want 400", code)
	}
	addTut(t, tutorials, "lox", "Lox")
	code, out := postJob(t, ts, "/-/shelves/sort", "{}")
	if code != http.StatusOK || out["mode"] != "handoff" || out["command"] != "/lathe-shelf sort" {
		t.Fatalf("no agent = %d %v", code, out)
	}
	if page := get(t, ts, "/-/settings"); !strings.Contains(page, "Sort my shelves…") || !strings.Contains(page, "command to paste") {
		t.Error("Settings lacks the Sort button and its no-agent hint")
	}
	srv.queue.MarkWorkerSeen()
	code, out = postJob(t, ts, "/-/shelves/sort", "{}")
	if job, ok := srv.queue.Get(out["jobId"]); code != http.StatusOK || !ok || job.Type != queue.JobShelf || job.Command != "/lathe-shelf sort" {
		t.Errorf("with a worker = %d %v %+v", code, out, job)
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/-/shelves/sort", strings.NewReader("{}"))
	req.Header.Set("Origin", "https://evil.example")
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin sort = %v %v", resp, err)
	}
}

// bigShelf puts n tutorials on a shelf named Languages.
func bigShelf(t *testing.T, srv *Server, tutorials string, n int) string {
	t.Helper()
	var id string
	_, err := store.UpdateShelves(srv.shelvesPath(), func(s *store.Shelves) error {
		sh, err := s.Create("Languages")
		id = sh.ID
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= n; i++ {
		growShelf(t, srv, tutorials, id, fmt.Sprintf("t%02d", i))
	}
	return id
}

func growShelf(t *testing.T, srv *Server, tutorials, id, slug string) {
	t.Helper()
	addTut(t, tutorials, slug, "Tutorial "+slug)
	if _, err := store.UpdateShelves(srv.shelvesPath(), func(s *store.Shelves) error { return s.Assign(slug, id) }); err != nil {
		t.Fatal(err)
	}
}

func TestSplitBannerPreviewDismissAndApply(t *testing.T) {
	srv, ts, tutorials := newTutServer(t, nil, nil)
	id := bigShelf(t, srv, tutorials, 12)
	if page := get(t, ts, "/?shelf="+id); strings.Contains(page, `class="split-banner"`) {
		t.Error("a shelf at the threshold (12) shows the banner")
	}
	growShelf(t, srv, tutorials, id, "t13")
	page := get(t, ts, "/?shelf="+id)
	for _, want := range []string{"13 tutorials is a lot for one shelf.", "Suggest a split", `/-/shelves/` + id + `/split/dismiss`} {
		if !strings.Contains(page, want) {
			t.Errorf("banner lacks %q", want)
		}
	}
	if code, out := postJob(t, ts, "/-/shelves/"+id+"/split/suggest", "{}"); code != http.StatusOK || out["command"] != "/lathe-shelf split "+id {
		t.Errorf("suggest without an agent = %d %v", code, out)
	}
	if code, _ := postJob(t, ts, "/-/shelves/unsorted/split/suggest", "{}"); code != http.StatusNotFound {
		t.Errorf("suggest a split of Unsorted = %d", code)
	}

	// The threshold is a setting: 16 hides it, Never too.
	for _, v := range []int{16, 0} {
		v := v
		_ = config.UpdateConfig(func(c *config.Config) error { c.ShelfSplitAt = &v; return nil })
		if strings.Contains(get(t, ts, "/?shelf="+id), `class="split-banner"`) {
			t.Errorf("banner shows with shelf_split_at=%d", v)
		}
	}
	_ = config.UpdateConfig(func(c *config.Config) error { c.ShelfSplitAt = nil; return nil })

	var a, b []string
	for i := 1; i <= 13; i++ {
		slug := fmt.Sprintf("t%02d", i)
		if i <= 7 {
			a = append(a, slug)
		} else {
			b = append(b, slug)
		}
	}
	if _, err := store.ProposeSplit(tutorials, id, []store.SplitGroup{{Name: "Interpreters", Slugs: a}, {Name: "Compilers", Slugs: b}}); err != nil {
		t.Fatal(err)
	}
	page = get(t, ts, "/?shelf="+id)
	for _, want := range []string{"Split it into Interpreters (7) and Compilers (6)?", "Preview split", `id="splitPreview"`, "the shelf is renamed Interpreters", "A new shelf, right after this one", "Split into two shelves", ">Tutorial t13<"} {
		if !strings.Contains(page, want) {
			t.Errorf("proposal banner lacks %q", want)
		}
	}

	// Not now: gone until the shelf grows by 3.
	if code, loc := postShelfForm(t, ts.URL, "/-/shelves/"+id+"/split/dismiss", nil); code != http.StatusSeeOther || loc != "/?shelf="+id {
		t.Fatalf("dismiss = %d %s", code, loc)
	}
	if strings.Contains(get(t, ts, "/?shelf="+id), `class="split-banner"`) {
		t.Error("banner still shows after Not now")
	}
	growShelf(t, srv, tutorials, id, "t14")
	growShelf(t, srv, tutorials, id, "t15")
	if strings.Contains(get(t, ts, "/?shelf="+id), `class="split-banner"`) {
		t.Error("banner back after growing by 2")
	}
	growShelf(t, srv, tutorials, id, "t16")
	if page := get(t, ts, "/?shelf="+id); !strings.Contains(page, "16 tutorials is a lot") || !strings.Contains(page, "Interpreters (10) and Compilers (6)") {
		t.Error("banner not back (with the newcomers on the first side) after growing by 3")
	}

	code, loc := postShelfForm(t, ts.URL, "/-/shelves/"+id+"/split", nil)
	if code != http.StatusSeeOther || !strings.Contains(loc, "split="+id) {
		t.Fatalf("split = %d %s", code, loc)
	}
	s, _ := store.ReadShelves(srv.shelvesPath())
	if sh, _ := s.Get(id); sh.Name != "Interpreters" || s.ShelfOf("t13").Name != "Compilers" || s.ShelfOf("t16").ID != id {
		t.Errorf("after split: %+v %+v", s.Shelves, s.Placements)
	}
	if page := get(t, ts, loc); !strings.Contains(page, "Split into Interpreters and Compilers") || strings.Contains(page, `class="split-banner"`) {
		t.Error("after the split the toast should show and the banner be gone")
	}
}

func TestShelfSplitRunsOnTheAgentScopedToTheShelf(t *testing.T) {
	f := &fakeHost{}
	var id string
	var prompt string
	srv, ts, tutorials := newTutServer(t, f, func() *acptest.Agent {
		return &acptest.Agent{Reply: func(p string) string {
			prompt = p
			m := &mcp.Server{Propose: mcp.ProposeSplit, Shelf: id}
			groups := []map[string]any{{"name": "Interpreters", "slugs": []string{"t01", "t02"}}, {"name": "Compilers", "slugs": []string{"t03"}}}
			if text, isErr := mcpCall(t, m, "lathe_shelf_split_propose", map[string]any{"groups": groups}); isErr {
				t.Error(text)
			}
			return "Split proposed."
		}}
	})
	id = bigShelf(t, srv, tutorials, 3)
	addTut(t, tutorials, "elsewhere", "Not on the shelf")
	code, out := postJob(t, ts, "/-/shelves/"+id+"/split/suggest", "{}")
	if code != http.StatusOK || out["mode"] != "agent" {
		t.Fatalf("suggest = %d %v", code, out)
	}
	if e := last(eventStream(t, ts, out["runId"], nil)); e.Type != "done" || e.Message != "" {
		t.Fatalf("final = %+v", e)
	}
	if !strings.Contains(prompt, "/lathe-shelf split "+id) || strings.Contains(prompt, `"slug": "elsewhere"`) {
		t.Errorf("prompt should list only the shelf's tutorials:\n%.800s", prompt)
	}
	var servers []acp.MCPServer
	_ = json.Unmarshal(f.agent(0).MCPServers[0], &servers)
	if len(servers) != 1 || strings.Join(servers[0].Args, " ") != "mcp --propose split --shelf "+id {
		t.Errorf("MCP servers = %+v", servers)
	}
	sug, _ := store.ReadShelfSuggestions(tutorials)
	if p := sug.Splits[id]; p == nil || p.Groups[0].Name != "Interpreters" {
		t.Errorf("split = %+v", p)
	}
}

func TestShelfJobEndingWithoutAProposalSaysSo(t *testing.T) {
	f := &fakeHost{}
	_, ts, tutorials := newTutServer(t, f, func() *acptest.Agent {
		return &acptest.Agent{Reply: func(string) string { return "I could not decide." }}
	})
	addTut(t, tutorials, "lox", "Lox")
	_, out := postJob(t, ts, "/-/shelves/sort", "{}")
	if e := last(eventStream(t, ts, out["runId"], nil)); e.Type != "done" || !strings.Contains(e.Message, "without proposing") {
		t.Errorf("final = %+v", e)
	}
}

func TestSettingsShelfSplitAt(t *testing.T) {
	_, ts, _ := settingsServer(t)
	if !strings.Contains(get(t, ts, "/-/settings"), `name="shelf_split_at" value="12" data-setting="shelf_split_at" data-setting-int checked`) {
		t.Error("Library tab lacks the split threshold with 12 checked")
	}
	for _, v := range []int{8, 24, 0, 12} {
		if code, body := postSettings(t, ts, "/-/settings", fmt.Sprintf(`{"shelf_split_at":%d}`, v)); code != http.StatusOK || config.LoadSettings().ShelfSplitAt != v {
			t.Errorf("save %d: %d %s", v, code, body)
		}
	}
	if c, _ := config.ReadConfig(); c.ShelfSplitAt != nil {
		t.Error("the default (12) should not be written")
	}
	if code, _ := postSettings(t, ts, "/-/settings", `{"shelf_split_at":13}`); code != http.StatusBadRequest {
		t.Errorf("13 = %d, want 400", code)
	}
}

func TestNewPromptRespectsTheReadersShelf(t *testing.T) {
	in := &newInputs{newRequest: newRequest{Topic: "a synth", Shelf: "audio"}}
	shelves := []store.ShelfListing{{ID: "audio", Name: "Audio", Count: 2}}
	p := newPrompt("synth", "/tmp/s", "", in, "/lathe a synth --shelf audio", shelves)
	if !strings.Contains(p, "The reader chose the tutorial's shelf") || strings.Contains(p, "The reader's shelves (pick") {
		t.Errorf("a chosen shelf should be respected:\n%.1500s", p)
	}
	in.Shelf = ""
	p = newPrompt("synth", "/tmp/s", "", in, "/lathe a synth", shelves)
	if !strings.Contains(p, "lathe_shelf_set") || !strings.Contains(p, `"name": "Audio"`) {
		t.Errorf("without a choice the agent picks from the shelves:\n%.1500s", p)
	}
	if got := (&newInputs{newRequest: newRequest{Topic: "x", Shelf: "audio"}}).command(); !strings.HasSuffix(got, "--shelf audio") {
		t.Errorf("command = %q", got)
	}
}
