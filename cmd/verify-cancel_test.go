package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
)

func TestVerifyCancelFallsBackToUnverified(t *testing.T) {
	homeDir := t.TempDir()
	setHome(t, homeDir)
	tutDir := writeTutorial(t, homeDir, "test-slug", store.StatusVerifying, []string{"part-01.md"})

	if err := verifyCancelCmd.RunE(verifyCancelCmd, []string{"test-slug"}); err != nil {
		t.Fatalf("verify-cancel: %v", err)
	}

	got, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusUnverified {
		t.Errorf("Status = %q, want unverified (no prior result to restore)", got.Status)
	}
	if _, err := os.Stat(filepath.Join(tutDir, "verify-result.json")); !os.IsNotExist(err) {
		t.Error("cancel should not create a verify-result.json")
	}
}

func TestVerifyCancelRestoresPriorResult(t *testing.T) {
	homeDir := t.TempDir()
	setHome(t, homeDir)
	tutDir := writeTutorial(t, homeDir, "test-slug", store.StatusVerifying, []string{"part-01.md"})
	prior := &store.VerifyResult{Status: store.StatusVerified, CheckedAt: "2026-06-01T00:00:00Z"}
	if err := store.WriteVerifyResult(tutDir, prior); err != nil {
		t.Fatal(err)
	}

	if err := verifyCancelCmd.RunE(verifyCancelCmd, []string{"test-slug"}); err != nil {
		t.Fatalf("verify-cancel: %v", err)
	}

	got, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusVerified {
		t.Errorf("Status = %q, want verified restored from prior result", got.Status)
	}
	vr, err := store.ReadVerifyResult(tutDir)
	if err != nil {
		t.Fatalf("ReadVerifyResult: %v", err)
	}
	if vr.Status != store.StatusVerified || vr.CheckedAt != prior.CheckedAt {
		t.Errorf("VerifyResult = %+v, want prior result untouched", vr)
	}
}

func TestVerifyCancelRejectsWhenNotVerifying(t *testing.T) {
	homeDir := t.TempDir()
	setHome(t, homeDir)
	for _, status := range []store.Status{store.StatusUnverified, store.StatusVerified, store.StatusExtending} {
		slug := "slug-" + string(status)
		tutDir := writeTutorial(t, homeDir, slug, status, []string{"part-01.md"})

		if err := verifyCancelCmd.RunE(verifyCancelCmd, []string{slug}); err == nil {
			t.Errorf("verify-cancel should reject a tutorial with status %q", status)
		}

		got, err := store.ReadMetadata(tutDir)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != status {
			t.Errorf("Status = %q, want %q unchanged after rejected cancel", got.Status, status)
		}
	}
}

func TestVerifyCancelRejectsMissingTutorial(t *testing.T) {
	setHome(t, t.TempDir())
	if err := verifyCancelCmd.RunE(verifyCancelCmd, []string{"no-such-slug"}); err == nil {
		t.Error("verify-cancel should error on a missing tutorial")
	}
}

func TestVerifyCancelRejectsBadSlug(t *testing.T) {
	setHome(t, t.TempDir())
	for _, slug := range []string{"", ".", "..", "foo/bar", `foo\bar`} {
		if err := verifyCancelCmd.RunE(verifyCancelCmd, []string{slug}); err == nil {
			t.Errorf("verify-cancel %q should be rejected as invalid", slug)
		}
	}
}

// With `lathe serve --agent` running the verification, verify-cancel asks the
// server to stop it; the server restores the status, and the command reports
// it rather than erroring that nothing is verifying.
func TestVerifyCancelStopsHostedRun(t *testing.T) {
	homeDir := t.TempDir()
	setHome(t, homeDir)
	tutDir := writeTutorial(t, homeDir, "test-slug", store.StatusVerifying, []string{"part-01.md"})

	var hit string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = r.Method + " " + r.URL.Path
		// What the server does once its agent has wound down.
		if _, err := store.CancelVerify(tutDir); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	if err := config.WriteServeRuntime(&config.ServeRuntime{URL: srv.URL}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	verifyCancelCmd.SetOut(&out)
	defer verifyCancelCmd.SetOut(nil)
	if err := verifyCancelCmd.RunE(verifyCancelCmd, []string{"test-slug"}); err != nil {
		t.Fatalf("verify-cancel: %v", err)
	}
	if hit != "POST /-/verify/test-slug/cancel" {
		t.Errorf("server got %q", hit)
	}
	if !strings.Contains(out.String(), "Stopped the hosted agent's verification") {
		t.Errorf("output = %q", out.String())
	}
	if got, _ := store.ReadMetadata(tutDir); got.Status != store.StatusUnverified {
		t.Errorf("Status = %q, want unverified", got.Status)
	}
}

// A server with no hosted run for the slug (404) leaves verify-cancel to
// reset the status itself, as before.
func TestVerifyCancelWithServerButNoHostedRun(t *testing.T) {
	homeDir := t.TempDir()
	setHome(t, homeDir)
	tutDir := writeTutorial(t, homeDir, "test-slug", store.StatusVerifying, []string{"part-01.md"})
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	if err := config.WriteServeRuntime(&config.ServeRuntime{URL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	if err := verifyCancelCmd.RunE(verifyCancelCmd, []string{"test-slug"}); err != nil {
		t.Fatalf("verify-cancel: %v", err)
	}
	if got, _ := store.ReadMetadata(tutDir); got.Status != store.StatusUnverified {
		t.Errorf("Status = %q, want unverified", got.Status)
	}
}
