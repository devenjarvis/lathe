package config_test

import (
	"os"
	"strings"
	"testing"

	"github.com/devenjarvis/lathe/internal/config"
)

func TestTutorialsDir(t *testing.T) {
	withTempHome(t)
	dir, err := config.TutorialsDir()
	if err != nil {
		t.Fatalf("TutorialsDir() error = %v", err)
	}
	if !strings.HasSuffix(dir, ".lathe/tutorials") {
		t.Errorf("TutorialsDir() = %q, want path ending in .lathe/tutorials", dir)
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Errorf("TutorialsDir() did not create directory at %q", dir)
	}
}

func TestVoicesDir(t *testing.T) {
	withTempHome(t)
	dir, err := config.VoicesDir()
	if err != nil {
		t.Fatalf("VoicesDir() error = %v", err)
	}
	if !strings.HasSuffix(dir, ".lathe/voices") {
		t.Errorf("VoicesDir() = %q, want path ending in .lathe/voices", dir)
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Errorf("VoicesDir() did not create directory at %q", dir)
	}
}

// withTempHome points os.UserHomeDir at a temp dir so the config tests never
// touch the developer's real ~/.lathe/config.json.
func withTempHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

func TestDefaultVoiceFallsBackWhenUnset(t *testing.T) {
	withTempHome(t)
	if got := config.DefaultVoice(); got != config.DefaultVoiceName {
		t.Errorf("DefaultVoice() = %q, want %q", got, config.DefaultVoiceName)
	}
}

func TestSetAndReadDefaultVoiceRoundTrip(t *testing.T) {
	withTempHome(t)
	if err := config.SetDefaultVoice("companion"); err != nil {
		t.Fatalf("SetDefaultVoice: %v", err)
	}
	if got := config.DefaultVoice(); got != "companion" {
		t.Errorf("DefaultVoice() = %q, want companion", got)
	}
	c, err := config.ReadConfig()
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if c.DefaultVoice != "companion" {
		t.Errorf("Config.DefaultVoice = %q, want companion", c.DefaultVoice)
	}
}

func TestSetDefaultVoiceTrimsAndEmptyFallsBack(t *testing.T) {
	withTempHome(t)
	if err := config.SetDefaultVoice("  plainspoken  "); err != nil {
		t.Fatalf("SetDefaultVoice: %v", err)
	}
	if got := config.DefaultVoice(); got != "plainspoken" {
		t.Errorf("DefaultVoice() = %q, want plainspoken", got)
	}
	if err := config.SetDefaultVoice(""); err != nil {
		t.Fatalf("SetDefaultVoice(empty): %v", err)
	}
	if got := config.DefaultVoice(); got != config.DefaultVoiceName {
		t.Errorf("DefaultVoice() = %q, want %q", got, config.DefaultVoiceName)
	}
}

func TestReadConfigMissingFileIsZero(t *testing.T) {
	withTempHome(t)
	c, err := config.ReadConfig()
	if err != nil {
		t.Fatalf("ReadConfig on missing file: %v", err)
	}
	if c.DefaultVoice != "" {
		t.Errorf("expected zero Config, got %+v", c)
	}
}
