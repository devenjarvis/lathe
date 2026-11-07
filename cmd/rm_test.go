package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
)

func runRm(t *testing.T, stdin string, now, force bool, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	rmCmd.SetOut(&out)
	rmCmd.SetErr(&out)
	rmCmd.SetIn(strings.NewReader(stdin))
	rmNow, rmForce = now, force
	t.Cleanup(func() {
		rmCmd.SetOut(nil)
		rmCmd.SetErr(nil)
		rmCmd.SetIn(nil)
		rmNow, rmForce = false, false
	})
	err := rmCmd.RunE(rmCmd, args)
	return out.String(), err
}

func TestRmMovesToTrashAndKeepsShelf(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	tutDir := writeTutorial(t, home, "synth", store.StatusUnverified, []string{"part-01.md"})
	path, _ := store.DefaultShelvesPath()
	if _, err := store.UpdateShelves(path, func(s *store.Shelves) error {
		sh, _ := s.Create("Audio")
		return s.Assign("synth", sh.ID)
	}); err != nil {
		t.Fatal(err)
	}

	// Undoable, so no prompt.
	out, err := runRm(t, "", false, false, "synth")
	if err != nil || !strings.Contains(out, "Moved synth to the Trash") {
		t.Fatalf("rm = %q, %v", out, err)
	}
	if _, err := os.Stat(tutDir); !os.IsNotExist(err) {
		t.Error("tutorial still in the library")
	}
	dir := filepath.Dir(tutDir)
	list, _ := store.ListTrash(dir)
	if len(list) != 1 || list[0].Slug != "synth" {
		t.Fatalf("Trash = %+v", list)
	}
	if s, _ := store.ReadShelves(path); s.ShelfOf("synth").Name != "Audio" {
		t.Error("rm dropped the shelf placement (a restore should keep it)")
	}
	if _, err := runRm(t, "", false, false, "synth"); err == nil {
		t.Error("rm of a missing tutorial succeeded")
	}
}

func TestRmNowDeletesPermanently(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	tutDir := writeTutorial(t, home, "kv", store.StatusUnverified, []string{"part-01.md"})
	path, _ := store.DefaultShelvesPath()
	if _, err := store.UpdateShelves(path, func(s *store.Shelves) error {
		sh, _ := s.Create("Systems")
		return s.Assign("kv", sh.ID)
	}); err != nil {
		t.Fatal(err)
	}

	out, err := runRm(t, "n\n", true, false, "kv")
	if err != nil || !strings.Contains(out, "aborted") {
		t.Fatalf("declined rm --now = %q, %v", out, err)
	}
	if _, err := os.Stat(tutDir); err != nil {
		t.Fatal("declined rm --now deleted the tutorial")
	}
	out, err = runRm(t, "y\n", true, false, "kv")
	if err != nil || !strings.Contains(out, "Deleted kv permanently") {
		t.Fatalf("rm --now = %q, %v", out, err)
	}
	if _, err := os.Stat(tutDir); !os.IsNotExist(err) {
		t.Error("rm --now left the tutorial")
	}
	if list, _ := store.ListTrash(filepath.Dir(tutDir)); len(list) != 0 {
		t.Errorf("rm --now used the Trash: %+v", list)
	}
	if s, _ := store.ReadShelves(path); len(s.Placements) != 0 {
		t.Errorf("rm --now kept the placement: %v", s.Placements)
	}
}

func TestRmPurgesExpiredTrash(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	writeTutorial(t, home, "old", store.StatusUnverified, []string{"part-01.md"})
	writeTutorial(t, home, "new", store.StatusUnverified, []string{"part-01.md"})
	dir, _ := config.TutorialsDir()
	old, err := store.MoveToTrash(dir, "old", time.Now().Add(-40*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	out, err := runRm(t, "", false, false, "new")
	if err != nil || !strings.Contains(out, "Deleted 1 expired Trash entry") {
		t.Fatalf("rm = %q, %v", out, err)
	}
	if _, err := os.Stat(old.Path); !os.IsNotExist(err) {
		t.Error("expired Trash entry kept")
	}
	if list, _ := store.ListTrash(dir); len(list) != 1 || list[0].Slug != "new" {
		t.Errorf("Trash = %+v", list)
	}

	// trash_days: 0 is Never.
	if err := config.UpdateConfig(func(c *config.Config) error { v := 0; c.TrashDays = &v; return nil }); err != nil {
		t.Fatal(err)
	}
	writeTutorial(t, home, "other", store.StatusUnverified, []string{"part-01.md"})
	if _, err := store.MoveToTrash(dir, "other", time.Now().Add(-400*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	writeTutorial(t, home, "third", store.StatusUnverified, []string{"part-01.md"})
	if _, err := runRm(t, "", false, false, "third"); err != nil {
		t.Fatal(err)
	}
	if list, _ := store.ListTrash(dir); len(list) != 3 {
		t.Errorf("Never purged something: %+v", list)
	}
}
