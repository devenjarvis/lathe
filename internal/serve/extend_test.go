package serve_test

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

	"github.com/devenjarvis/lathe/internal/serve"
	"github.com/devenjarvis/lathe/internal/store"
)

func makeExtendTutorial(t *testing.T, dir, slug string, status store.Status, parts []string) string {
	t.Helper()
	tutDir := filepath.Join(dir, slug)
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	tut := &store.Tutorial{
		Slug:    slug,
		Title:   store.SlugToTitle(slug),
		Status:  status,
		Parts:   parts,
		Created: time.Now(),
	}
	for _, p := range parts {
		if err := os.WriteFile(filepath.Join(tutDir, p), []byte("# "+p), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}
	return tutDir
}

func postExtend(t *testing.T, srv *serve.Server, slug string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/-/extend/"+slug, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func TestExtendRejectsWrongMethod(t *testing.T) {
	dir := t.TempDir()
	makeExtendTutorial(t, dir, "test-tut", store.StatusVerified, []string{"part-01.md"})
	srv := serve.NewServer(dir)

	req := httptest.NewRequest(http.MethodGet, "/-/extend/test-tut", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Errorf("GET /-/extend = %d, want method not allowed", w.Code)
	}
}

func TestExtendUnknownSlugIs404(t *testing.T) {
	dir := t.TempDir()
	srv := serve.NewServer(dir)

	w := postExtend(t, srv, "no-such-tutorial", []byte(`{}`))
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown slug = %d, want 404", w.Code)
	}
}

func TestExtendOversizeBodyIs400(t *testing.T) {
	dir := t.TempDir()
	makeExtendTutorial(t, dir, "test-tut", store.StatusVerified, []string{"part-01.md"})
	srv := serve.NewServer(dir)

	big := strings.Repeat("a", 3*1024)
	body := []byte(`{"guidance":"` + big + `"}`)
	w := postExtend(t, srv, "test-tut", body)
	if w.Code != http.StatusBadRequest {
		t.Errorf("oversize body = %d, want 400", w.Code)
	}
}

func TestExtendBlankGuidanceReturnsHandoff(t *testing.T) {
	dir := t.TempDir()
	makeExtendTutorial(t, dir, "test-tut", store.StatusVerified, []string{"part-01.md"})
	srv := serve.NewServer(dir)

	body, _ := json.Marshal(map[string]string{"guidance": ""})
	w := postExtend(t, srv, "test-tut", body)

	if w.Code != http.StatusOK {
		t.Errorf("blank guidance = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "/lathe-extend test-tut") {
		t.Errorf("body = %q, want the /lathe-extend handoff command", w.Body.String())
	}

	// The web button must not change status — the /lathe-extend skill marks it
	// extending when it actually starts.
	tutDir := filepath.Join(dir, "test-tut")
	got, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if got.Status != store.StatusVerified {
		t.Errorf("Status = %q, want %q (handoff must not change status)", got.Status, store.StatusVerified)
	}
}

func TestExtendGuidanceFlowsIntoHandoff(t *testing.T) {
	dir := t.TempDir()
	makeExtendTutorial(t, dir, "test-tut", store.StatusVerified, []string{"part-01.md"})
	srv := serve.NewServer(dir)

	body, _ := json.Marshal(map[string]string{"guidance": "cover the filter envelope"})
	w := postExtend(t, srv, "test-tut", body)
	if w.Code != http.StatusOK {
		t.Fatalf("with guidance = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "/lathe-extend test-tut cover the filter envelope") {
		t.Errorf("body = %q, want guidance folded into the handoff command", w.Body.String())
	}
}

func TestExtendRejectsWhileExtending(t *testing.T) {
	dir := t.TempDir()
	tutDir := filepath.Join(dir, "test-tut")
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tutDir, "part-01.md"), []byte("# Part 1"), 0644); err != nil {
		t.Fatal(err)
	}
	tut := &store.Tutorial{
		Slug:        "test-tut",
		Status:      store.StatusExtending,
		PendingPart: "part-02.md",
		Parts:       []string{"part-01.md"},
		Created:     time.Now(),
	}
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}
	srv := serve.NewServer(dir)

	body, _ := json.Marshal(map[string]string{"guidance": ""})
	w := postExtend(t, srv, "test-tut", body)
	if w.Code != http.StatusConflict {
		t.Errorf("while extending = %d, want 409", w.Code)
	}
}

func TestExtendRejectsWhileVerifying(t *testing.T) {
	dir := t.TempDir()
	makeExtendTutorial(t, dir, "test-tut", store.StatusVerifying, []string{"part-01.md"})
	srv := serve.NewServer(dir)

	body, _ := json.Marshal(map[string]string{"guidance": ""})
	w := postExtend(t, srv, "test-tut", body)
	if w.Code != http.StatusConflict {
		t.Errorf("while verifying = %d, want 409", w.Code)
	}
}
