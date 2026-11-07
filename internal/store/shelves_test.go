package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func shelfNames(s *Shelves) []string {
	var names []string
	for _, sh := range s.Shelves {
		names = append(names, sh.Name)
	}
	return names
}

func TestShelvesPathIsBesideTutorialsDir(t *testing.T) {
	got := ShelvesPath(filepath.Join("/home", "me", ".lathe", "tutorials"))
	want := filepath.Join("/home", "me", ".lathe", ShelvesFile)
	if got != want {
		t.Errorf("ShelvesPath = %q, want %q", got, want)
	}
}

func TestReadShelvesMissingIsEmpty(t *testing.T) {
	s, err := ReadShelves(filepath.Join(t.TempDir(), ShelvesFile))
	if err != nil {
		t.Fatalf("ReadShelves on a missing file: %v", err)
	}
	if len(s.Shelves) != 0 || s.Placements == nil {
		t.Fatalf("got %+v, want no shelves and a non-nil placement map", s)
	}
	if got := s.ShelfOf("anything"); !got.Unsorted() {
		t.Errorf("ShelfOf on an empty library = %+v, want Unsorted", got)
	}
}

func TestReadShelvesCorruptIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), ShelvesFile)
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadShelves(path); err == nil {
		t.Fatal("ReadShelves on a corrupt file returned no error")
	}
	// UpdateShelves must refuse rather than clobber the reader's filing.
	if _, err := UpdateShelves(path, func(s *Shelves) error { _, err := s.Create("Systems"); return err }); err == nil {
		t.Fatal("UpdateShelves over a corrupt file returned no error")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "{not json" {
		t.Errorf("corrupt file was overwritten: %q", data)
	}
}

func TestShelvesRoundTripAndFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", ShelvesFile)
	s, err := UpdateShelves(path, func(s *Shelves) error {
		sys, err := s.Create("Systems")
		if err != nil {
			return err
		}
		if _, err := s.Create("Compilers & languages"); err != nil {
			return err
		}
		return s.Assign("tiny-kernel", sys.ID)
	})
	if err != nil {
		t.Fatalf("UpdateShelves: %v", err)
	}
	if got := s.ShelfOf("tiny-kernel"); got.ID != "systems" || got.Name != "Systems" {
		t.Errorf("ShelfOf(tiny-kernel) = %+v", got)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"version": 1`, `"id": "compilers-languages"`, `"tiny-kernel": "systems"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("shelves.json missing %s:\n%s", want, raw)
		}
	}
	// No temp file is left beside it.
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("dir has %d entries after an atomic write, want 1", len(entries))
	}

	back, err := ReadShelves(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(shelfNames(back), "|"); got != "Systems|Compilers & languages" {
		t.Errorf("order after reload = %q", got)
	}
	if back.ShelfOf("tiny-kernel").ID != "systems" {
		t.Error("placement lost across a reload")
	}
}

func TestShelfCreateValidatesNames(t *testing.T) {
	s := &Shelves{}
	s.normalize()
	if _, err := s.Create("  Audio   &  DSP "); err != nil {
		t.Fatal(err)
	}
	if s.Shelves[0].Name != "Audio & DSP" || s.Shelves[0].ID != "audio-dsp" {
		t.Errorf("created %+v, want spaces collapsed and id audio-dsp", s.Shelves[0])
	}
	for _, bad := range []string{"", "   ", "audio & dsp", "Unsorted", "unsorted", strings.Repeat("x", MaxShelfNameLen+1), "tab\x07bell"} {
		if _, err := s.Create(bad); err == nil {
			t.Errorf("Create(%q) succeeded, want an error", bad)
		}
	}
	if len(s.Shelves) != 1 {
		t.Errorf("failed creates changed the list: %v", shelfNames(s))
	}
}

