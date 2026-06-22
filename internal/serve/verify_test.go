package serve_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devenjarvis/lathe/internal/serve"
	"github.com/devenjarvis/lathe/internal/store"
)

func postVerify(t *testing.T, srv *serve.Server, slug string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/-/verify/"+slug, nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func TestVerifyRejectsWrongMethod(t *testing.T) {
	dir := t.TempDir()
	makeExtendTutorial(t, dir, "test-tut", store.StatusUnverified, []string{"part-01.md"})
	srv := serve.NewServer(dir)

	req := httptest.NewRequest(http.MethodGet, "/-/verify/test-tut", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code == http.StatusOK || w.Code == http.StatusAccepted {
		t.Errorf("GET /-/verify = %d, want method not allowed", w.Code)
	}
}

func TestVerifyUnknownSlugIs404(t *testing.T) {
	dir := t.TempDir()
	srv := serve.NewServer(dir)

	w := postVerify(t, srv, "no-such-tutorial")
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown slug = %d, want 404", w.Code)
	}
}

func TestVerifyReturnsHandoffCommand(t *testing.T) {
	dir := t.TempDir()
	makeExtendTutorial(t, dir, "test-tut", store.StatusUnverified, []string{"part-01.md"})
	srv := serve.NewServer(dir)

	w := postVerify(t, srv, "test-tut")
	if w.Code != http.StatusOK {
		t.Errorf("verify = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "/lathe-verify test-tut") {
		t.Errorf("body = %q, want the /lathe-verify handoff command", w.Body.String())
	}

	// The web button must not change status — the /lathe-verify skill marks it
	// verifying when it actually starts, so the badge can't get stuck.
	got, err := store.ReadMetadata(filepath.Join(dir, "test-tut"))
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if got.Status != store.StatusUnverified {
		t.Errorf("Status = %q, want %q (handoff must not change status)", got.Status, store.StatusUnverified)
	}
}

func TestVerifyRejectsWhileVerifying(t *testing.T) {
	dir := t.TempDir()
	makeExtendTutorial(t, dir, "test-tut", store.StatusVerifying, []string{"part-01.md"})
	srv := serve.NewServer(dir)

	w := postVerify(t, srv, "test-tut")
	if w.Code != http.StatusConflict {
		t.Errorf("while verifying = %d, want 409", w.Code)
	}
}

func TestVerifyRejectsWhileExtending(t *testing.T) {
	dir := t.TempDir()
	makeExtendTutorial(t, dir, "test-tut", store.StatusExtending, []string{"part-01.md"})
	srv := serve.NewServer(dir)

	w := postVerify(t, srv, "test-tut")
	if w.Code != http.StatusConflict {
		t.Errorf("while extending = %d, want 409", w.Code)
	}
}

func TestVerifyButtonRendersForVerifiableStatuses(t *testing.T) {
	for _, status := range []store.Status{store.StatusUnverified, store.StatusFailed, store.StatusSkipped} {
		t.Run(string(status), func(t *testing.T) {
			dir := t.TempDir()
			makeExtendTutorial(t, dir, "test-tut", status, []string{"part-01.md"})
			srv := serve.NewServer(dir)

			req := httptest.NewRequest(http.MethodGet, "/test-tut/part-01.md", nil)
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("GET part = %d, want 200", w.Code)
			}
			body := w.Body.String()
			if !strings.Contains(body, `id="verifyForm"`) {
				t.Errorf("status %q should render the verify form", status)
			}
			if !strings.Contains(body, `action="/-/verify/test-tut"`) {
				t.Errorf("verify form should post to /-/verify/test-tut")
			}
		})
	}
}

func TestVerifyButtonHiddenWhenVerified(t *testing.T) {
	dir := t.TempDir()
	makeExtendTutorial(t, dir, "test-tut", store.StatusVerified, []string{"part-01.md"})
	srv := serve.NewServer(dir)

	req := httptest.NewRequest(http.MethodGet, "/test-tut/part-01.md", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if strings.Contains(w.Body.String(), `id="verifyForm"`) {
		t.Error("verified tutorial should not render the verify form")
	}
}

func TestVerifyingPanelAutoRefreshes(t *testing.T) {
	dir := t.TempDir()
	makeExtendTutorial(t, dir, "test-tut", store.StatusVerifying, []string{"part-01.md"})
	srv := serve.NewServer(dir)

	req := httptest.NewRequest(http.MethodGet, "/test-tut/part-01.md", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET part = %d, want 200", w.Code)
	}
	body := w.Body.String()
	// With JS the status poller updates in place; the full-page meta-refresh now
	// only fires as a <noscript> fallback, never on its own.
	if !strings.Contains(body, `<noscript><meta http-equiv="refresh" content="5"></noscript>`) {
		t.Error("verifying page should carry the noscript meta-refresh fallback")
	}
	// The swappable region the poller targets must be present, with the verifying
	// panel inside it.
	if !strings.Contains(body, `id="verifyRegion"`) {
		t.Error("verifying page should render the #verifyRegion swap container")
	}
	if !strings.Contains(body, `id="statusPoll"`) {
		t.Error("verifying page should render the #statusPoll element that drives polling")
	}
	if strings.Contains(body, `id="verifyForm"`) {
		t.Error("verify form should NOT appear while status is verifying")
	}
}

func TestSkippedBadgeRendersOnPart(t *testing.T) {
	dir := t.TempDir()
	makeExtendTutorial(t, dir, "test-tut", store.StatusSkipped, []string{"part-01.md"})
	srv := serve.NewServer(dir)

	req := httptest.NewRequest(http.MethodGet, "/test-tut/part-01.md", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if !strings.Contains(w.Body.String(), `badge skipped`) {
		t.Error("part page missing skipped badge for tutorial with status=skipped")
	}
}

func TestSkippedBadgeRendersOnList(t *testing.T) {
	dir := t.TempDir()
	makeExtendTutorial(t, dir, "test-tut", store.StatusSkipped, []string{"part-01.md"})
	srv := serve.NewServer(dir)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if !strings.Contains(w.Body.String(), `badge skipped`) {
		t.Error("list page missing skipped badge for tutorial with status=skipped")
	}
}

func TestUnverifiedRendersNoBadge(t *testing.T) {
	dir := t.TempDir()
	makeExtendTutorial(t, dir, "test-tut", store.StatusUnverified, []string{"part-01.md"})
	srv := serve.NewServer(dir)

	req := httptest.NewRequest(http.MethodGet, "/test-tut/part-01.md", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	body := w.Body.String()
	// No status badge span should render for the calm unverified state.
	if strings.Contains(body, `class="badge `) {
		t.Error("unverified tutorial should render no status badge")
	}
}

func TestFailurePanelRendersFromVerifyResult(t *testing.T) {
	dir := t.TempDir()
	tutDir := makeExtendTutorial(t, dir, "test-tut", store.StatusFailed, []string{"part-01.md"})
	if err := store.WriteVerifyResult(tutDir, &store.VerifyResult{
		Status:     store.StatusFailed,
		Part:       "part-01.md",
		FailedStep: 3,
		Error:      "zig: command failed with exit code 1",
	}); err != nil {
		t.Fatal(err)
	}
	srv := serve.NewServer(dir)

	req := httptest.NewRequest(http.MethodGet, "/test-tut/part-01.md", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET part = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "verify-failure") {
		t.Error("failed tutorial should render the verify-failure panel")
	}
	if !strings.Contains(body, "part-01.md") {
		t.Error("failure panel should name the failing part")
	}
	if !strings.Contains(body, "zig: command failed with exit code 1") {
		t.Error("failure panel should show the recorded error")
	}
}
