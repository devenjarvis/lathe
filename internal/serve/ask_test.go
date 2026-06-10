package serve

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devenjarvis/lathe/internal/store"
)

// makeTutFixture builds a small tutorial fixture and returns the tutorial dir.
// Kept inline (rather than relying on serve_test.go's helper) because this
// test file is in package serve while server_test.go is in package serve_test.
func makeTutFixture(t *testing.T, dir, slug string, series bool) string {
	t.Helper()
	tutDir := filepath.Join(dir, slug)
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	tut := &store.Tutorial{
		Slug:    slug,
		Title:   "Test Tutorial",
		Status:  store.StatusVerified,
		Created: time.Now(),
	}
	if series {
		tut.Parts = []string{"part-01.md", "part-02.md"}
		for _, p := range tut.Parts {
			if err := os.WriteFile(filepath.Join(tutDir, p), []byte("# "+p), 0644); err != nil {
				t.Fatal(err)
			}
		}
	} else {
		if err := os.WriteFile(filepath.Join(tutDir, "index.md"), []byte("# Index"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}
	return tutDir
}

func postAsk(t *testing.T, srv *Server, slug, part string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/-/ask/"+slug+"/"+part, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func TestAskHandlerValidation(t *testing.T) {
	dir := t.TempDir()
	makeTutFixture(t, dir, "tut", false)
	makeTutFixture(t, dir, "series", true)
	srv := NewServer(dir)

	t.Run("unknown slug returns 404", func(t *testing.T) {
		w := postAsk(t, srv, "nope", "index.md", []byte(`{"question":"hi"}`))
		if w.Code != http.StatusNotFound {
			t.Errorf("unknown slug = %d, want 404", w.Code)
		}
	})

	t.Run("known slug, unknown part returns 404", func(t *testing.T) {
		w := postAsk(t, srv, "tut", "missing.md", []byte(`{"question":"hi"}`))
		if w.Code != http.StatusNotFound {
			t.Errorf("unknown part = %d, want 404", w.Code)
		}
	})

	t.Run("non-md part returns 404", func(t *testing.T) {
		w := postAsk(t, srv, "tut", "index.txt", []byte(`{"question":"hi"}`))
		if w.Code != http.StatusNotFound {
			t.Errorf("non-md part = %d, want 404", w.Code)
		}
	})

	t.Run("slug with leading dot returns 404", func(t *testing.T) {
		// ServeMux path-cleans `..` segments before matching, so a literal
		// `..` slug never reaches the handler. A slug like `.hidden` does
		// reach us though, and should still 404 because no metadata exists.
		w := postAsk(t, srv, ".hidden", "index.md", []byte(`{"question":"hi"}`))
		if w.Code != http.StatusNotFound {
			t.Errorf(".hidden slug = %d, want 404", w.Code)
		}
	})

	t.Run("empty body returns 400", func(t *testing.T) {
		w := postAsk(t, srv, "tut", "index.md", []byte(``))
		if w.Code != http.StatusBadRequest {
			t.Errorf("empty body = %d, want 400", w.Code)
		}
	})

	t.Run("bad json returns 400", func(t *testing.T) {
		w := postAsk(t, srv, "tut", "index.md", []byte(`{not json`))
		if w.Code != http.StatusBadRequest {
			t.Errorf("bad json = %d, want 400", w.Code)
		}
	})

	t.Run("blank question returns 400", func(t *testing.T) {
		w := postAsk(t, srv, "tut", "index.md", []byte(`{"question":"   "}`))
		if w.Code != http.StatusBadRequest {
			t.Errorf("blank question = %d, want 400", w.Code)
		}
	})

	t.Run("oversize body returns 400", func(t *testing.T) {
		// 10KB question -> oversize since the cap is 8KB.
		big := strings.Repeat("a", 10*1024)
		body := []byte(`{"question":"` + big + `"}`)
		w := postAsk(t, srv, "tut", "index.md", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("oversize body = %d, want 400", w.Code)
		}
	})

	t.Run("GET on ask route is rejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/-/ask/tut/index.md", nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code == http.StatusOK {
			t.Errorf("GET /-/ask = %d, want non-200 (method not allowed)", w.Code)
		}
	})
}

// A valid question is answered by handing the reader the /lathe-ask command to
// paste into their interactive coding-agent session — carrying their question
// verbatim — rather than spawning a model from the binary.
func TestAskReturnsHandoffCommand(t *testing.T) {
	dir := t.TempDir()
	makeTutFixture(t, dir, "tut", false)
	srv := NewServer(dir)

	w := postAsk(t, srv, "tut", "index.md", []byte(`{"question":"Why a ring buffer?"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("valid ask = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "/lathe-ask tut index.md") {
		t.Errorf("body = %q, want the /lathe-ask handoff command", body)
	}
	if !strings.Contains(body, "Why a ring buffer?") {
		t.Errorf("body = %q, want the reader's question carried into the handoff", body)
	}
}
