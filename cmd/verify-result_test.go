package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devenjarvis/lathe/internal/store"
)

// resetVerifyResultFlags restores the package-level flag vars between cases,
// since cobra binds flags to shared package state.
func resetVerifyResultFlags(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		verifyResultStatus = ""
		verifyResultPart = ""
		verifyResultFailedStep = 0
		verifyResultError = ""
		verifyResultCheckedAt = ""
		verifyResultRepoCommit = ""
	})
}

func TestVerifyResultRecordsVerified(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	tutDir := writeTutorial(t, homeDir, "test-slug", store.StatusVerifying, []string{"part-01.md"})

	resetVerifyResultFlags(t)
	verifyResultStatus = "verified"
	if err := verifyResultCmd.RunE(verifyResultCmd, []string{"test-slug"}); err != nil {
		t.Fatalf("verify-result: %v", err)
	}

	got, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusVerified {
		t.Errorf("Status = %q, want verified", got.Status)
	}
	vr, err := store.ReadVerifyResult(tutDir)
	if err != nil {
		t.Fatalf("ReadVerifyResult: %v", err)
	}
	if vr.Status != store.StatusVerified {
		t.Errorf("VerifyResult.Status = %q, want verified", vr.Status)
	}
	if vr.CheckedAt == "" {
		t.Error("CheckedAt should default to now, got empty")
	}
}

func TestVerifyResultRecordsFailure(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	tutDir := writeTutorial(t, homeDir, "test-slug", store.StatusVerifying, []string{"part-01.md", "part-02.md"})

	resetVerifyResultFlags(t)
	verifyResultStatus = "failed"
	verifyResultPart = "part-02.md"
	verifyResultFailedStep = 3
	verifyResultError = "zig: command failed"
	if err := verifyResultCmd.RunE(verifyResultCmd, []string{"test-slug"}); err != nil {
		t.Fatalf("verify-result: %v", err)
	}

	vr, err := store.ReadVerifyResult(tutDir)
	if err != nil {
		t.Fatalf("ReadVerifyResult: %v", err)
	}
	if vr.Status != store.StatusFailed || vr.Part != "part-02.md" || vr.FailedStep != 3 {
		t.Errorf("VerifyResult = %+v, want failed at part-02.md step 3", vr)
	}
	if vr.Error != "zig: command failed" {
		t.Errorf("Error = %q, want recorded message", vr.Error)
	}
}

func TestVerifyResultVerifyingDoesNotWriteResultFile(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	tutDir := writeTutorial(t, homeDir, "test-slug", store.StatusUnverified, []string{"part-01.md"})

	resetVerifyResultFlags(t)
	verifyResultStatus = "verifying"
	if err := verifyResultCmd.RunE(verifyResultCmd, []string{"test-slug"}); err != nil {
		t.Fatalf("verify-result: %v", err)
	}

	got, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusVerifying {
		t.Errorf("Status = %q, want verifying", got.Status)
	}
	if _, err := os.Stat(filepath.Join(tutDir, "verify-result.json")); !os.IsNotExist(err) {
		t.Error("verifying should not write a verify-result.json")
	}
}

func TestVerifyResultVerifyingRejectsWhileExtending(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	writeTutorial(t, homeDir, "test-slug", store.StatusExtending, []string{"part-01.md"})

	resetVerifyResultFlags(t)
	verifyResultStatus = "verifying"
	if err := verifyResultCmd.RunE(verifyResultCmd, []string{"test-slug"}); err == nil {
		t.Error("verify-result --status verifying should reject a tutorial that is extending")
	}
}

func TestVerifyResultRejectsBadStatus(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	writeTutorial(t, homeDir, "test-slug", store.StatusUnverified, []string{"part-01.md"})

	resetVerifyResultFlags(t)
	verifyResultStatus = "bogus"
	if err := verifyResultCmd.RunE(verifyResultCmd, []string{"test-slug"}); err == nil {
		t.Error("verify-result should reject an unknown --status")
	}
}

func TestVerifyResultRePinsRepoCommit(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	tutDir := filepath.Join(homeDir, ".lathe", "tutorials", "onboard")
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	old := "1111111111111111111111111111111111111111"
	tut := &store.Tutorial{
		Slug:       "onboard",
		Status:     store.StatusStale,
		Kind:       store.KindOnboarding,
		Repo:       "github.com/devenjarvis/lathe",
		RepoCommit: old,
		RepoPath:   "/tmp/lathe",
	}
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}

	resetVerifyResultFlags(t)
	verifyResultStatus = "verified"
	verifyResultRepoCommit = "  2222222222222222222222222222222222222222 "
	if err := verifyResultCmd.RunE(verifyResultCmd, []string{"onboard"}); err != nil {
		t.Fatalf("verify-result: %v", err)
	}

	got, _ := store.ReadMetadata(tutDir)
	if got.RepoCommit != "2222222222222222222222222222222222222222" {
		t.Errorf("RepoCommit = %q, want the re-pinned SHA", got.RepoCommit)
	}
	if got.Status != store.StatusVerified {
		t.Errorf("Status = %q, want verified", got.Status)
	}
}

func TestVerifyResultLeavesRepoCommitAloneWithoutTheFlag(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	tutDir := filepath.Join(homeDir, ".lathe", "tutorials", "onboard")
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	old := "1111111111111111111111111111111111111111"
	if err := store.WriteMetadata(tutDir, &store.Tutorial{
		Slug: "onboard", Status: store.StatusUnverified,
		Kind: store.KindOnboarding, Repo: "github.com/devenjarvis/lathe",
		RepoCommit: old, RepoPath: "/tmp/lathe",
	}); err != nil {
		t.Fatal(err)
	}

	resetVerifyResultFlags(t)
	verifyResultStatus = "verified"
	if err := verifyResultCmd.RunE(verifyResultCmd, []string{"onboard"}); err != nil {
		t.Fatalf("verify-result: %v", err)
	}
	got, _ := store.ReadMetadata(tutDir)
	if got.RepoCommit != old {
		t.Errorf("RepoCommit = %q, want it unchanged at %q", got.RepoCommit, old)
	}
}
