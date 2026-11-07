package config_test

import (
	"os"
	"path/filepath"
	"slices"
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

func TestAgentRoundTripKeepsVoice(t *testing.T) {
	withTempHome(t)
	if got := config.Agent(); got != "" {
		t.Errorf("Agent() on a fresh home = %q, want empty", got)
	}
	if err := config.SetDefaultVoice("companion"); err != nil {
		t.Fatal(err)
	}
	if err := config.SetAgent(" codex "); err != nil {
		t.Fatalf("SetAgent: %v", err)
	}
	if got := config.Agent(); got != "codex" {
		t.Errorf("Agent() = %q, want codex", got)
	}
	if got := config.DefaultVoice(); got != "companion" {
		t.Errorf("SetAgent clobbered the default voice: %q", got)
	}
}

func TestAgentPathExpandsAndFilters(t *testing.T) {
	withTempHome(t)
	if got := config.AgentPath(); got != nil {
		t.Errorf("AgentPath() on a fresh home = %v, want nil", got)
	}
	if err := config.SetAgent("claude"); err != nil {
		t.Fatal(err)
	}
	if err := config.SetAgentPath([]string{" ~/bin ", "", "/opt/homebrew/bin/", "relative/dir", "~"}); err != nil {
		t.Fatalf("SetAgentPath: %v", err)
	}
	home, _ := os.UserHomeDir()
	want := []string{filepath.Join(home, "bin"), "/opt/homebrew/bin", home}
	if got := config.AgentPath(); !slices.Equal(got, want) {
		t.Errorf("AgentPath() = %v, want %v", got, want)
	}
	if got := config.Agent(); got != "claude" {
		t.Errorf("SetAgentPath clobbered the agent: %q", got)
	}
	if err := config.SetAgentPath(nil); err != nil {
		t.Fatal(err)
	}
	if got := config.AgentPath(); got != nil {
		t.Errorf("AgentPath() after clearing = %v, want nil", got)
	}
}

func TestAgentPathReadsHandEditedConfig(t *testing.T) {
	withTempHome(t)
	dir, err := config.ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"agent":"codex","agent_path":["/usr/local/node/bin"],"future_field":true}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := config.AgentPath(); !slices.Equal(got, []string{"/usr/local/node/bin"}) {
		t.Errorf("AgentPath() = %v", got)
	}
}

func TestAgentPathUnreadableConfigIsNil(t *testing.T) {
	withTempHome(t)
	dir, err := config.ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := config.AgentPath(); got != nil {
		t.Errorf("AgentPath() with a broken config = %v, want nil", got)
	}
}
