package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devenjarvis/lathe/internal/config"
)

func TestOnboardedRoundTrip(t *testing.T) {
	withTempHome(t)
	if config.Onboarded() {
		t.Fatal("a fresh home reads as onboarded")
	}
	if err := config.SetDefaultVoice("terse"); err != nil {
		t.Fatal(err)
	}
	if err := config.SetOnboarded(true); err != nil {
		t.Fatal(err)
	}
	if !config.Onboarded() {
		t.Error("SetOnboarded(true) did not stick")
	}
	if config.DefaultVoice() != "terse" {
		t.Error("SetOnboarded dropped another key")
	}
	if err := config.SetOnboarded(false); err != nil {
		t.Fatal(err)
	}
	if config.Onboarded() {
		t.Error("SetOnboarded(false) did not stick")
	}
}

// A broken config.json must not trap the reader in the setup flow.
func TestOnboardedUnreadableConfigReadsAsDone(t *testing.T) {
	withTempHome(t)
	dir, _ := config.ConfigDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	if !config.Onboarded() {
		t.Error("an unreadable config reads as not onboarded")
	}
}
