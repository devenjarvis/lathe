package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// trashLibrary makes a tutorials dir (inside a fresh ~/.lathe-like parent)
// holding the given slugs, each with a metadata.json.
func trashLibrary(t *testing.T, slugs ...string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "tutorials")
	for _, slug := range slugs {
		p := filepath.Join(dir, slug)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := WriteMetadata(p, &Tutorial{Slug: slug, Title: "Title " + slug, Parts: []string{"part-01.md"}}); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "part-01.md"), []byte("# One\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func placeOn(t *testing.T, dir, slug, shelf string) {
	t.Helper()
	if _, err := UpdateShelves(ShelvesPath(dir), func(s *Shelves) error {
		sh, ok := s.Find(shelf)
		if !ok {
			var err error
			if sh, err = s.Create(shelf); err != nil {
				return err
			}
		}
		return s.Assign(slug, sh.ID)
	}); err != nil {
		t.Fatal(err)
	}
}

func shelfOf(t *testing.T, dir, slug string) string {
	t.Helper()
	s, err := ReadShelves(ShelvesPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	return s.ShelfOf(slug).Name
}

func TestTrashPathIsBesideTutorialsDir(t *testing.T) {
	if got, want := TrashPath(filepath.FromSlash("/h/.lathe/tutorials")), filepath.FromSlash("/h/.lathe/trash"); got != want {
		t.Errorf("TrashPath = %q, want %q", got, want)
	}
}

func TestMoveToTrashAndRestoreKeepsShelf(t *testing.T) {
	dir := trashLibrary(t, "synth-in-zig", "kv")
	placeOn(t, dir, "synth-in-zig", "Audio")
	now := time.Date(2026, 11, 7, 9, 30, 0, 123e6, time.UTC)

	e, err := MoveToTrash(dir, "synth-in-zig", now)
	if err != nil {
		t.Fatal(err)
	}
	if e.ID != "synth-in-zig-20261107T093000.123Z" || e.Slug != "synth-in-zig" || e.Title != "Title synth-in-zig" || !e.TrashedAt.Equal(now) {
		t.Errorf("entry = %+v", e)
	}
	if _, err := os.Stat(filepath.Join(dir, "synth-in-zig")); !os.IsNotExist(err) {
		t.Errorf("tutorial still in the library: %v", err)
	}
	if got := shelfOf(t, dir, "synth-in-zig"); got != "Audio" {
		t.Errorf("trashing dropped the shelf: on %q", got)
	}

	list, err := ListTrash(dir)
	if err != nil || len(list) != 1 || list[0].ID != e.ID || list[0].Title != "Title synth-in-zig" {
		t.Fatalf("ListTrash = %+v, %v", list, err)
	}

	slug, err := RestoreTrashed(dir, e.ID)
	if err != nil || slug != "synth-in-zig" {
		t.Fatalf("restore = %q, %v", slug, err)
	}
	if _, err := ReadMetadata(filepath.Join(dir, "synth-in-zig")); err != nil {
		t.Errorf("restored tutorial unreadable: %v", err)
	}
	if got := shelfOf(t, dir, "synth-in-zig"); got != "Audio" {
		t.Errorf("restored onto %q, want Audio", got)
	}
	if list, _ := ListTrash(dir); len(list) != 0 {
		t.Errorf("Trash not empty after restore: %+v", list)
	}
}

func TestMoveToTrashSameSlugTwice(t *testing.T) {
	dir := trashLibrary(t, "kv")
	now := time.Now()
	a, err := MoveToTrash(dir, "kv", now)
	if err != nil {
		t.Fatal(err)
	}
	trashLibraryInto(t, dir, "kv")
	b, err := MoveToTrash(dir, "kv", now)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Fatalf("both deletes got ID %q", a.ID)
	}
	list, _ := ListTrash(dir)
	if len(list) != 2 || list[0].ID != b.ID {
		t.Errorf("ListTrash = %+v, want newest (%s) first", list, b.ID)
	}
}

