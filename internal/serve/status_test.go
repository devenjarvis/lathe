package serve_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/devenjarvis/lathe/internal/serve"
	"github.com/devenjarvis/lathe/internal/store"
)

// statusResp mirrors the JSON handleStatus emits.
type statusResp struct {
	Status string `json:"status"`
	Done   bool   `json:"done"`
	Badge  string `json:"badge"`
	Verify string `json:"verify"`
	Extend string `json:"extend"`
}

func getStatus(t *testing.T, srv *serve.Server, path string) (*httptest.ResponseRecorder, statusResp) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	var resp statusResp
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode status JSON: %v (body=%q)", err, w.Body.String())
		}
	}
	return w, resp
}

// While verifying, the endpoint reports the tutorial is still in flight so the
// client keeps polling and does not swap anything.
func TestStatusVerifyingNotDone(t *testing.T) {
	dir := t.TempDir()
	makeExtendTutorial(t, dir, "test-tut", store.StatusVerifying, []string{"part-01.md"})
	srv := serve.NewServer(dir)

	w, resp := getStatus(t, srv, "/-/status/test-tut/part-01.md?from=verifying")
	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", w.Code)
	}
	if resp.Done {
		t.Error("done should be false while verifying")
	}
	if resp.Status != string(store.StatusVerifying) {
		t.Errorf("status = %q, want verifying", resp.Status)
	}
	if !strings.Contains(resp.Verify, "verifying-panel") {
		t.Errorf("verify region should still show the verifying panel, got %q", resp.Verify)
	}
}

// When verification succeeds, the endpoint reports done, the badge flips to
// "Verified" with the recorded date, and the verify region empties (no form).
func TestStatusVerifiedDone(t *testing.T) {
	dir := t.TempDir()
	tutDir := makeExtendTutorial(t, dir, "test-tut", store.StatusVerified, []string{"part-01.md"})
	if err := store.WriteVerifyResult(tutDir, &store.VerifyResult{
		Status:    store.StatusVerified,
		CheckedAt: "2026-06-03T12:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	srv := serve.NewServer(dir)

	w, resp := getStatus(t, srv, "/-/status/test-tut/part-01.md?from=verifying")
	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", w.Code)
	}
	if !resp.Done {
		t.Error("done should be true once verified")
	}
	if !strings.Contains(resp.Badge, "Verified") {
		t.Errorf("badge should read Verified, got %q", resp.Badge)
	}
	if !strings.Contains(resp.Badge, "Jun 3, 2026") {
		t.Errorf("badge region should carry the verified date, got %q", resp.Badge)
	}
	if strings.Contains(resp.Verify, "verifyForm") || strings.Contains(resp.Verify, "verifying-panel") {
		t.Errorf("verified verify region should be empty, got %q", resp.Verify)
	}
}

// A failed verification surfaces the failure callout (with the recorded detail)
// and a re-verify form that the poller can re-wire.
func TestStatusFailedShowsCalloutAndForm(t *testing.T) {
	dir := t.TempDir()
	tutDir := makeExtendTutorial(t, dir, "test-tut", store.StatusFailed, []string{"part-01.md"})
	if err := store.WriteVerifyResult(tutDir, &store.VerifyResult{
		Status:     store.StatusFailed,
		Part:       "part-01.md",
		FailedStep: 2,
		Error:      "boom",
		CheckedAt:  "2026-06-03T12:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	srv := serve.NewServer(dir)

	w, resp := getStatus(t, srv, "/-/status/test-tut/part-01.md?from=verifying")
	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", w.Code)
	}
	if !resp.Done {
		t.Error("done should be true once failed")
	}
	if !strings.Contains(resp.Verify, "verify-failure") {
		t.Errorf("verify region should include the failure callout, got %q", resp.Verify)
	}
	if !strings.Contains(resp.Verify, "boom") {
		t.Errorf("failure callout should include the recorded error, got %q", resp.Verify)
	}
	if !strings.Contains(resp.Verify, `id="verifyForm"`) {
		t.Errorf("failed verify region should include a re-verify form, got %q", resp.Verify)
	}
}

// When an extend finishes, polling the (formerly last) part with from=extending
// returns a "ready" link to the newly appended part instead of the form.
func TestStatusExtendCompletionShowsReadyLink(t *testing.T) {
	dir := t.TempDir()
	// extend-commit appends the new part and flips status off "extending" before
	// the poll arrives, so the tutorial now has part-02 and is unverified.
	makeExtendTutorial(t, dir, "test-tut", store.StatusUnverified, []string{"part-01.md", "part-02.md"})
	srv := serve.NewServer(dir)

	w, resp := getStatus(t, srv, "/-/status/test-tut/part-01.md?from=extending")
	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", w.Code)
	}
	if !resp.Done {
		t.Error("done should be true once the extend completed")
	}
	if !strings.Contains(resp.Extend, "Part 2 is ready") {
		t.Errorf("extend region should announce the new part, got %q", resp.Extend)
	}
	if !strings.Contains(resp.Extend, `href="/test-tut/part-02.md"`) {
		t.Errorf("extend region should link to the new part, got %q", resp.Extend)
	}
}

// A verify poll on a middle part must never produce a spurious "ready" link —
// the ready link is gated on from=extending.
func TestStatusVerifyPollNoReadyLink(t *testing.T) {
	dir := t.TempDir()
	makeExtendTutorial(t, dir, "test-tut", store.StatusUnverified, []string{"part-01.md", "part-02.md"})
	srv := serve.NewServer(dir)

	_, resp := getStatus(t, srv, "/-/status/test-tut/part-01.md?from=verifying")
	if strings.Contains(resp.Extend, "is ready") {
		t.Errorf("verify poll on a middle part should not show a ready link, got %q", resp.Extend)
	}
	if resp.Extend != "" {
		t.Errorf("extend region should be empty for a non-last part, got %q", resp.Extend)
	}
}

// A skipped tutorial records a CheckedAt date, but the provenance line must not
// mislabel it as "Verified" — skipped means verification did not run.
func TestStatusSkippedDateLabel(t *testing.T) {
	dir := t.TempDir()
	tutDir := makeExtendTutorial(t, dir, "test-tut", store.StatusSkipped, []string{"part-01.md"})
	if err := store.WriteVerifyResult(tutDir, &store.VerifyResult{
		Status:    store.StatusSkipped,
		CheckedAt: "2026-06-03T12:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	srv := serve.NewServer(dir)

	_, resp := getStatus(t, srv, "/-/status/test-tut/part-01.md?from=verifying")
	if !strings.Contains(resp.Badge, "Skipped Jun 3, 2026") {
		t.Errorf("skipped date should be labeled Skipped, got %q", resp.Badge)
	}
	if strings.Contains(resp.Badge, "Verified Jun 3, 2026") {
		t.Errorf("skipped badge must not label its date as Verified, got %q", resp.Badge)
	}
}

func TestStatusUnknownPartIs404(t *testing.T) {
	dir := t.TempDir()
	makeExtendTutorial(t, dir, "test-tut", store.StatusVerifying, []string{"part-01.md"})
	srv := serve.NewServer(dir)

	for _, path := range []string{
		"/-/status/test-tut/part-99.md",
		"/-/status/test-tut/metadata.json",
		"/-/status/does-not-exist/part-01.md",
	} {
		w, _ := getStatus(t, srv, path)
		if w.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, w.Code)
		}
	}
}
