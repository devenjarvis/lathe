package voice_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devenjarvis/lathe/internal/voice"
)

const draftSpec = `---
name: whatever
description: Short labelled lines.
---

# Field notes

## Stance and register

- Terse.

## Sample

A ring buffer is a fixed array with two indices.
`

func TestDraftSeriesReserveDraftApproveSave(t *testing.T) {
	home := withTempHome(t)

	if _, err := voice.ReserveDraft("plainspoken"); !errors.Is(err, voice.ErrExists) {
		t.Errorf("reserving a built-in name: %v, want ErrExists", err)
	}
	if _, err := voice.ReserveDraft("Not A Slug"); err == nil {
		t.Error("reserving a non-slug should fail")
	}
	dir, err := voice.ReserveDraft("field-notes")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := voice.ReserveDraft("field-notes"); !errors.Is(err, voice.ErrDraftBusy) {
		t.Errorf("second reservation: %v, want ErrDraftBusy", err)
	}
	// A reservation is not a voice.
	if _, err := voice.Resolve("field-notes"); err == nil {
		t.Error("a reservation should not resolve as a voice")
	}

	if _, err := voice.AddDraft("field-notes", "# No sample\n\nJust a body.\n"); err == nil || !strings.Contains(err.Error(), "## Sample") {
		t.Errorf("draft without a sample: %v", err)
	}
	n, err := voice.AddDraft("field-notes", voice.Preamble+draftSpec)
	if err != nil || n != 1 {
		t.Fatalf("AddDraft = %d, %v", n, err)
	}
	n, err = voice.AddDraft("field-notes", strings.Replace(draftSpec, "Terse.", "Terser.", 1))
	if err != nil || n != 2 {
		t.Fatalf("AddDraft 2 = %d, %v", n, err)
	}
	drafts, err := voice.Drafts("field-notes")
	if err != nil || len(drafts) != 2 {
		t.Fatalf("Drafts = %v, %v", drafts, err)
	}
	first := string(drafts[0].Raw)
	if strings.Contains(first, "GUARDRAIL") || !strings.Contains(first, "name: field-notes") {
		t.Errorf("draft 1 should drop the preamble and take the reserved name:\n%s", first)
	}
	if got := drafts[1].Voice("field-notes").Sample(); got != "A ring buffer is a fixed array with two indices." {
		t.Errorf("sample = %q", got)
	}

	if _, _, err := voice.SaveApproved("field-notes"); !errors.Is(err, voice.ErrNotApproved) {
		t.Errorf("save before approval: %v", err)
	}
	if err := voice.Approve("field-notes", voice.Approval{Draft: 9}); err == nil {
		t.Error("approving a missing draft should fail")
	}
	if err := voice.Approve("field-notes", voice.Approval{Draft: 1, MakeDefault: true}); err != nil {
		t.Fatal(err)
	}
	if a, ok := voice.Approved("field-notes"); !ok || a.Draft != 1 || !a.MakeDefault {
		t.Errorf("Approved = %+v, %v", a, ok)
	}
	v, n, err := voice.SaveApproved("field-notes")
	if err != nil || n != 1 {
		t.Fatalf("SaveApproved = %d, %v", n, err)
	}
	if !strings.Contains(v.Body(), "- Terse.") || v.Description != "Short labelled lines." {
		t.Errorf("saved the wrong draft: %q", v.Body())
	}
	if _, err := os.Stat(filepath.Join(home, ".lathe", "voices", "field-notes.md")); err != nil {
		t.Error(err)
	}
	// Saving twice can't replace it.
	if _, _, err := voice.SaveApproved("field-notes"); !errors.Is(err, voice.ErrExists) {
		t.Errorf("second save: %v", err)
	}

	if err := voice.CancelDraft("field-notes"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("reservation still there: %v", err)
	}
	if _, err := voice.AddDraft("field-notes", draftSpec); !errors.Is(err, voice.ErrNoReservation) {
		t.Errorf("draft after cancel: %v", err)
	}
	// The voice list never shows the drafts folder.
	list, _ := voice.List()
	for _, v := range list {
		if strings.HasPrefix(v.Name, ".") {
			t.Errorf("listed %q", v.Name)
		}
	}
}

func TestUnapprove(t *testing.T) {
	withTempHome(t)
	if _, err := voice.ReserveDraft("terse"); err != nil {
		t.Fatal(err)
	}
	if _, err := voice.AddDraft("terse", draftSpec); err != nil {
		t.Fatal(err)
	}
	if err := voice.Approve("terse", voice.Approval{Draft: 1}); err != nil {
		t.Fatal(err)
	}
	if err := voice.Unapprove("terse"); err != nil {
		t.Fatal(err)
	}
	if _, ok := voice.Approved("terse"); ok {
		t.Error("still approved")
	}
}
