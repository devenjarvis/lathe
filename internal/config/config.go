package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// DefaultVoiceName is the out-of-box writing voice: the honest,
// non-anthropomorphic preset. An empty or missing default in config.json
// resolves to this.
const DefaultVoiceName = "plainspoken"

// ConfigDir returns ~/.lathe, creating it if needed. It is the root for all
// durable Lathe state the CLI owns (tutorials, voices, config.json).
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".lathe")
	return dir, os.MkdirAll(dir, 0755)
}

func TutorialsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".lathe", "tutorials")
	return dir, os.MkdirAll(dir, 0755)
}

// VoicesDir returns ~/.lathe/voices, creating it if needed. It holds the
// user-authored custom voice spec files (<name>.md), written by the
// `lathe voice add` command (which the /lathe-voice skill calls).
func VoicesDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".lathe", "voices")
	return dir, os.MkdirAll(dir, 0755)
}

// Config is the user-level configuration persisted at ~/.lathe/config.json.
// It is small on purpose — only settings that have no better home on an
// individual tutorial belong here.
type Config struct {
	// DefaultVoice is the voice applied to a `/lathe` run when the user names
	// none. Empty means the built-in DefaultVoiceName.
	DefaultVoice string `json:"default_voice,omitempty"`
	// Agent is the registry id (internal/agents) of the coding agent the
	// reader picked in the web UI; `lathe serve` hosts it when no --agent is
	// given. Empty means none picked yet (the panel shows the picker).
	Agent string `json:"agent,omitempty"`
}

func configPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// ReadConfig loads ~/.lathe/config.json. A missing file is not an error — it
// returns a zero Config (the documented defaults apply).
func ReadConfig() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// WriteConfig persists the config to ~/.lathe/config.json.
func WriteConfig(c *Config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// DefaultVoice returns the configured default voice name, falling back to
// DefaultVoiceName when unset (or when config.json is absent/unreadable —
// the default voice should always resolve, never block on config errors).
func DefaultVoice() string {
	c, err := ReadConfig()
	if err != nil {
		return DefaultVoiceName
	}
	if v := strings.TrimSpace(c.DefaultVoice); v != "" {
		return v
	}
	return DefaultVoiceName
}

// SetDefaultVoice records name as the default voice in config.json. The caller
// is responsible for validating that the voice exists before calling this.
func SetDefaultVoice(name string) error {
	c, err := ReadConfig()
	if err != nil {
		return err
	}
	c.DefaultVoice = strings.TrimSpace(name)
	return WriteConfig(c)
}

// Agent returns the picked agent's registry id, or "" when none is set or
// config.json can't be read (the picker then simply shows again).
func Agent() string {
	c, err := ReadConfig()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(c.Agent)
}

// SetAgent records id as the picked agent. The caller validates it against
// the registry.
func SetAgent(id string) error {
	c, err := ReadConfig()
	if err != nil {
		return err
	}
	c.Agent = strings.TrimSpace(id)
	return WriteConfig(c)
}
