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

func TestExtendCommitAppendsSources(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	tutDir := writeTutorial(t, homeDir, "test-slug", store.StatusExtending, []string{"part-01.md"})
	if err := os.WriteFile(filepath.Join(tutDir, "part-02.md"), []byte("# Part 2"), 0644); err != nil {
		t.Fatal(err)
	}
	// Seed an existing research trail; the new part adds one fresh source plus a
	// duplicate of an existing one.
	tut, _ := store.ReadMetadata(tutDir)
	tut.Sources = []string{"https://a.com/spec"}
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}

	extendCommitSources = []string{"https://a.com/spec", "https://b.com/paper"}
	t.Cleanup(func() { extendCommitSources = nil })

	if err := extendCommitCmd.RunE(extendCommitCmd, []string{"test-slug", "part-02.md"}); err != nil {
		t.Fatalf("extend-commit: %v", err)
	}

	got, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://a.com/spec", "https://b.com/paper"}
	if len(got.Sources) != len(want) {
		t.Fatalf("Sources = %v, want %v (de-duped union)", got.Sources, want)
	}
	for i := range want {
		if got.Sources[i] != want[i] {
			t.Errorf("Sources[%d] = %q, want %q", i, got.Sources[i], want[i])
		}
	}
}

func TestExtendCommitUpdatesModel(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	tutDir := writeTutorial(t, homeDir, "test-slug", store.StatusExtending, []string{"part-01.md"})
	if err := os.WriteFile(filepath.Join(tutDir, "part-02.md"), []byte("# Part 2"), 0644); err != nil {
		t.Fatal(err)
	}
	// Seed an existing model label; re-extending in a newer model should refresh it.
	tut, _ := store.ReadMetadata(tutDir)
	tut.Model = "Claude Opus 4.8"
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}

	extendCommitModel = "  Claude Sonnet 4.6  "
	t.Cleanup(func() { extendCommitModel = "" })

	if err := extendCommitCmd.RunE(extendCommitCmd, []string{"test-slug", "part-02.md"}); err != nil {
		t.Fatalf("extend-commit: %v", err)
	}
	got, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "Claude Sonnet 4.6" { // trimmed, last-writer-wins
		t.Errorf("Model = %q, want %q", got.Model, "Claude Sonnet 4.6")
	}
}

func TestExtendCommitOmittedModelLeavesExisting(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	tutDir := writeTutorial(t, homeDir, "test-slug", store.StatusExtending, []string{"part-01.md"})
	if err := os.WriteFile(filepath.Join(tutDir, "part-02.md"), []byte("# Part 2"), 0644); err != nil {
		t.Fatal(err)
	}
	tut, _ := store.ReadMetadata(tutDir)
	tut.Model = "Claude Opus 4.8"
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}

	extendCommitModel = "" // omitted
	if err := extendCommitCmd.RunE(extendCommitCmd, []string{"test-slug", "part-02.md"}); err != nil {
		t.Fatalf("extend-commit: %v", err)
	}
	got, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "Claude Opus 4.8" {
		t.Errorf("Model = %q, want existing %q untouched", got.Model, "Claude Opus 4.8")
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
