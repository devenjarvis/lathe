package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
	"github.com/devenjarvis/lathe/internal/voice"
)

func TestResolveShowNameExplicitArgWins(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got, err := resolveShowName([]string{"companion"}, "")
	if err != nil {
		t.Fatalf("resolveShowName: %v", err)
	}
	if got != "companion" {
		t.Errorf("got %q, want companion", got)
	}
}

func TestResolveShowNameNoArgsUsesDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got, err := resolveShowName(nil, "")
	if err != nil {
		t.Fatalf("resolveShowName: %v", err)
	}
	if got != config.DefaultVoiceName {
		t.Errorf("got %q, want %q", got, config.DefaultVoiceName)
	}
}

func TestResolveShowNameTutorialVoice(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeTutorialWithVoice(t, home, "synth", "companion")

	got, err := resolveShowName(nil, "synth")
	if err != nil {
		t.Fatalf("resolveShowName: %v", err)
	}
	if got != "companion" {
		t.Errorf("got %q, want companion", got)
	}
}

func TestResolveShowNameTutorialWithoutVoiceFallsBack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeTutorialWithVoice(t, home, "legacy", "") // pre-feature tutorial

	got, err := resolveShowName(nil, "legacy")
	if err != nil {
		t.Fatalf("resolveShowName: %v", err)
	}
	if got != config.DefaultVoiceName {
		t.Errorf("got %q, want %q (default fallback)", got, config.DefaultVoiceName)
	}
}

func TestVoiceShowCmdPrintsWrappedSpec(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var out bytes.Buffer
	voiceShowCmd.SetOut(&out)
	t.Cleanup(func() { voiceShowCmd.SetOut(nil) })
	if err := voiceShowCmd.RunE(voiceShowCmd, []string{"plainspoken"}); err != nil {
		t.Fatalf("voice show: %v", err)
	}
	if !strings.HasPrefix(out.String(), voice.Preamble) {
		t.Errorf("voice show output must start with the guardrail preamble")
	}
}

func TestVoiceRmResetsDefaultWhenRemovingTheDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Add a custom voice and make it the default.
	spec := []byte("---\nname: temp\ndescription: t\n---\n# Temp\nbody\n")
	if err := voice.Add("temp", spec); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := config.SetDefaultVoice("temp"); err != nil {
		t.Fatalf("SetDefaultVoice: %v", err)
	}

	var out bytes.Buffer
	voiceRmCmd.SetOut(&out)
	t.Cleanup(func() { voiceRmCmd.SetOut(nil) })
	if err := voiceRmCmd.RunE(voiceRmCmd, []string{"temp"}); err != nil {
		t.Fatalf("voice rm: %v", err)
	}

	// Removing the current default must revert it to the built-in default so no
	// tutorial resolves a dangling voice.
	if got := config.DefaultVoice(); got != config.DefaultVoiceName {
		t.Errorf("default after rm = %q, want %q", got, config.DefaultVoiceName)
	}
	if !strings.Contains(out.String(), "reset default") {
		t.Errorf("expected the reset notice in output, got %q", out.String())
	}
}

func TestVoiceRmKeepsDefaultWhenRemovingOther(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if err := voice.Add("keep", []byte("---\nname: keep\n---\nx\n")); err != nil {
		t.Fatalf("Add keep: %v", err)
	}
	if err := voice.Add("drop", []byte("---\nname: drop\n---\nx\n")); err != nil {
		t.Fatalf("Add drop: %v", err)
	}
	if err := config.SetDefaultVoice("keep"); err != nil {
		t.Fatalf("SetDefaultVoice: %v", err)
	}

	var out bytes.Buffer
	voiceRmCmd.SetOut(&out)
	t.Cleanup(func() { voiceRmCmd.SetOut(nil) })
	if err := voiceRmCmd.RunE(voiceRmCmd, []string{"drop"}); err != nil {
		t.Fatalf("voice rm: %v", err)
	}

	if got := config.DefaultVoice(); got != "keep" {
		t.Errorf("default = %q, want keep (untouched)", got)
	}
	if strings.Contains(out.String(), "reset default") {
		t.Errorf("should not have reset the default when removing a non-default voice")
	}
}

func writeTutorialWithVoice(t *testing.T, homeDir, slug, voiceName string) {
	t.Helper()
	tutDir := filepath.Join(homeDir, ".lathe", "tutorials", slug)
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	tut := &store.Tutorial{Slug: slug, Title: store.SlugToTitle(slug), Status: store.StatusUnverified, Voice: voiceName}
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}
}
