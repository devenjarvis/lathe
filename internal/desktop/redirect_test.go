package desktop

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFollowRedirectsRewritesSameOrigin(t *testing.T) {
	h := followRedirects(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/demo/part-01.md", http.StatusFound)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/demo/", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Location") != "" {
		t.Fatalf("code %d, Location %q; want a 200 page", rec.Code, rec.Header().Get("Location"))
	}
	body := rec.Body.String()
	if !strings.Contains(body, `location.replace("/demo/part-01.md")`) || strings.Contains(body, "Found") {
		t.Errorf("body = %s", body)
	}
}

func TestFollowRedirectsEscapesTarget(t *testing.T) {
	h := followRedirects(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", `/x"></script><script>alert(1)</script>`)
		w.WriteHeader(http.StatusSeeOther)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/-/delete/x", nil))
	if strings.Contains(rec.Body.String(), "<script>alert") {
		t.Errorf("unescaped target: %s", rec.Body.String())
	}
}

func TestFollowRedirectsLeavesOthersAlone(t *testing.T) {
	// Off-origin redirects pass through as they are.
	h := followRedirects(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com/", http.StatusFound)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusFound {
		t.Errorf("off-origin redirect rewritten: %d", rec.Code)
	}

	// Streams keep flushing through the wrapper.
	srv := httptest.NewServer(followRedirects(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: one\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	buf := make([]byte, 64)
	n, err := resp.Body.Read(buf)
	if err != nil || !strings.Contains(string(buf[:n]), "data: one") {
		t.Errorf("stream read %q, %v", buf[:n], err)
	}
}