func TestShelfIDsStayUnique(t *testing.T) {
	s := &Shelves{}
	s.normalize()
	a, _ := s.Create("C++")
	b, _ := s.Create("C")
	c, err := s.Create("C!")
	if err == nil {
		// "C!" and "C" slug alike but are different names.
		if a.ID != "c" || b.ID != "c-2" || c.ID != "c-3" {
			t.Errorf("ids = %q %q %q, want c c-2 c-3", a.ID, b.ID, c.ID)
		}
	} else {
		t.Fatal(err)
	}
	if sh, _ := s.Create("!!!"); sh.ID != "shelf" {
		t.Errorf("symbol-only name got id %q, want shelf", sh.ID)
	}
	// The reserved id is never handed out.
	if sh, _ := s.Create("Unsorted stuff"); sh.ID == UnsortedShelfID {
		t.Error("handed out the reserved unsorted id")
	}
}

func TestShelfRenameKeepsIDAndTutorials(t *testing.T) {
	s := &Shelves{}
	s.normalize()
	sh, _ := s.Create("Sytsems")
	_ = s.Assign("tiny-kernel", sh.ID)
	if err := s.Rename(sh.ID, "Systems"); err != nil {
		t.Fatal(err)
	}
	got := s.ShelfOf("tiny-kernel")
	if got.ID != sh.ID || got.Name != "Systems" {
		t.Errorf("after rename ShelfOf = %+v", got)
	}
	// Changing only the case of its own name is allowed.
	if err := s.Rename(sh.ID, "SYSTEMS"); err != nil {
		t.Errorf("case-only rename: %v", err)
	}
	other, _ := s.Create("Audio")
	if err := s.Rename(other.ID, "systems"); err == nil {
		t.Error("rename onto another shelf's name succeeded")
	}
	if err := s.Rename("nope", "X"); !errors.Is(err, ErrShelfNotFound) {
		t.Errorf("rename unknown = %v, want ErrShelfNotFound", err)
	}
	if err := s.Rename(UnsortedShelfID, "Misc"); err == nil {
		t.Error("renaming Unsorted succeeded")
	}
}

func TestShelfDeleteMovesTutorialsToUnsorted(t *testing.T) {
	s := &Shelves{}
	s.normalize()
	sys, _ := s.Create("Systems")
	audio, _ := s.Create("Audio")
	_ = s.Assign("tiny-kernel", sys.ID)
	_ = s.Assign("synth", audio.ID)
	if err := s.Delete(sys.ID); err != nil {
		t.Fatal(err)
	}
	if !s.ShelfOf("tiny-kernel").Unsorted() {
		t.Error("tutorial on a deleted shelf did not move to Unsorted")
	}
	if _, ok := s.Placements["tiny-kernel"]; ok {
		t.Error("deleted shelf's placement left behind")
	}
	if s.ShelfOf("synth").ID != audio.ID {
		t.Error("deleting one shelf moved another shelf's tutorial")
	}
	if err := s.Delete(sys.ID); !errors.Is(err, ErrShelfNotFound) {
		t.Errorf("second delete = %v, want ErrShelfNotFound", err)
	}
	if err := s.Delete(UnsortedShelfID); err == nil {
		t.Error("deleting Unsorted succeeded")
	}
}

