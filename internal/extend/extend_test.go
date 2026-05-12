package extend_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devenjarvis/lathe/internal/extend"
	"github.com/devenjarvis/lathe/internal/store"
)

func makeTutorialDir(t *testing.T, parts []string) string {
	t.Helper()
	dir := t.TempDir()
	tut := &store.Tutorial{
		Slug:   "test-tut",
		Title:  "Test Tutorial",
		Status: store.StatusVerified,
		Parts:  parts,
	}
	for _, p := range parts {
		if err := os.WriteFile(filepath.Join(dir, p), []byte("# "+p), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.WriteMetadata(dir, tut); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestNextPartFilename(t *testing.T) {
	cases := []struct {
		parts []string
		want  string
	}{
		{nil, "part-01.md"},
		{[]string{}, "part-01.md"},
		{[]string{"part-01.md"}, "part-02.md"},
		{[]string{"part-01.md", "part-02.md"}, "part-03.md"},
		{[]string{"part-01.md", "part-02.md", "part-03.md"}, "part-04.md"},
	}
	for _, tc := range cases {
		got := extend.NextPartFilename(tc.parts)
		if got != tc.want {
			t.Errorf("NextPartFilename(%v) = %q, want %q", tc.parts, got, tc.want)
		}
	}
}

func TestSpawnExtenderWritesExtendingMetadataBeforeSpawn(t *testing.T) {
	tutDir := makeTutorialDir(t, []string{"part-01.md"})
	// Block claude so SpawnExtender fails fast, but metadata should already be written
	t.Setenv("PATH", "")

	// Intentionally ignoring error — claude won't be found
	extend.SpawnExtender("test-tut", tutDir, "")

	got, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatalf("metadata not written before spawn: %v", err)
	}
	if got.Status != store.StatusExtending {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusExtending)
	}
	if got.PendingPart != "part-02.md" {
		t.Errorf("PendingPart = %q, want %q", got.PendingPart, "part-02.md")
	}
}

func TestSpawnExtenderRejectsConcurrent(t *testing.T) {
	tutDir := makeTutorialDir(t, []string{"part-01.md"})
	// Set status to extending manually
	tut, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatal(err)
	}
	tut.Status = store.StatusExtending
	tut.PendingPart = "part-02.md"
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}

	err = extend.SpawnExtender("test-tut", tutDir, "")
	if err == nil {
		t.Fatal("SpawnExtender() should return error when already extending")
	}
	if !strings.Contains(err.Error(), "already extending") {
		t.Errorf("error should mention 'already extending', got: %v", err)
	}
}

func TestSpawnExtenderRejectsWhileVerifying(t *testing.T) {
	tutDir := makeTutorialDir(t, []string{"part-01.md"})
	tut, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatal(err)
	}
	tut.Status = store.StatusVerifying
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}

	err = extend.SpawnExtender("test-tut", tutDir, "")
	if err == nil {
		t.Fatal("SpawnExtender() should return error when verifying")
	}
	if !strings.Contains(err.Error(), "already extending") {
		t.Errorf("error should mention 'already extending', got: %v", err)
	}
}
