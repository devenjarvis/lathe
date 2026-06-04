package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devenjarvis/lathe/internal/store"
)

func writeTutorial(t *testing.T, homeDir, slug string, status store.Status, parts []string) string {
	t.Helper()
	tutDir := filepath.Join(homeDir, ".lathe", "tutorials", slug)
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, p := range parts {
		if err := os.WriteFile(filepath.Join(tutDir, p), []byte("# "+p), 0644); err != nil {
			t.Fatal(err)
		}
	}
	tut := &store.Tutorial{
		Slug:   slug,
		Title:  store.SlugToTitle(slug),
		Status: status,
		Parts:  parts,
	}
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}
	return tutDir
}

func TestVerifyCommandPrintsHandoff(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	tutDir := writeTutorial(t, homeDir, "test-slug", store.StatusUnverified, []string{"part-01.md"})

	var out bytes.Buffer
	verifyCmd.SetOut(&out)
	t.Cleanup(func() { verifyCmd.SetOut(nil) })
	if err := verifyCmd.RunE(verifyCmd, []string{"test-slug"}); err != nil {
		t.Fatalf("verify: %v", err)
	}

	if !strings.Contains(out.String(), "/lathe-verify test-slug") {
		t.Errorf("output = %q, want the /lathe-verify handoff command", out.String())
	}

	// Handoff must not touch durable state — the skill does that.
	got, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if got.Status != store.StatusUnverified {
		t.Errorf("Status = %q, want %q (handoff must not change status)", got.Status, store.StatusUnverified)
	}
}

func TestVerifyCommandRejectsBadSlug(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, slug := range []string{"", ".", "..", "foo/bar", `foo\bar`} {
		if err := verifyCmd.RunE(verifyCmd, []string{slug}); err == nil {
			t.Errorf("verify %q should be rejected as invalid", slug)
		}
	}
}