func TestShelfAssignMovesBetweenShelves(t *testing.T) {
	s := &Shelves{}
	s.normalize()
	a, _ := s.Create("A")
	b, _ := s.Create("B")
	if err := s.Assign("t", a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Assign("t", b.ID); err != nil {
		t.Fatal(err)
	}
	if s.ShelfOf("t").ID != b.ID {
		t.Error("Assign did not move the tutorial")
	}
	if err := s.Assign("t", UnsortedShelfID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Placements["t"]; ok || !s.ShelfOf("t").Unsorted() {
		t.Error("assigning to Unsorted did not unassign")
	}
	if err := s.Assign("t", "missing"); !errors.Is(err, ErrShelfNotFound) {
		t.Errorf("assign to unknown shelf = %v", err)
	}
	if err := s.Assign("../escape", a.ID); err == nil {
		t.Error("assign with a bad slug succeeded")
	}
}

func TestShelfMoveReorders(t *testing.T) {
	s := &Shelves{}
	s.normalize()
	for _, n := range []string{"A", "B", "C", "D"} {
		if _, err := s.Create(n); err != nil {
			t.Fatal(err)
		}
	}
	steps := []struct {
		id    string
		index int
		want  string
	}{
		{"d", 0, "D|A|B|C"},
		{"d", 99, "A|B|C|D"},
		{"a", 2, "B|C|A|D"},
		{"c", -5, "C|B|A|D"},
	}
	for _, st := range steps {
		if err := s.Move(st.id, st.index); err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(shelfNames(s), "|"); got != st.want {
			t.Errorf("Move(%s,%d) = %s, want %s", st.id, st.index, got, st.want)
		}
	}
	if err := s.Move(UnsortedShelfID, 0); err == nil {
		t.Error("moving Unsorted succeeded")
	}
	if err := s.Move("zzz", 0); !errors.Is(err, ErrShelfNotFound) {
		t.Errorf("move unknown = %v", err)
	}
}

func TestShelfFindByIDOrName(t *testing.T) {
	s := &Shelves{}
	s.normalize()
	sh, _ := s.Create("Audio & DSP")
	for _, q := range []string{"audio-dsp", "Audio & DSP", " audio & dsp "} {
		if got, ok := s.Find(q); !ok || got.ID != sh.ID {
			t.Errorf("Find(%q) = %+v, %v", q, got, ok)
		}
	}
	if got, ok := s.Find("Unsorted"); !ok || !got.Unsorted() {
		t.Errorf("Find(Unsorted) = %+v, %v", got, ok)
	}
	if _, ok := s.Find("nope"); ok {
		t.Error("Find(nope) found something")
	}
}

func TestShelvesNormalizeRepairsHandEdits(t *testing.T) {
	path := filepath.Join(t.TempDir(), ShelvesFile)
	raw := `{"shelves":[{"id":"a","name":"A"},{"id":"a","name":"dup"},{"id":"","name":"no id"},{"id":"unsorted","name":"fake"},{"id":"b","name":""}],
	"placements":{"x":"a","y":"gone","z":"unsorted"}}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := ReadShelves(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(shelfNames(s), "|"); got != "A|b" {
		t.Errorf("shelves = %s, want A|b", got)
	}
	if s.ShelfOf("y").ID != UnsortedShelfID {
		t.Error("placement on a missing shelf should read as Unsorted")
	}
	if _, ok := s.Placements["z"]; ok {
		t.Error("explicit unsorted placement should be dropped")
	}
}

// A re-store rewrites the tutorial dir; the placement lives outside it.
func TestShelfSurvivesRestore(t *testing.T) {
	home := t.TempDir()
	tutorials := filepath.Join(home, "tutorials")
	tutDir := filepath.Join(tutorials, "tiny-kernel")
	if err := os.MkdirAll(tutDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteMetadata(tutDir, &Tutorial{Slug: "tiny-kernel", Title: "Tiny kernel", Created: time.Now()}); err != nil {
		t.Fatal(err)
	}
	path := ShelvesPath(tutorials)
	if _, err := UpdateShelves(path, func(s *Shelves) error {
		sh, err := s.Create("Systems")
		if err != nil {
			return err
		}
		return s.Assign("tiny-kernel", sh.ID)
	}); err != nil {
		t.Fatal(err)
	}
	// Simulate a re-store: the tutorial dir is replaced wholesale.
	if err := os.RemoveAll(tutDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tutDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteMetadata(tutDir, &Tutorial{Slug: "tiny-kernel", Title: "Tiny kernel v2", Created: time.Now()}); err != nil {
		t.Fatal(err)
	}
	s, err := ReadShelves(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.ShelfOf("tiny-kernel").ID != "systems" {
		t.Error("re-store lost the tutorial's shelf")
	}
	meta, _ := os.ReadFile(filepath.Join(tutDir, "metadata.json"))
	if strings.Contains(string(meta), "systems") {
		t.Error("shelf leaked into metadata.json")
	}
}

func TestUpdateShelvesSerializes(t *testing.T) {
	path := filepath.Join(t.TempDir(), ShelvesFile)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := UpdateShelves(path, func(s *Shelves) error {
				_, err := s.Create("Shelf " + string(rune('A'+i)))
				return err
			})
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	s, err := ReadShelves(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Shelves) != 20 {
		t.Errorf("got %d shelves after 20 concurrent creates, want 20", len(s.Shelves))
	}
}

func TestUpdateShelvesFnErrorWritesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), ShelvesFile)
	boom := errors.New("boom")
	if _, err := UpdateShelves(path, func(*Shelves) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("a failed update wrote the file")
	}
}
