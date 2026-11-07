package config_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/devenjarvis/lathe/internal/config"
)

func TestLoadSettingsDefaults(t *testing.T) {
	withTempHome(t)
	got := config.LoadSettings()
	want := config.Settings{
		Appearance: "", TextSize: "default", LineWidth: "default",
		RememberPlace: true, PanelSuggestions: true, PanelWindow: false,
		CodeRoot: config.DefaultCodeRoot, KeepVerifiedCode: true, AutoVerify: false,
		NotifyVerify: true, NotifyExtend: true, NotifyCheck: true, NotifyNeedsYou: true,
		TrashDays: config.DefaultTrashDays, UpdateCheck: true, ShelfSplitAt: config.DefaultShelfSplitAt,
	}
	if got != want {
		t.Errorf("LoadSettings() = %+v, want %+v", got, want)
	}
}

func TestLoadSettingsUnreadableConfigYieldsDefaults(t *testing.T) {
	withTempHome(t)
	dir, _ := config.ConfigDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := config.LoadSettings(); !got.RememberPlace || got.TextSize != "default" {
		t.Errorf("LoadSettings() on a bad file = %+v, want defaults", got)
	}
}

func TestSettingsRoundTripAndKeys(t *testing.T) {
	withTempHome(t)
	err := config.UpdateConfig(func(c *config.Config) error {
		c.Appearance = "dark"
		c.TextSize = "larger"
		c.LineWidth = "wide"
		c.CodeRoot = "~/src"
		c.RememberPlace = config.Bool(false)
		c.PanelSuggestions = config.Bool(false)
		c.KeepVerifiedCode = config.Bool(false)
		c.PanelWindow = true
		c.AutoVerify = true
		c.UpdateCheck = config.Bool(false)
		c.Notify = &config.NotifyConfig{Extend: config.Bool(false)}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got := config.LoadSettings()
	if got.Appearance != "dark" || got.TextSize != "larger" || got.LineWidth != "wide" || got.CodeRoot != "~/src" ||
		got.RememberPlace || got.PanelSuggestions || got.KeepVerifiedCode || !got.PanelWindow || !got.AutoVerify ||
		!got.NotifyVerify || got.NotifyExtend || !got.NotifyCheck || got.UpdateCheck || config.UpdateCheck() {
		t.Errorf("LoadSettings() = %+v", got)
	}
	if config.KeepVerifiedCode() {
		t.Error("KeepVerifiedCode() = true after turning it off")
	}
	dir, _ := config.ConfigDir()
	raw, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	for _, key := range []string{`"appearance"`, `"text_size"`, `"line_width"`, `"code_root"`, `"remember_place"`, `"panel_suggestions"`, `"keep_verified_code"`, `"panel_window"`, `"auto_verify"`, `"notify"`, `"extend"`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("config.json lacks %s:\n%s", key, raw)
		}
	}
}

func TestResolveIgnoresUnknownValues(t *testing.T) {
	c := &config.Config{Appearance: "sepia", TextSize: "huge", LineWidth: "infinite"}
	got := c.Resolve()
	if got.Appearance != "" || got.TextSize != "default" || got.LineWidth != "default" {
		t.Errorf("Resolve() = %+v, want defaults for unknown values", got)
	}
}

func TestUpdateConfigKeepsOtherKeys(t *testing.T) {
	withTempHome(t)
	if err := config.SetAgent("claude"); err != nil {
		t.Fatal(err)
	}
	if err := config.UpdateConfig(func(c *config.Config) error { c.TextSize = "large"; return nil }); err != nil {
		t.Fatal(err)
	}
	if config.Agent() != "claude" {
		t.Error("UpdateConfig dropped the agent")
	}
}

func TestCodeRootFor(t *testing.T) {
	for _, tc := range []struct{ root, want string }{
		{"", "~/code/synth"},
		{"~/src/", "~/src/synth"},
		{"/work", "/work/synth"},
	} {
		if got := config.CodeRootFor(tc.root, "synth"); got != tc.want {
			t.Errorf("CodeRootFor(%q) = %q, want %q", tc.root, got, tc.want)
		}
	}
}

func TestExpandHome(t *testing.T) {
	withTempHome(t)
	home, _ := os.UserHomeDir()
	if got, _ := config.ExpandHome("~/x/"); got != filepath.Join(home, "x") {
		t.Errorf("ExpandHome(~/x/) = %q", got)
	}
	if got, _ := config.ExpandHome("/abs"); got != "/abs" {
		t.Errorf("ExpandHome(/abs) = %q", got)
	}
}

func TestTrashDays(t *testing.T) {
	withTempHome(t)
	if got := config.TrashDays(); got != 30 {
		t.Errorf("default TrashDays = %d, want 30", got)
	}
	for _, c := range []struct{ stored, want int }{{7, 7}, {0, 0}, {30, 30}, {14, 30}, {-1, 30}} {
		v := c.stored
		if err := config.UpdateConfig(func(cfg *config.Config) error { cfg.TrashDays = &v; return nil }); err != nil {
			t.Fatal(err)
		}
		if got := config.TrashDays(); got != c.want {
			t.Errorf("trash_days %d resolves to %d, want %d", c.stored, got, c.want)
		}
	}
}

func TestShelfSplitAt(t *testing.T) {
	withTempHome(t)
	if got := config.ShelfSplitAt(); got != 12 {
		t.Errorf("default ShelfSplitAt = %d, want 12", got)
	}
	for _, c := range []struct{ stored, want int }{{8, 8}, {24, 24}, {0, 0}, {13, 12}, {-1, 12}} {
		v := c.stored
		if err := config.UpdateConfig(func(cfg *config.Config) error { cfg.ShelfSplitAt = &v; return nil }); err != nil {
			t.Fatal(err)
		}
		if got := config.ShelfSplitAt(); got != c.want {
			t.Errorf("shelf_split_at %d resolves to %d, want %d", c.stored, got, c.want)
		}
	}
}

func TestLaunchersResolve(t *testing.T) {
	withTempHome(t)
	err := config.UpdateConfig(func(c *config.Config) error {
		c.Editor = "zed"
		c.Terminal = config.TerminalsFor(runtime.GOOS)[0].ID
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if st := config.LoadSettings(); st.Editor != "zed" || st.Terminal != config.TerminalsFor(runtime.GOOS)[0].ID {
		t.Errorf("settings = %+v", st)
	}
	// Hand edits, or a config.json from another OS, fall back to the default.
	err = config.UpdateConfig(func(c *config.Config) error {
		c.Editor = "notepad; rm -rf ~"
		c.Terminal = "nope"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if st := config.LoadSettings(); st.Editor != "" || st.Terminal != "" {
		t.Errorf("unknown ids resolved to %+v", st)
	}
	for _, goos := range []string{"darwin", "linux", "windows"} {
		if len(config.TerminalsFor(goos)) == 0 {
			t.Errorf("no terminals for %s", goos)
		}
		if config.TerminalLabel(goos, "") == "" {
			t.Errorf("no default terminal label for %s", goos)
		}
	}
	if _, ok := config.TerminalByID("linux", "iterm"); ok {
		t.Error("iTerm offered on Linux")
	}
}

func TestNeedsYouToggleAndWindowsRoundTrip(t *testing.T) {
	withTempHome(t)
	err := config.UpdateConfig(func(c *config.Config) error {
		c.Notify = &config.NotifyConfig{NeedsYou: config.Bool(false)}
		c.Windows = []config.WindowState{
			{Main: true, Path: "/", Width: 1200, Height: 800},
			{Path: "/demo/part-02.md", Scroll: 640, X: 40, Y: 60, Width: 900, Height: 700},
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := config.LoadSettings(); got.NotifyNeedsYou || !got.NotifyVerify {
		t.Errorf("LoadSettings() = %+v, want needs-you off and the rest on", got)
	}
	c, err := config.ReadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Windows) != 2 || !c.Windows[0].Main || c.Windows[1].Path != "/demo/part-02.md" || c.Windows[1].Scroll != 640 {
		t.Errorf("Windows = %+v", c.Windows)
	}
}
