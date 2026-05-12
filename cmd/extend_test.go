package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devenjarvis/lathe/internal/store"
)

func TestExtendCommandFlipsMetadataToExtending(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("PATH", "") // prevent claude from being found

	// Create a minimal tutorial under ~/.lathe/tutorials/test-slug/
	tutDir := filepath.Join(homeDir, ".lathe", "tutorials", "test-slug")
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tutDir, "part-01.md"), []byte("# Part 1"), 0644); err != nil {
		t.Fatal(err)
	}
	tut := &store.Tutorial{
		Slug:   "test-slug",
		Title:  "Test Slug",
		Status: store.StatusVerified,
		Parts:  []string{"part-01.md"},
	}
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}

	// Run the command directly; PATH="" means SpawnExtender will fail fast but
	// metadata flip happens before spawn.
	extendCmd.Flags().Set("guidance", "") //nolint:errcheck
	extendCmd.RunE(extendCmd, []string{"test-slug"})

	got, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatalf("ReadMetadata after extend: %v", err)
	}
	if got.Status != store.StatusExtending {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusExtending)
	}
	if got.PendingPart != "part-02.md" {
		t.Errorf("PendingPart = %q, want %q", got.PendingPart, "part-02.md")
	}
}
