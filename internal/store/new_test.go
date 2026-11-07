package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSlugFromTopic(t *testing.T) {
	for topic, want := range map[string]string{
		"build a digital synth in Zig":         "digital-synth-zig",
		"How to build a compiler in Rust":      "compiler-rust",
		"  Ring buffers: lock-free, in C++!  ": "ring-buffers-lock-free-c",
		"how to build":                         "how-to-build",
		"???":                                  "tutorial",
		"one two three four five six seven":    "one-two-three-four-five-six",
		"Café au lait":                         "caf-au-lait",
	} {
		if got := SlugFromTopic(topic); got != want {
			t.Errorf("SlugFromTopic(%q) = %q, want %q", topic, got, want)
		}
	}
}

func TestReserveNewMakesUniquePlaceholders(t *testing.T) {
	dir := t.TempDir()
	r := NewReservation{Topic: "build a digital synth in Zig", Voice: "Companion", Repo: "git@github.com:me/synth.git", RepoBranch: "main", Tools: []Tool{{Name: "Zig", Version: "0.13.0"}}}
	tut, tutDir, err := ReserveNew(dir, r)
	if err != nil {
		t.Fatal(err)
	}
	if tut.Slug != "digital-synth-zig" || tutDir != filepath.Join(dir, "digital-synth-zig") {
		t.Fatalf("slug %q dir %q", tut.Slug, tutDir)
	}
	got, err := ReadMetadata(tutDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusWriting || len(got.Parts) != 0 || got.Title != "Build a digital synth in Zig" ||
		got.Voice != "companion" || got.Repo != "github.com/me/synth" || got.RepoBranch != "main" ||
		len(got.Tools) != 1 || got.Tools[0].Name != "zig" {
		t.Errorf("placeholder = %+v", got)
	}
	entries, _ := os.ReadDir(tutDir)
	if len(entries) != 1 {
		t.Errorf("placeholder holds %d files, want only metadata.json", len(entries))
	}

	// The same topic again gets the next free slug.
	tut2, _, err := ReserveNew(dir, r)
	if err != nil || tut2.Slug != "digital-synth-zig-2" {
		t.Fatalf("second reservation = %v, %v; want digital-synth-zig-2", tut2, err)
	}

	// An explicit slug is used exactly, and refused when taken.
	if _, _, err := ReserveNew(dir, NewReservation{Topic: "x", Slug: "digital-synth-zig"}); !errors.Is(err, ErrSlugTaken) {
		t.Errorf("explicit taken slug err = %v, want ErrSlugTaken", err)
	}
	if _, _, err := ReserveNew(dir, NewReservation{Topic: "x", Slug: "../evil"}); err == nil {
		t.Error("invalid explicit slug was accepted")
	}
	if _, _, err := ReserveNew(dir, NewReservation{Topic: "  "}); err == nil {
		t.Error("empty topic was accepted")
	}
}

func TestCancelNewRemovesOnlyAnUnstoredReservation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	tutorials := filepath.Join(home, ".lathe", "tutorials")
	_, tutDir, err := ReserveNew(tutorials, NewReservation{Topic: "parsers in go"})
	if err != nil {
		t.Fatal(err)
	}
	if err := CancelNew(tutDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tutDir); !os.IsNotExist(err) {
		t.Fatalf("reservation still there (err %v)", err)
	}
	if err := CancelNew(tutDir); !errors.Is(err, ErrNotInFlight) {
		t.Errorf("CancelNew on a gone reservation err = %v, want ErrNotInFlight", err)
	}

	// Once stored into the reserved slug, it is a real tutorial: kept.
	tut, tutDir, err := ReserveNew(tutorials, NewReservation{Topic: "parsers in go"})
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "lathe-something-else")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "part-01.md"), []byte("# Parsers"), 0o644); err != nil {
		t.Fatal(err)
	}
	stored, err := Store(src, StoreOptions{Slug: tut.Slug})
	if err != nil {
		t.Fatal(err)
	}
	if stored.Slug != tut.Slug || stored.Status != StatusUnverified || len(stored.Parts) != 1 {
		t.Fatalf("stored = %+v, want the reserved slug, unverified, one part", stored)
	}
	if _, err := os.Stat(filepath.Join(tutorials, "something-else")); !os.IsNotExist(err) {
		t.Errorf("Store with Slug also wrote the source dir's slug (err %v)", err)
	}
	if err := CancelNew(tutDir); !errors.Is(err, ErrNotInFlight) {
		t.Errorf("CancelNew after store err = %v, want ErrNotInFlight", err)
	}
	if _, err := os.Stat(filepath.Join(tutDir, "part-01.md")); err != nil {
		t.Errorf("stored tutorial was removed: %v", err)
	}
}

func TestWritingBlocksVerifyAndExtend(t *testing.T) {
	dir := cancelFixture(t, StatusWriting)
	if err := RecordVerify(dir, VerifyRecord{Status: StatusVerifying}); err == nil {
		t.Error("verifying a tutorial still being written was allowed")
	}
	if _, err := ExtendStart(dir); err == nil {
		t.Error("extending a tutorial still being written was allowed")
	}
}
