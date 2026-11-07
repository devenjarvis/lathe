package cmd

import (
	"testing"

	"github.com/devenjarvis/lathe/internal/store"
)

// resetTagFlags restores the package-level flag vars between cases, since cobra
// binds flags to shared package state.
func resetTagFlags(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		tagSet = nil
		tagAdd = nil
		tagRemove = nil
	})
}

func tagsOf(t *testing.T, tutDir string) []string {
	t.Helper()
	tut, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatal(err)
	}
	return tut.Tags
}

func sameTags(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestTagSetReplacesAndNormalizes(t *testing.T) {
	homeDir := t.TempDir()
	setHome(t, homeDir)
	tutDir := writeTutorial(t, homeDir, "test-slug", store.StatusUnverified, []string{"part-01.md"})

	resetTagFlags(t)
	tagSet = []string{"Rust,audio", " rust ", "dsp"}
	if err := tagCmd.RunE(tagCmd, []string{"test-slug"}); err != nil {
		t.Fatalf("tag --set: %v", err)
	}
	if got, want := tagsOf(t, tutDir), []string{"rust", "audio", "dsp"}; !sameTags(got, want) {
		t.Errorf("tags = %v, want %v", got, want)
	}
}

func TestTagAddAndRemove(t *testing.T) {
	homeDir := t.TempDir()
	setHome(t, homeDir)
	tutDir := writeTutorial(t, homeDir, "test-slug", store.StatusUnverified, []string{"part-01.md"})

	resetTagFlags(t)
	tagSet = []string{"rust", "cli"}
	if err := tagCmd.RunE(tagCmd, []string{"test-slug"}); err != nil {
		t.Fatalf("tag --set: %v", err)
	}

	resetTagFlags(t)
	tagAdd = []string{"parsing"}
	tagRemove = []string{"cli"}
	if err := tagCmd.RunE(tagCmd, []string{"test-slug"}); err != nil {
		t.Fatalf("tag --add/--remove: %v", err)
	}
	if got, want := tagsOf(t, tutDir), []string{"rust", "parsing"}; !sameTags(got, want) {
		t.Errorf("tags = %v, want %v", got, want)
	}
}

func TestTagRequiresAnAction(t *testing.T) {
	homeDir := t.TempDir()
	setHome(t, homeDir)
	writeTutorial(t, homeDir, "test-slug", store.StatusUnverified, []string{"part-01.md"})

	resetTagFlags(t)
	if err := tagCmd.RunE(tagCmd, []string{"test-slug"}); err == nil {
		t.Error("tag with no --set/--add/--remove should error")
	}
}
