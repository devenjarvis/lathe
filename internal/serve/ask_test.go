package serve

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
		Series:  series,
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

func TestBuildAskPrompt(t *testing.T) {
	t.Run("non-series", func(t *testing.T) {
		tut := &store.Tutorial{Title: "Single", Series: false}
		system, user := buildAskPrompt(tut, "index.md", "Hello world", "What is X?")

		if !strings.Contains(system, "Single") {
			t.Errorf("system prompt missing tutorial title %q:\n%s", "Single", system)
		}
		if !strings.Contains(system, "index.md") {
			t.Errorf("system prompt missing current part %q:\n%s", "index.md", system)
		}
		if !strings.Contains(system, "Hello world") {
			t.Errorf("system prompt missing article body:\n%s", system)
		}
		// Non-series should not have the series block listing other parts.
		if strings.Contains(system, "Other parts are also available") {
			t.Errorf("non-series system prompt unexpectedly mentions other parts:\n%s", system)
		}
		if user != "What is X?" {
			t.Errorf("user prompt = %q, want %q", user, "What is X?")
		}
	})

	t.Run("series", func(t *testing.T) {
		tut := &store.Tutorial{
			Title:  "Series",
			Series: true,
			Parts:  []string{"part-01.md", "part-02.md", "part-03.md"},
		}
		system, user := buildAskPrompt(tut, "part-02.md", "Body of part 2", "Why?")

		if !strings.Contains(system, "Body of part 2") {
			t.Errorf("system prompt missing article body:\n%s", system)
		}
		if !strings.Contains(system, "part-01.md") {
			t.Errorf("system prompt missing sibling part-01.md in series block:\n%s", system)
		}
		if !strings.Contains(system, "part-03.md") {
			t.Errorf("system prompt missing sibling part-03.md in series block:\n%s", system)
		}
		// The current part should NOT be listed as another available part.
		// It will appear elsewhere (e.g. "currently reading"), so we look for
		// a bullet-line containing it specifically.
		if strings.Contains(system, "- part-02.md") {
			t.Errorf("series block unexpectedly lists current part as a sibling:\n%s", system)
		}
		if user != "Why?" {
			t.Errorf("user prompt = %q, want %q", user, "Why?")
		}
	})

	t.Run("series with single part omits sibling block", func(t *testing.T) {
		tut := &store.Tutorial{
			Title:  "Solo",
			Series: true,
			Parts:  []string{"part-01.md"},
		}
		system, _ := buildAskPrompt(tut, "part-01.md", "Body", "Q?")
		if strings.Contains(system, "Other parts are also available") {
			t.Errorf("solo series should not include the sibling-parts block:\n%s", system)
		}
	})

	t.Run("nil tutorial does not panic", func(t *testing.T) {
		// buildAskPrompt should be defensive against a nil tut. The handler
		// reads metadata first, but we don't want a NPE inside the helper.
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("buildAskPrompt panicked on nil tut: %v", r)
			}
		}()
		_, _ = buildAskPrompt(nil, "index.md", "Body", "Q?")
	})
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

// TestExtractTextDelta feeds a representative claude stream-json sequence
// (a few content_block_delta partials, then the final `assistant` envelope
// containing the concatenated full text) into extractTextDelta line-by-line
// and asserts the assistant frame contributes zero additional text. This is
// the regression test for the duplicate-output bug: when claude is run with
// --include-partial-messages, the trailing assistant frame is *always* a
// duplicate of what already streamed, so we must ignore it.
func TestExtractTextDelta(t *testing.T) {
	lines := []string{
		// Banner / system frames the client should ignore.
		`{"type":"system","subtype":"init"}`,
		`{"type":"message_start","message":{"id":"msg_1"}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,

		// Partials — these carry the actual streamed text.
		`{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello"}}`,
		`{"type":"content_block_delta","delta":{"type":"text_delta","text":", "}}`,
		// Same partial wrapped in a stream_event envelope, which we must unwrap.
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"world"}}}`,
		`{"type":"content_block_delta","delta":{"type":"text_delta","text":"!"}}`,

		// Stop frame.
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,

		// Final assistant envelope — full concatenated text. Must contribute 0.
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Hello, world!"}]}}`,

		// Result frame (no text).
		`{"type":"result","subtype":"success"}`,
	}

	var streamed strings.Builder
	for _, line := range lines {
		var obj any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("malformed test fixture line %q: %v", line, err)
		}
		streamed.WriteString(extractTextDelta(obj))
	}

	want := "Hello, world!"
	if got := streamed.String(); got != want {
		t.Errorf("streamed output = %q, want %q (duplication or loss of text)", got, want)
	}
}
