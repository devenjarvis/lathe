package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/devenjarvis/lathe/internal/store"
)

func TestExtendCommandPrintsHandoff(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	tutDir := writeTutorial(t, homeDir, "test-slug", store.StatusVerified, []string{"part-01.md"})

	var out bytes.Buffer
	extendCmd.SetOut(&out)
	t.Cleanup(func() { extendCmd.SetOut(nil) })
	extendCmd.Flags().Set("guidance", "") //nolint:errcheck
	if err := extendCmd.RunE(extendCmd, []string{"test-slug"}); err != nil {
		t.Fatalf("extend: %v", err)
	}

	if !strings.Contains(out.String(), "/lathe-extend test-slug") {
		t.Errorf("output = %q, want the /lathe-extend handoff command", out.String())
	}

	// Handoff must not touch durable state — the skill does that.
	got, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if got.Status != store.StatusVerified {
		t.Errorf("Status = %q, want %q (handoff must not change status)", got.Status, store.StatusVerified)
	}
	if got.PendingPart != "" {
		t.Errorf("PendingPart = %q, want empty (handoff must not reserve a part)", got.PendingPart)
	}
}

func TestExtendCommandFoldsGuidanceIntoHandoff(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	writeTutorial(t, homeDir, "test-slug", store.StatusVerified, []string{"part-01.md"})

	var out bytes.Buffer
	extendCmd.SetOut(&out)
	t.Cleanup(func() { extendCmd.SetOut(nil) })
	extendCmd.Flags().Set("guidance", "cover the filter envelope") //nolint:errcheck
	t.Cleanup(func() { extendCmd.Flags().Set("guidance", "") })     //nolint:errcheck
	if err := extendCmd.RunE(extendCmd, []string{"test-slug"}); err != nil {
		t.Fatalf("extend: %v", err)
	}

	if !strings.Contains(out.String(), "/lathe-extend test-slug cover the filter envelope") {
		t.Errorf("output = %q, want guidance folded into the handoff", out.String())
	}
}
