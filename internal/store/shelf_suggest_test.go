package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// suggestLibrary makes a tutorials dir holding slugs, all Unsorted.
func suggestLibrary(t *testing.T, slugs ...string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "tutorials")
	for _, slug := range slugs {
		if err := os.MkdirAll(filepath.Join(dir, slug), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := WriteMetadata(filepath.Join(dir, slug), &Tutorial{Slug: slug, Title: slug, Status: StatusUnverified}); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestProposeShelfMovesNothingUntilApplied(t *testing.T) {
	dir := suggestLibrary(t, "synth", "lexer", "kernel")
	path := ShelvesPath(dir)
	if _, err := UpdateShelves(path, func(s *Shelves) error { _, err := s.Create("Systems"); return err }); err != nil {
		t.Fatal(err)
	}
	if msg, err := ProposeShelf(dir, "kernel", "systems"); err != nil || !strings.Contains(msg, "existing shelf") {
		t.Fatalf("propose existing: %q %v", msg, err)
	}
	if msg, err := ProposeShelf(dir, "lexer", "Compilers"); err != nil || !strings.Contains(msg, "new shelf") {
		t.Fatalf("propose new: %q %v", msg, err)
	}
	if _, err := ProposeShelf(dir, "synth", "Audio"); err != nil {
		t.Fatal(err)
	}
	if _, err := ProposeShelf(dir, "missing", "Audio"); err == nil {
		t.Error("proposed a shelf for a missing tutorial")
	}
	if _, err := ProposeShelf(dir, "synth", "  "); err == nil {
		t.Error("proposed an empty shelf name")
	}
	s, _ := ReadShelves(path)
	if len(s.Placements) != 0 || len(s.Shelves) != 1 {
		t.Fatalf("proposing changed shelves.json: %+v", s)
	}
	sug, _ := ReadShelfSuggestions(dir)
	if sug.Sort == nil || sug.Sort.Picks["kernel"] != "Systems" || sug.Sort.Picks["lexer"] != "Compilers" {
		t.Fatalf("picks = %+v", sug.Sort)
	}

	// Apply only two of the three: the third is dropped with the proposal.
	res, err := ApplySort(dir, []string{"kernel", "lexer"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Moved != 2 || len(res.Created) != 1 || res.Created[0].Name != "Compilers" {
		t.Fatalf("result = %+v", res)
	}
	s, _ = ReadShelves(path)
	if s.ShelfOf("kernel").Name != "Systems" || s.ShelfOf("lexer").Name != "Compilers" || !s.ShelfOf("synth").Unsorted() {
		t.Fatalf("placements = %+v", s.Placements)
	}
	if sug, _ = ReadShelfSuggestions(dir); sug.Sort != nil {
		t.Errorf("proposal kept after apply: %+v", sug.Sort)
	}
	if _, err := ApplySort(dir, nil); err == nil {
		t.Error("applied an empty proposal")
	}
}

func TestResetSortProposal(t *testing.T) {
	dir := suggestLibrary(t, "synth")
	if _, err := ProposeShelf(dir, "synth", "Audio"); err != nil {
		t.Fatal(err)
	}
	if err := ResetSortProposal(dir); err != nil {
		t.Fatal(err)
	}
	if sug, _ := ReadShelfSuggestions(dir); sug.Sort != nil {
		t.Errorf("sort = %+v after reset", sug.Sort)
	}
}

func splitLibrary(t *testing.T) (string, string) {
	t.Helper()
	dir := suggestLibrary(t, "a1", "a2", "a3", "b1", "b2", "other")
	var id string
	_, err := UpdateShelves(ShelvesPath(dir), func(s *Shelves) error {
		sh, err := s.Create("Languages")
		if err != nil {
			return err
		}
		if _, err := s.Create("Later"); err != nil {
			return err
		}
		id = sh.ID
		for _, slug := range []string{"a1", "a2", "a3", "b1", "b2"} {
			if err := s.Assign(slug, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return dir, id
}

func TestProposeSplitValidates(t *testing.T) {
	dir, id := splitLibrary(t)
	g := func(a, b []string) []SplitGroup {
		return []SplitGroup{{Name: "Interpreters", Slugs: a}, {Name: "Compilers", Slugs: b}}
	}
	for name, groups := range map[string][]SplitGroup{
		"one group":     {{Name: "X", Slugs: []string{"a1", "a2", "a3", "b1", "b2"}}},
		"missing slug":  g([]string{"a1", "a2"}, []string{"b1", "b2"}),
		"off the shelf": g([]string{"a1", "a2", "a3", "other"}, []string{"b1", "b2"}),
		"duplicate":     g([]string{"a1", "a2", "a3", "b1"}, []string{"b1", "b2"}),
		"empty group":   g([]string{"a1", "a2", "a3", "b1", "b2"}, nil),
		"same names":    {{Name: "X", Slugs: []string{"a1", "a2", "a3"}}, {Name: "x", Slugs: []string{"b1", "b2"}}},
	} {
		if _, err := ProposeSplit(dir, id, groups); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := ProposeSplit(dir, UnsortedShelfID, g([]string{"other"}, []string{"other"})); err == nil {
		t.Error("split of Unsorted accepted")
	}
	msg, err := ProposeSplit(dir, "languages", g([]string{"a1", "a2", "a3"}, []string{"b1", "b2"}))
	if err != nil || !strings.Contains(msg, "Interpreters (3) and Compilers (2)") {
		t.Fatalf("propose: %q %v", msg, err)
	}
	if s, _ := ReadShelves(ShelvesPath(dir)); s.ShelfOf("b1").ID != id {
		t.Error("proposing moved a tutorial")
	}
}

func TestApplySplitKeepsTheShelfAndPlacesTheNewOneAfterIt(t *testing.T) {
	dir, id := splitLibrary(t)
	if _, err := ProposeSplit(dir, id, []SplitGroup{{Name: "Interpreters", Slugs: []string{"a1", "a2", "a3"}}, {Name: "Compilers", Slugs: []string{"b1", "b2"}}}); err != nil {
		t.Fatal(err)
	}
	if err := DismissSplit(dir, id, 5); err != nil {
		t.Fatal(err)
	}
	// A tutorial filed after the proposal stays with the first group.
	if _, err := UpdateShelves(ShelvesPath(dir), func(s *Shelves) error { return s.Assign("other", id) }); err != nil {
		t.Fatal(err)
	}
	res, err := ApplySplit(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kept.ID != id || res.Kept.Name != "Interpreters" || res.Into.Name != "Compilers" || res.Moved != 2 {
		t.Fatalf("result = %+v", res)
	}
	s, _ := ReadShelves(ShelvesPath(dir))
	if got := strings.Join(shelfNames(s), ","); got != "Interpreters,Compilers,Later" {
		t.Errorf("order = %s", got)
	}
	if s.ShelfOf("other").ID != id || s.ShelfOf("b2").Name != "Compilers" {
		t.Errorf("placements = %+v", s.Placements)
	}
	sug, _ := ReadShelfSuggestions(dir)
	if sug.Splits[id] != nil || sug.Dismissed[id] != 0 {
		t.Errorf("suggestions kept: %+v", sug)
	}
	if _, err := ApplySplit(dir, id); err == nil {
		t.Error("applied a split twice")
	}
}

func TestSplitDue(t *testing.T) {
	for _, c := range []struct {
		count, threshold, dismissed int
		want                        bool
	}{
		{12, 12, 0, false},
		{13, 12, 0, true},
		{30, 0, 0, false}, // Never
		{13, 12, 13, false},
		{15, 12, 13, false},
		{16, 12, 13, true},
		{9, 8, 0, true},
	} {
		if got := SplitDue(c.count, c.threshold, c.dismissed); got != c.want {
			t.Errorf("SplitDue(%d, %d, %d) = %v", c.count, c.threshold, c.dismissed, got)
		}
	}
}
