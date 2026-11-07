package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordWritingTracksStepsSourcesAndAsk(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	tutorials := filepath.Join(home, ".lathe", "tutorials")
	tut, tutDir, err := ReserveNew(tutorials, NewReservation{Topic: "a Game Boy emulator in Rust"})
	if err != nil {
		t.Fatal(err)
	}
	if w, err := ReadWriting(tutDir); w != nil || err != nil {
		t.Fatalf("fresh reservation: %+v, %v; want nothing", w, err)
	}
	if _, err := RecordWriting(tutDir, WritingUpdate{Step: "versions", Ask: "confirm versions", Replies: []string{"Sounds right", " No  repo ", ""}}); err != nil {
		t.Fatal(err)
	}
	w, _ := ReadWriting(tutDir)
	if w.Step != WritingVersions || w.Ask == nil || w.Ask.Label != "confirm versions" || strings.Join(w.Ask.Replies, "|") != "Sounds right|No repo" {
		t.Fatalf("after the ask: %+v", w)
	}
	src := Source{Title: "Pan Docs", URL: "https://gbdev.io/pandocs/"}
	for i := 0; i < 2; i++ {
		if _, err := RecordWriting(tutDir, WritingUpdate{Step: "research", Sources: []Source{src}}); err != nil {
			t.Fatal(err)
		}
	}
	w, _ = ReadWriting(tutDir)
	if w.Step != WritingResearch || w.Ask != nil || len(w.Sources) != 1 {
		t.Errorf("after research: %+v (ask cleared, one source)", w)
	}
	if WritingStepIndex(w.Step) != 2 || WritingStepIndex("nope") != 0 {
		t.Error("WritingStepIndex")
	}
	for _, bad := range []WritingUpdate{
		{Step: "proofread"},
		{Ask: strings.Repeat("x", 61)},
		{Replies: []string{"yes"}},
		{Ask: "pick", Replies: []string{"a", "b", "c", "d", "e"}},
	} {
		if _, err := RecordWriting(tutDir, bad); err == nil {
			t.Errorf("RecordWriting(%+v) accepted", bad)
		}
	}

	// Storing into the reservation drops the run's progress, and a stored
	// tutorial takes no more reports.
	srcDir := filepath.Join(t.TempDir(), "gb")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "part-01.md"), []byte("# GB"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Store(srcDir, StoreOptions{Slug: tut.Slug}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tutDir, WritingFile)); !os.IsNotExist(err) {
		t.Errorf("writing.json survived the store (err %v)", err)
	}
	if _, err := RecordWriting(tutDir, WritingUpdate{Step: "write"}); !errors.Is(err, ErrNotInFlight) {
		t.Errorf("report after the store err = %v, want ErrNotInFlight", err)
	}
}
