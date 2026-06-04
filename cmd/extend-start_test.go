package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devenjarvis/lathe/internal/store"
)

func TestExtendStartReservesNextPart(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	tutDir := writeTutorial(t, homeDir, "test-slug", store.StatusVerified, []string{"part-01.md"})

	var out bytes.Buffer
	extendStartCmd.SetOut(&out)
	t.Cleanup(func() { extendStartCmd.SetOut(nil) })
	if err := extendStartCmd.RunE(extendStartCmd, []string{"test-slug"}); err != nil {
		t.Fatalf("extend-start: %v", err)
	}

	if got := strings.TrimSpace(out.String()); got != "part-02.md" {
		t.Errorf("output = %q, want the target filename part-02.md", got)
	}

	tut, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatal(err)
	}
	if tut.Status != store.StatusExtending {
		t.Errorf("Status = %q, want extending", tut.Status)
	}
	if tut.PendingPart != "part-02.md" {
		t.Errorf("PendingPart = %q, want part-02.md", tut.PendingPart)
	}
}

func TestExtendStartPromotesLegacyIndex(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	// Legacy single-part tutorial: index.md, no parts list.
	tutDir := filepath.Join(homeDir, ".lathe", "tutorials", "legacy")
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tutDir, "index.md"), []byte("# Index"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteMetadata(tutDir, &store.Tutorial{Slug: "legacy", Status: store.StatusVerified}); err != nil {
		t.Fatal(err)
	}

	if err := extendStartCmd.RunE(extendStartCmd, []string{"legacy"}); err != nil {
		t.Fatalf("extend-start: %v", err)
	}

	tut, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatal(err)
	}
	// index.md promoted to part-01.md, so the reserved part is part-02.md.
	if tut.PendingPart != "part-02.md" {
		t.Errorf("PendingPart = %q, want part-02.md after promoting index.md", tut.PendingPart)
	}
}

func TestExtendStartRejectsWhileBusy(t *testing.T) {
	for _, status := range []store.Status{store.StatusExtending, store.StatusVerifying} {
		t.Run(string(status), func(t *testing.T) {
			homeDir := t.TempDir()
			t.Setenv("HOME", homeDir)
			writeTutorial(t, homeDir, "test-slug", status, []string{"part-01.md"})
			if err := extendStartCmd.RunE(extendStartCmd, []string{"test-slug"}); err == nil {
				t.Errorf("extend-start should reject a tutorial that is %q", status)
			}
		})
	}
}
