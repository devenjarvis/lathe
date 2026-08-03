package serve_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devenjarvis/lathe/internal/drift"
	"github.com/devenjarvis/lathe/internal/serve"
	"github.com/devenjarvis/lathe/internal/store"
)

const driftAppGo = "package app\n\nfunc One() {}\n\nfunc Two() {}\n"

func driftGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newDriftFixture builds a git repo plus a tutorials dir containing one
// onboarding guide anchored at app.go:3-3.
func newDriftFixture(t *testing.T, status store.Status) (tutorialsDir, repoDir, sha string) {
	t.Helper()
	repoDir = t.TempDir()
	driftGit(t, repoDir, "init", "-b", "main")
	driftGit(t, repoDir, "config", "user.email", "test@example.com")
	driftGit(t, repoDir, "config", "user.name", "Test")
	driftGit(t, repoDir, "config", "commit.gpgsign", "false")
	driftGit(t, repoDir, "remote", "add", "origin", "git@github.com:devenjarvis/example.git")
	if err := os.WriteFile(filepath.Join(repoDir, "app.go"), []byte(driftAppGo), 0644); err != nil {
		t.Fatal(err)
	}
	driftGit(t, repoDir, "add", "-A")
	driftGit(t, repoDir, "commit", "-m", "init")
	sha = driftGit(t, repoDir, "rev-parse", "HEAD")

	tutorialsDir = t.TempDir()
	tutDir := filepath.Join(tutorialsDir, "example-onboarding")
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	part := "# Guide\n\nThe entry point:\n\n```go path=app.go lines=3-3\nfunc One() {}\n```\n"
	if err := os.WriteFile(filepath.Join(tutDir, "part-01.md"), []byte(part), 0644); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteMetadata(tutDir, &store.Tutorial{
		Slug:       "example-onboarding",
		Title:      "Example Onboarding",
		Status:     status,
		Parts:      []string{"part-01.md"},
		Kind:       store.KindOnboarding,
		Repo:       "github.com/devenjarvis/example",
		RepoBranch: "main",
		RepoCommit: sha,
		RepoPath:   repoDir,
		Created:    time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	return tutorialsDir, repoDir, sha
}

func postDrift(t *testing.T, srv *serve.Server, slug string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/-/drift/"+slug, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

// TestDriftEndpointRunsWithNoWorkerConnected pins the property that separates
// drift from verify/extend: it needs no model, so it answers directly with no
// /lathe-work session in sight. If this ever starts returning a queued or
// handoff mode, drift has been wrongly folded into the job queue.
func TestDriftEndpointRunsWithNoWorkerConnected(t *testing.T) {
	tutorialsDir, _, sha := newDriftFixture(t, store.StatusVerified)
	srv := serve.NewServer(tutorialsDir)

	w := postDrift(t, srv, "example-onboarding", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /-/drift = %d, want 200\n%s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("response is not JSON: %v\n%s", err, w.Body.String())
	}
	if got["mode"] != "drift" {
		t.Errorf("mode = %v, want \"drift\" (never queued/handoff — drift needs no worker)", got["mode"])
	}
	if got["stale"] != false {
		t.Errorf("stale = %v, want false", got["stale"])
	}
	if section, _ := got["section"].(string); !strings.Contains(section, "Check for drift") {
		t.Errorf("response should carry the re-rendered region, got: %q", section)
	}

	// The check must have persisted drift.json.
	res, err := store.ReadDrift(filepath.Join(tutorialsDir, "example-onboarding"))
	if err != nil {
		t.Fatalf("ReadDrift: %v", err)
	}
	if res.PinnedCommit != sha {
		t.Errorf("drift.json PinnedCommit = %q, want %q", res.PinnedCommit, sha)
	}
}

func TestDriftEndpointFlipsToStale(t *testing.T) {
	tutorialsDir, repoDir, _ := newDriftFixture(t, store.StatusVerified)
	edited := strings.Replace(driftAppGo, "func One() {}", "func One(x int) {}", 1)
	if err := os.WriteFile(filepath.Join(repoDir, "app.go"), []byte(edited), 0644); err != nil {
		t.Fatal(err)
	}
	driftGit(t, repoDir, "commit", "-am", "change One")

	srv := serve.NewServer(tutorialsDir)
	w := postDrift(t, srv, "example-onboarding", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /-/drift = %d\n%s", w.Code, w.Body.String())
	}
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["stale"] != true {
		t.Errorf("stale = %v, want true", got["stale"])
	}
	if got["status"] != string(store.StatusStale) {
		t.Errorf("status = %v, want stale", got["status"])
	}
	section, _ := got["section"].(string)
	if !strings.Contains(section, "This guide has drifted") {
		t.Errorf("section should carry the warning panel, got:\n%s", section)
	}
	if !strings.Contains(section, "app.go:3") {
		t.Errorf("warning panel should name the drifted anchor, got:\n%s", section)
	}

	tut, _ := store.ReadMetadata(filepath.Join(tutorialsDir, "example-onboarding"))
	if tut.Status != store.StatusStale {
		t.Errorf("metadata Status = %q, want stale", tut.Status)
	}
}

func TestDriftEndpointUnknownPinLeavesStatusAlone(t *testing.T) {
	tutorialsDir, _, _ := newDriftFixture(t, store.StatusVerified)
	tutDir := filepath.Join(tutorialsDir, "example-onboarding")
	tut, _ := store.ReadMetadata(tutDir)
	tut.RepoCommit = "0000000000000000000000000000000000000000"
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}

	srv := serve.NewServer(tutorialsDir)
	w := postDrift(t, srv, "example-onboarding", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /-/drift = %d, want 200 (unknown is an answer, not an error)", w.Code)
	}
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["mode"] != "unknown" {
		t.Errorf("mode = %v, want \"unknown\"", got["mode"])
	}
	after, _ := store.ReadMetadata(tutDir)
	if after.Status != store.StatusVerified {
		t.Errorf("Status = %q, want verified — an unknown pin must not change status", after.Status)
	}
	if _, err := store.ReadDrift(tutDir); !os.IsNotExist(err) {
		t.Errorf("an unknown pin must not write drift.json: err=%v", err)
	}
}

func TestDriftEndpointRejectsCrossOrigin(t *testing.T) {
	tutorialsDir, _, _ := newDriftFixture(t, store.StatusVerified)
	srv := serve.NewServer(tutorialsDir)

	w := postDrift(t, srv, "example-onboarding", map[string]string{"Origin": "https://evil.example.com"})
	if w.Code != http.StatusForbidden {
		t.Errorf("cross-origin POST = %d, want 403", w.Code)
	}
	w = postDrift(t, srv, "example-onboarding", map[string]string{"Origin": "http://localhost:4242"})
	if w.Code != http.StatusOK {
		t.Errorf("same-origin POST = %d, want 200\n%s", w.Code, w.Body.String())
	}
}

func TestDriftEndpointRejectsTraversalSlug(t *testing.T) {
	tutorialsDir, _, _ := newDriftFixture(t, store.StatusVerified)
	srv := serve.NewServer(tutorialsDir)

	for _, slug := range []string{"..", "..%2Fetc", "."} {
		req := httptest.NewRequest(http.MethodPost, "/-/drift/"+slug, nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code == http.StatusOK {
			t.Errorf("POST /-/drift/%s = 200, want a rejection", slug)
		}
	}
}

func TestDriftEndpointRejectsPlainTutorial(t *testing.T) {
	tutorialsDir := t.TempDir()
	tutDir := filepath.Join(tutorialsDir, "plain")
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tutDir, "part-01.md"), []byte("# Hi"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteMetadata(tutDir, &store.Tutorial{
		Slug: "plain", Title: "Plain", Status: store.StatusUnverified,
		Parts: []string{"part-01.md"}, Created: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	srv := serve.NewServer(tutorialsDir)
	w := postDrift(t, srv, "plain", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("POST /-/drift on a plain tutorial = %d, want 400", w.Code)
	}
}

func TestReadingPageShowsDriftSectionInsteadOfVerifyForm(t *testing.T) {
	tutorialsDir, _, sha := newDriftFixture(t, store.StatusUnverified)
	srv := serve.NewServer(tutorialsDir)

	req := httptest.NewRequest(http.MethodGet, "/example-onboarding/part-01.md", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET part = %d", w.Code)
	}
	body := w.Body.String()

	if !strings.Contains(body, `id="driftForm"`) {
		t.Error("onboarding reading page missing the drift form")
	}
	if !strings.Contains(body, "Check for drift") {
		t.Error("onboarding reading page missing the drift button label")
	}
	if !strings.Contains(body, sha[:8]) {
		t.Errorf("onboarding reading page should show the short pin %q", sha[:8])
	}
	if !strings.Contains(body, "never checked") {
		t.Error("a guide with no drift.json should say it has never been checked")
	}
	if strings.Contains(body, "Verify this tutorial") {
		t.Error("the mktemp-style verify label must not appear on an onboarding guide")
	}
	// The anchored excerpt still renders normally.
	if !strings.Contains(body, `data-path="app.go"`) {
		t.Error("anchored block missing from the rendered part")
	}
	// (The inlined stylesheet carries .anchor[data-drift="…"] rules, so match the
	// attribute as it appears on the open tag rather than the bare string.)
	if strings.Contains(body, `data-drift="changed">`) {
		t.Error("a never-checked guide must not mark any anchor as drifted")
	}
}

func TestReadingPageMarksDriftedAnchors(t *testing.T) {
	tutorialsDir, repoDir, _ := newDriftFixture(t, store.StatusVerified)
	edited := strings.Replace(driftAppGo, "func One() {}", "func One(x int) {}", 1)
	if err := os.WriteFile(filepath.Join(repoDir, "app.go"), []byte(edited), 0644); err != nil {
		t.Fatal(err)
	}
	driftGit(t, repoDir, "commit", "-am", "change One")

	srv := serve.NewServer(tutorialsDir)
	if w := postDrift(t, srv, "example-onboarding", nil); w.Code != http.StatusOK {
		t.Fatalf("drift check failed: %s", w.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/example-onboarding/part-01.md", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	body := w.Body.String()

	if !strings.Contains(body, `data-drift="changed">`) {
		t.Errorf("the changed anchor should be marked in the rendered part")
	}
	if !strings.Contains(body, "This guide has drifted") {
		t.Error("the reading page should show the drift warning panel")
	}
}

func TestStatusEndpointCarriesDriftRegion(t *testing.T) {
	tutorialsDir, _, _ := newDriftFixture(t, store.StatusUnverified)
	srv := serve.NewServer(tutorialsDir)

	req := httptest.NewRequest(http.MethodGet, "/-/status/example-onboarding/part-01.md", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /-/status = %d", w.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	driftRegion, _ := got["drift"].(string)
	if !strings.Contains(driftRegion, `id="driftForm"`) {
		t.Errorf("status JSON drift key should carry the rendered region, got: %q", driftRegion)
	}
	if verify, _ := got["verify"].(string); strings.TrimSpace(verify) != "" {
		t.Errorf("an onboarding guide should render no verify region, got: %q", verify)
	}
}

func TestStatusEndpointKeepsVerifyRegionForPlainTutorials(t *testing.T) {
	tutorialsDir := t.TempDir()
	tutDir := filepath.Join(tutorialsDir, "plain")
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tutDir, "part-01.md"), []byte("# Hi"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteMetadata(tutDir, &store.Tutorial{
		Slug: "plain", Title: "Plain", Status: store.StatusUnverified,
		Parts: []string{"part-01.md"}, Created: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	srv := serve.NewServer(tutorialsDir)
	req := httptest.NewRequest(http.MethodGet, "/-/status/plain/part-01.md", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if verify, _ := got["verify"].(string); !strings.Contains(verify, "Verify this tutorial") {
		t.Errorf("a plain tutorial must keep its verify region unchanged, got: %q", verify)
	}
	if driftRegion, _ := got["drift"].(string); strings.TrimSpace(driftRegion) != "" {
		t.Errorf("a plain tutorial should render no drift region, got: %q", driftRegion)
	}
}

func TestDriftRecheckClearsStale(t *testing.T) {
	tutorialsDir, repoDir, _ := newDriftFixture(t, store.StatusVerified)
	tutDir := filepath.Join(tutorialsDir, "example-onboarding")

	edited := strings.Replace(driftAppGo, "func One() {}", "func One(x int) {}", 1)
	if err := os.WriteFile(filepath.Join(repoDir, "app.go"), []byte(edited), 0644); err != nil {
		t.Fatal(err)
	}
	driftGit(t, repoDir, "commit", "-am", "change One")

	srv := serve.NewServer(tutorialsDir)
	postDrift(t, srv, "example-onboarding", nil)
	if tut, _ := store.ReadMetadata(tutDir); tut.Status != store.StatusStale {
		t.Fatalf("setup: Status = %q, want stale", tut.Status)
	}

	driftGit(t, repoDir, "revert", "--no-edit", "HEAD")
	postDrift(t, srv, "example-onboarding", nil)
	if tut, _ := store.ReadMetadata(tutDir); tut.Status != store.StatusUnverified {
		t.Errorf("Status = %q, want unverified after a clean re-check", tut.Status)
	}
}

func TestDriftEndpointSkipsInFlightStatus(t *testing.T) {
	tutorialsDir, repoDir, _ := newDriftFixture(t, store.StatusVerifying)
	edited := strings.Replace(driftAppGo, "func One() {}", "func One(x int) {}", 1)
	if err := os.WriteFile(filepath.Join(repoDir, "app.go"), []byte(edited), 0644); err != nil {
		t.Fatal(err)
	}
	driftGit(t, repoDir, "commit", "-am", "change One")

	srv := serve.NewServer(tutorialsDir)
	if w := postDrift(t, srv, "example-onboarding", nil); w.Code != http.StatusOK {
		t.Fatalf("POST /-/drift = %d\n%s", w.Code, w.Body.String())
	}
	tut, _ := store.ReadMetadata(filepath.Join(tutorialsDir, "example-onboarding"))
	if tut.Status != store.StatusVerifying {
		t.Errorf("Status = %q, want verifying — drift must not disturb in-flight work", tut.Status)
	}
	// The record is still written; only the status transition is skipped.
	res, err := store.ReadDrift(filepath.Join(tutorialsDir, "example-onboarding"))
	if err != nil {
		t.Fatalf("ReadDrift: %v", err)
	}
	if res.Summary[drift.VerdictChanged] != 1 {
		t.Errorf("Summary = %#v, want one changed", res.Summary)
	}
}

func TestListPageCarriesKindAndFilters(t *testing.T) {
	tutorialsDir, _, _ := newDriftFixture(t, store.StatusStale)
	// A plain tutorial alongside the onboarding guide, written the pre-feature
	// way (no kind key at all).
	plainDir := filepath.Join(tutorialsDir, "plain")
	if err := os.MkdirAll(plainDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plainDir, "part-01.md"), []byte("# Hi"), 0644); err != nil {
		t.Fatal(err)
	}
	raw := `{"slug":"plain","title":"Plain","topic":"plain","created":"2026-05-03T00:00:00Z","status":"verified","parts":["part-01.md"]}`
	if err := os.WriteFile(filepath.Join(plainDir, "metadata.json"), []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}

	srv := serve.NewServer(tutorialsDir)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET / = %d", w.Code)
	}
	body := w.Body.String()

	if !strings.Contains(body, `data-kind="onboarding"`) {
		t.Error("the onboarding card should carry data-kind=onboarding")
	}
	if !strings.Contains(body, `data-kind="tutorial"`) {
		t.Error("a card with no stored kind should still render data-kind=tutorial")
	}
	if !strings.Contains(body, `class="badge stale"`) {
		t.Error("the stale guide should render the stale badge")
	}
	if !strings.Contains(body, `data-status="stale">Stale<`) {
		t.Error("the status filter row should offer a Stale pill")
	}
	if !strings.Contains(body, `data-kind="onboarding">Onboarding<`) {
		t.Error("the type filter row should offer an Onboarding pill")
	}
}
