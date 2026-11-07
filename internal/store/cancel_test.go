package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func cancelFixture(t *testing.T, status Status, parts ...string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "tut")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range parts {
		if err := os.WriteFile(filepath.Join(dir, p), []byte("# "+p), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteMetadata(dir, &Tutorial{Slug: "tut", Title: "T", Status: status, Parts: parts, Created: time.Now()}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCancelVerify(t *testing.T) {
	dir := cancelFixture(t, StatusVerifying, "part-01.md")
	if err := WriteVerifyResult(dir, &VerifyResult{Status: StatusSkipped}); err != nil {
		t.Fatal(err)
	}
	st, err := CancelVerify(dir)
	if err != nil || st != StatusSkipped {
		t.Fatalf("CancelVerify = %q, %v; want skipped (the prior result)", st, err)
	}
	if _, err := CancelVerify(dir); !errors.Is(err, ErrNotInFlight) {
		t.Errorf("second CancelVerify err = %v, want ErrNotInFlight", err)
	}
}

func TestCancelExtendDiscardsReservedPart(t *testing.T) {
	dir := cancelFixture(t, StatusUnverified, "part-01.md")
	name, err := ExtendStart(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("half"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := CancelExtend(dir)
	if err != nil || st != StatusUnverified {
		t.Fatalf("CancelExtend = %q, %v", st, err)
	}
	tut, _ := ReadMetadata(dir)
	if tut.PendingPart != "" || len(tut.Parts) != 1 {
		t.Errorf("after cancel: pending %q parts %v", tut.PendingPart, tut.Parts)
	}
	if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
		t.Errorf("%s kept after cancel (err %v)", name, err)
	}
	// Nothing written yet is fine too; and a non-extending tutorial is refused.
	if _, err := ExtendStart(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := CancelExtend(dir); err != nil {
		t.Errorf("cancel with no part written: %v", err)
	}
	if _, err := CancelExtend(dir); !errors.Is(err, ErrNotInFlight) {
		t.Errorf("CancelExtend on a settled tutorial err = %v, want ErrNotInFlight", err)
	}
}

func TestCancelExtendKeepsRegisteredPart(t *testing.T) {
	// A pending marker naming a part already in Parts (a commit raced the
	// cancel) must never delete a registered part.
	dir := cancelFixture(t, StatusExtending, "part-01.md", "part-02.md")
	tut, _ := ReadMetadata(dir)
	tut.PendingPart = "part-02.md"
	if err := WriteMetadata(dir, tut); err != nil {
		t.Fatal(err)
	}
	if _, err := CancelExtend(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "part-02.md")); err != nil {
		t.Errorf("registered part removed: %v", err)
	}
}