func trashLibraryInto(t *testing.T, dir, slug string) {
	t.Helper()
	p := filepath.Join(dir, slug)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteMetadata(p, &Tutorial{Slug: slug, Title: "New " + slug}); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreRefusesATakenSlug(t *testing.T) {
	dir := trashLibrary(t, "kv")
	e, err := MoveToTrash(dir, "kv", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	trashLibraryInto(t, dir, "kv") // re-stored since
	if _, err := RestoreTrashed(dir, e.ID); !errors.Is(err, ErrSlugTaken) {
		t.Fatalf("restore over a stored tutorial: %v, want ErrSlugTaken", err)
	}
	if _, err := os.Stat(e.Path); err != nil {
		t.Errorf("refused restore lost the Trash entry: %v", err)
	}
}

func TestTrashRejectsBadIDs(t *testing.T) {
	dir := trashLibrary(t, "kv")
	if err := os.MkdirAll(filepath.Join(TrashPath(dir), "not-an-entry"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "..", "../tutorials", "kv", "not-an-entry", "kv-20261107T093000.123Z", "x/../../kv-20261107T093000.123Z"} {
		if _, err := RestoreTrashed(dir, id); !errors.Is(err, ErrTrashNotFound) {
			t.Errorf("RestoreTrashed(%q) = %v, want ErrTrashNotFound", id, err)
		}
		if err := DeleteTrashed(dir, id); !errors.Is(err, ErrTrashNotFound) {
			t.Errorf("DeleteTrashed(%q) = %v, want ErrTrashNotFound", id, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "kv")); err != nil {
		t.Errorf("library disturbed: %v", err)
	}
	if list, _ := ListTrash(dir); len(list) != 0 {
		t.Errorf("a stray folder listed as a Trash entry: %+v", list)
	}
}

func TestDeleteTrashedForgetsShelf(t *testing.T) {
	dir := trashLibrary(t, "kv", "synth")
	placeOn(t, dir, "kv", "Systems")
	placeOn(t, dir, "synth", "Systems")
	e, err := MoveToTrash(dir, "kv", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := DeleteTrashed(dir, e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.Path); !os.IsNotExist(err) {
		t.Errorf("entry still on disk: %v", err)
	}
	s, _ := ReadShelves(ShelvesPath(dir))
	if _, ok := s.Placements["kv"]; ok {
		t.Error("permanent delete kept the shelf placement")
	}
	if s.ShelfOf("synth").Name != "Systems" {
		t.Error("another tutorial's placement was dropped")
	}
}

func TestDeleteNowSkipsTrash(t *testing.T) {
	dir := trashLibrary(t, "kv")
	placeOn(t, dir, "kv", "Systems")
	if err := DeleteNow(dir, "kv"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "kv")); !os.IsNotExist(err) {
		t.Errorf("still in the library: %v", err)
	}
	if list, _ := ListTrash(dir); len(list) != 0 {
		t.Errorf("DeleteNow used the Trash: %+v", list)
	}
	if s, _ := ReadShelves(ShelvesPath(dir)); len(s.Placements) != 0 {
		t.Errorf("placement kept: %v", s.Placements)
	}
	if err := DeleteNow(dir, "kv"); err == nil {
		t.Error("deleting a missing tutorial succeeded")
	}
}

func TestPurgeTrash(t *testing.T) {
	dir := trashLibrary(t, "old", "recent", "keep")
	placeOn(t, dir, "old", "Systems")
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	old, _ := MoveToTrash(dir, "old", now.Add(-31*24*time.Hour))
	recent, _ := MoveToTrash(dir, "recent", now.Add(-6*24*time.Hour))

	if n, err := PurgeTrash(dir, 0, now); n != 0 || err != nil {
		t.Errorf("never: purged %d, %v", n, err)
	}
	n, err := PurgeTrash(dir, 30, now)
	if n != 1 || err != nil {
		t.Fatalf("30 days: purged %d, %v; want 1", n, err)
	}
	if _, err := os.Stat(old.Path); !os.IsNotExist(err) {
		t.Error("expired entry kept")
	}
	if _, err := os.Stat(recent.Path); err != nil {
		t.Error("recent entry purged")
	}
	if s, _ := ReadShelves(ShelvesPath(dir)); len(s.Placements) != 0 {
		t.Errorf("purge kept the expired tutorial's shelf: %v", s.Placements)
	}
	if n, _ := PurgeTrash(dir, 7, now); n != 0 {
		t.Errorf("7 days purged the 6-day-old entry")
	}
	if _, err := os.Stat(filepath.Join(dir, "keep")); err != nil {
		t.Error("purge touched the library")
	}
	// No Trash at all is fine.
	if n, err := PurgeTrash(trashLibrary(t), 7, now); n != 0 || err != nil {
		t.Errorf("empty: %d, %v", n, err)
	}
}

func TestShelfDescribe(t *testing.T) {
	s := &Shelves{}
	s.normalize()
	sh, _ := s.Create("Systems")
	if err := s.Describe(sh.ID, "  Kernels,\n  databases   and networks "); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(sh.ID); got.Description != "Kernels, databases and networks" {
		t.Errorf("description = %q", got.Description)
	}
	if err := s.Describe(sh.ID, stringOf('x', MaxShelfDescriptionLen+1)); err == nil {
		t.Error("over-long description accepted")
	}
	if err := s.Describe(UnsortedShelfID, "x"); err == nil {
		t.Error("described Unsorted")
	}
	if err := s.Describe("nope", "x"); !errors.Is(err, ErrShelfNotFound) {
		t.Errorf("missing shelf: %v", err)
	}
	if err := s.Describe(sh.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(sh.ID); got.Description != "" {
		t.Errorf("not cleared: %q", got.Description)
	}
}

func stringOf(r rune, n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = r
	}
	return string(b)
}
