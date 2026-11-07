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

	// The Settings window's keys (see settings.go for the defaults each one
	// resolves to). Booleans that default to on are pointers, so a missing
	// key and an explicit false stay distinct.

	// CodeRoot is the folder new tutorials' workspaces go under
	// (<code_root>/<slug>). Empty means DefaultCodeRoot.
	CodeRoot string `json:"code_root,omitempty"`
	// KeepVerifiedCode keeps the per-part git snapshot a verify run passes
	// to `lathe verify-result --code`. Default on.
	KeepVerifiedCode *bool `json:"keep_verified_code,omitempty"`
	// Appearance is the theme every window uses: system, light or dark.
	// Empty means not chosen yet (pages fall back to the browser's saved
	// toggle, then the system setting).
	Appearance string `json:"appearance,omitempty"`
	// TextSize scales the reading text (one of TextSizes). Empty: default.
	TextSize string `json:"text_size,omitempty"`
	// LineWidth sets the reading measure (one of LineWidths). Empty: default.
	LineWidth string `json:"line_width,omitempty"`
	// RememberPlace reopens a tutorial at the part the reader saved. Default
	// on.
	RememberPlace *bool `json:"remember_place,omitempty"`
	// PanelSuggestions shows suggested questions in the agent panel. Default
	// on.
	PanelSuggestions *bool `json:"panel_suggestions,omitempty"`
	// PanelWindow opens the agent panel in its own window instead of docking
	// it beside the article. Default off.
	PanelWindow bool `json:"panel_window,omitempty"`
	// AutoVerify starts a Verify run on the hosted agent after a hosted
	// Extend run commits a new part. Default off: verification stays opt-in.
	AutoVerify bool `json:"auto_verify,omitempty"`
	// Notify picks which finished runs post a system notification (desktop
	// app only). Nil means all of them.
	Notify *NotifyConfig `json:"notify,omitempty"`
	// TrashDays is how many days a deleted tutorial stays in the Trash
	// before it is deleted for good: one of TrashDayChoices, 0 for never.
	// Nil means DefaultTrashDays.
	TrashDays *int `json:"trash_days,omitempty"`
	// UpdateCheck lets the desktop app look up the newest release on GitHub
	// at most once a day and show a notice when one is out. Default on.
	UpdateCheck *bool `json:"update_check,omitempty"`
}

// NotifyConfig is the per-event notification switches. Nil means on.
type NotifyConfig struct {
	Verify *bool `json:"verify,omitempty"`
	Extend *bool `json:"extend,omitempty"`
	Check  *bool `json:"check,omitempty"`
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
