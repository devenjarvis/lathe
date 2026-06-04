package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devenjarvis/lathe/internal/store"
)

func TestExtendCommitAppendsPart(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	tutDir := writeTutorial(t, homeDir, "test-slug", store.StatusExtending, []string{"part-01.md"})
	// The skill wrote the new part before committing.
	if err := os.WriteFile(filepath.Join(tutDir, "part-02.md"), []byte("# Part 2"), 0644); err != nil {
		t.Fatal(err)
	}
	// Reflect the in-flight pending marker that extend-start would have set.
	tut, _ := store.ReadMetadata(tutDir)
	tut.PendingPart = "part-02.md"
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}

	if err := extendCommitCmd.RunE(extendCommitCmd, []string{"test-slug", "part-02.md"}); err != nil {
		t.Fatalf("extend-commit: %v", err)
	}

	got, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Parts) != 2 || got.Parts[1] != "part-02.md" {
		t.Errorf("Parts = %v, want [part-01.md part-02.md]", got.Parts)
	}
	if got.PendingPart != "" {
		t.Errorf("PendingPart = %q, want cleared", got.PendingPart)
	}
	if got.Status != store.StatusUnverified {
		t.Errorf("Status = %q, want unverified", got.Status)
	}
}

func TestExtendCommitIsIdempotent(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	tutDir := writeTutorial(t, homeDir, "test-slug", store.StatusExtending, []string{"part-01.md"})
	if err := os.WriteFile(filepath.Join(tutDir, "part-02.md"), []byte("# Part 2"), 0644); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		if err := extendCommitCmd.RunE(extendCommitCmd, []string{"test-slug", "part-02.md"}); err != nil {
			t.Fatalf("extend-commit run %d: %v", i, err)
		}
	}
	got, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Parts) != 2 {
		t.Errorf("Parts = %v, want no duplicate append", got.Parts)
	}
}

func TestExtendCommitRejectsMissingFile(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	writeTutorial(t, homeDir, "test-slug", store.StatusExtending, []string{"part-01.md"})

	if err := extendCommitCmd.RunE(extendCommitCmd, []string{"test-slug", "part-99.md"}); err == nil {
		t.Error("extend-commit should reject a part file that does not exist")
	}
}
