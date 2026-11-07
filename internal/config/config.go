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
	// AgentPath lists extra directories to search for agent programs, ahead
	// of PATH. It is for installs the environment doesn't put on PATH, most
	// often a GUI launch (the desktop app opened from the dock), which doesn't
	// inherit the login shell's PATH. Entries may start with "~/". Set it by
	// editing config.json; internal/loginenv applies it.
	AgentPath []string `json:"agent_path,omitempty"`
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

// AgentPath returns the configured extra agent search directories with "~"
// expanded, blanks dropped and relative entries skipped (a relative PATH entry
// would resolve against whatever the cwd happens to be). It returns nil when
// none are set or config.json can't be read: a bad config must never stop an
// agent from starting with the PATH it already has.
func AgentPath() []string {
	c, err := ReadConfig()
	if err != nil {
		return nil
	}
	home, _ := os.UserHomeDir()
	var dirs []string
	for _, d := range c.AgentPath {
		d = strings.TrimSpace(d)
		if d == "~" || strings.HasPrefix(d, "~/") {
			if home == "" {
				continue
			}
			d = filepath.Join(home, strings.TrimPrefix(d, "~"))
		}
		if d == "" || !filepath.IsAbs(d) {
			continue
		}
		dirs = append(dirs, filepath.Clean(d))
	}
	return dirs
}

// SetAgentPath records dirs as the extra agent search directories, trimmed,
// with blanks dropped. An empty list clears the setting.
func SetAgentPath(dirs []string) error {
	c, err := ReadConfig()
	if err != nil {
		return err
	}
	var keep []string
	for _, d := range dirs {
		if d = strings.TrimSpace(d); d != "" {
			keep = append(keep, d)
		}
	}
	c.AgentPath = keep
	return WriteConfig(c)
}
