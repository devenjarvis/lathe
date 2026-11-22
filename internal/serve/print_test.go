package serve_test

import (
	"bytes"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/devenjarvis/lathe/internal/serve"
	"github.com/devenjarvis/lathe/internal/store"
)

// printBudget caps the embedded linocut prints, which every build carries.
const printBudget = 150 << 10

func get(t *testing.T, srv *serve.Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

// TestPrintAssets serves every print the stylesheet names, as a real PNG,
// and keeps the set inside its size budget.
func TestPrintAssets(t *testing.T) {
	srv := serve.NewServer(t.TempDir())
	home := get(t, srv, "/").Body.String()
	names := map[string]bool{}
	for _, m := range regexp.MustCompile(`/_static/(print-[a-z-]+\.png)`).FindAllStringSubmatch(home, -1) {
		names[m[1]] = true
	}
	for _, want := range []string{"print-spindle-colour.png", "print-spindle-key.png", "print-turning-colour.png", "print-turning-key.png", "print-grain.png"} {
		if !names[want] {
			t.Errorf("styles do not reference %s", want)
		}
	}
	total := 0
	for name := range names {
		w := get(t, srv, "/_static/"+name)
		if w.Code != http.StatusOK {
			t.Fatalf("GET /_static/%s = %d, want 200", name, w.Code)
		}
		if ct := w.Header().Get("Content-Type"); ct != "image/png" {
			t.Errorf("%s Content-Type = %q, want image/png", name, ct)
		}
		if _, err := png.Decode(bytes.NewReader(w.Body.Bytes())); err != nil {
			t.Errorf("%s is not a PNG: %v", name, err)
		}
		total += w.Body.Len()
	}
	if total > printBudget {
		t.Errorf("prints weigh %d bytes, over the %d budget", total, printBudget)
	}
}

func TestEmptyHomeShowsPrint(t *testing.T) {
	srv := serve.NewServer(t.TempDir())
	body := get(t, srv, "/").Body.String()
	if !strings.Contains(body, `<span class="print print-spindle" aria-hidden="true"></span>`) {
		t.Error("empty Home is missing the decorative spindle print")
	}
}

func TestVerifiedHeaderIsStamped(t *testing.T) {
	dir := t.TempDir()
	tutDir := filepath.Join(dir, "stamped")
	if err := os.MkdirAll(tutDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tutDir, "part-01.md"), []byte("# Part 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	tut := &store.Tutorial{Slug: "stamped", Title: "Stamped", Status: store.StatusVerified, Created: time.Now(), Parts: []string{"part-01.md"}}
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteVerifyResult(tutDir, &store.VerifyResult{Status: store.StatusVerified, CheckedAt: "2026-06-03T12:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	header := articleHeader(t, get(t, serve.NewServer(dir), "/stamped/part-01.md").Body.String())
	if !strings.Contains(header, `class="badge verified badge-stamp"`) {
		t.Error("a verified tutorial's header badge is not the ink stamp")
	}
}
