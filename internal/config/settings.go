package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// The Settings window (lathe serve's /-/settings) reads and writes the keys
// below through LoadSettings and UpdateConfig. Every key has a default, so a
// missing or unreadable config.json still yields a complete Settings.

// DefaultCodeRoot is where tutorial workspaces go when code_root is unset.
const DefaultCodeRoot = "~/code"

// DefaultTrashDays is how long a deleted tutorial stays in the Trash when
// trash_days is unset.
const DefaultTrashDays = 30

// TrashDayChoices are the Trash retention settings, in days; 0 is "Never"
// (keep until deleted by hand).
var TrashDayChoices = []int{7, 30, 0}

// Appearance values.
const (
	AppearanceSystem = "system"
	AppearanceLight  = "light"
	AppearanceDark   = "dark"
)

// Appearances lists the appearance values in display order.
var Appearances = []string{AppearanceSystem, AppearanceLight, AppearanceDark}

// TextSizes lists the reading text sizes, smallest first. "default" is the
// out-of-box size; ⌘+ and ⌘− step through this list.
var TextSizes = []string{"small", "default", "large", "larger", "largest"}

// LineWidths lists the reading measures, narrowest first.
var LineWidths = []string{"narrow", "default", "wide"}

// Settings is the resolved view of config.json's Settings-window keys, with
// every default applied.
type Settings struct {
	// Appearance is "", system, light or dark. "" means the reader never
	// chose, which pages treat as "use the browser's saved toggle, else the
	// system".
	Appearance       string `json:"appearance"`
	TextSize         string `json:"text_size"`
	LineWidth        string `json:"line_width"`
	RememberPlace    bool   `json:"remember_place"`
	PanelSuggestions bool   `json:"panel_suggestions"`
	PanelWindow      bool   `json:"panel_window"`
	CodeRoot         string `json:"code_root"`
	KeepVerifiedCode bool   `json:"keep_verified_code"`
	AutoVerify       bool   `json:"auto_verify"`
	NotifyVerify     bool   `json:"notify_verify"`
	NotifyExtend     bool   `json:"notify_extend"`
	NotifyCheck      bool   `json:"notify_check"`
	NotifyNeedsYou   bool   `json:"notify_needs_you"`
	// TrashDays is the Trash retention in days; 0 means never purge.
	TrashDays int `json:"trash_days"`
}

// Resolve applies the defaults to c's Settings-window keys. An unknown value
// (a hand edit) resolves to the default rather than failing.
func (c *Config) Resolve() Settings {
	s := Settings{
		Appearance:       "",
		TextSize:         "default",
		LineWidth:        "default",
		RememberPlace:    on(c.RememberPlace),
		PanelSuggestions: on(c.PanelSuggestions),
		PanelWindow:      c.PanelWindow,
		CodeRoot:         DefaultCodeRoot,
		KeepVerifiedCode: on(c.KeepVerifiedCode),
		AutoVerify:       c.AutoVerify,
		NotifyVerify:     true,
		NotifyExtend:     true,
		NotifyCheck:      true,
		NotifyNeedsYou:   true,
		TrashDays:        DefaultTrashDays,
	}
	if c.TrashDays != nil && slices.Contains(TrashDayChoices, *c.TrashDays) {
		s.TrashDays = *c.TrashDays
	}
	if v := strings.TrimSpace(c.Appearance); slices.Contains(Appearances, v) {
		s.Appearance = v
	}
	if v := strings.TrimSpace(c.TextSize); slices.Contains(TextSizes, v) {
		s.TextSize = v
	}
	if v := strings.TrimSpace(c.LineWidth); slices.Contains(LineWidths, v) {
		s.LineWidth = v
	}
	if v := strings.TrimSpace(c.CodeRoot); v != "" {
		s.CodeRoot = v
	}
	if n := c.Notify; n != nil {
		s.NotifyVerify, s.NotifyExtend, s.NotifyCheck = on(n.Verify), on(n.Extend), on(n.Check)
		s.NotifyNeedsYou = on(n.NeedsYou)
	}
	return s
}

func on(b *bool) bool { return b == nil || *b }

// LoadSettings reads config.json and resolves its Settings-window keys. A
// missing or unreadable file yields the defaults: a bad config must never
// stop a page from rendering.
func LoadSettings() Settings {
	c, err := ReadConfig()
	if err != nil {
		c = &Config{}
	}
	return c.Resolve()
}

// writeMu serializes UpdateConfig's read-modify-write, so two settings saved
// at once (two windows) can't drop each other's change.
var writeMu sync.Mutex

// UpdateConfig reads config.json, lets fn change it, and writes it back
// unless fn returns an error.
func UpdateConfig(fn func(*Config) error) error {
	writeMu.Lock()
	defer writeMu.Unlock()
	c, err := ReadConfig()
	if err != nil {
		return err
	}
	if err := fn(c); err != nil {
		return err
	}
	return WriteConfig(c)
}

// Bool returns a pointer to b, for the default-on keys.
func Bool(b bool) *bool { return &b }

// ValidateChoice reports an error unless v is one of choices.
func ValidateChoice(key, v string, choices []string) error {
	if slices.Contains(choices, v) {
		return nil
	}
	return fmt.Errorf("%s must be one of %s", key, strings.Join(choices, ", "))
}

// ExpandHome turns a leading "~" or "~/" into the home dir. Other paths are
// returned cleaned.
func ExpandHome(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return filepath.Clean(p), nil
}

// CodeRootFor is the suggested workspace for slug: <code_root>/<slug>, in
// the "~/…" form the reader typed the root in.
func CodeRootFor(root, slug string) string {
	root = strings.TrimRight(strings.TrimSpace(root), "/")
	if root == "" {
		root = DefaultCodeRoot
	}
	return root + "/" + slug
}

// TrashDays is the Trash retention in days (0: never purge).
func TrashDays() int { return LoadSettings().TrashDays }

// KeepVerifiedCode reports whether a verify run's code snapshot is kept.
func KeepVerifiedCode() bool { return LoadSettings().KeepVerifiedCode }
