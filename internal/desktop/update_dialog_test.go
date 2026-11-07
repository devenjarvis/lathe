package desktop

import (
	"errors"
	"strings"
	"testing"

	"github.com/devenjarvis/lathe/internal/shortcuts"
	"github.com/devenjarvis/lathe/internal/update"
)

func TestUpdateDialogFor(t *testing.T) {
	url := "https://github.com/devenjarvis/lathe/releases/tag/v0.9.0"
	cases := []struct {
		name   string
		res    update.Result
		err    error
		title  string
		action string
		arg    string
		has    string
	}{
		{"dev", update.Result{Current: "dev", Dev: true}, update.ErrDev, "Development build", "", "", "development build of Lathe (dev)"},
		{"offline", update.Result{Current: "0.8.0"}, errors.New("dial tcp: no route"), "Couldn't check for updates", updateActDownload, update.ReleasesPage, "no route"},
		{"current", update.Result{Current: "v0.9.0"}, nil, "You're up to date", "", "", "Lathe 0.9.0 is the newest"},
		{"download", update.Result{Current: "0.8.0", Notice: &update.Notice{Kind: update.KindDownload, Version: "0.9.0", Label: "0.9", URL: url}}, nil,
			"Lathe 0.9 is available", updateActDownload, url, "You have Lathe 0.8.0"},
		{"homebrew", update.Result{Current: "0.8.0", Notice: &update.Notice{Kind: update.KindHomebrew, Version: "0.9.0", Label: "0.9", URL: url, Command: update.BrewCommand}}, nil,
			"Lathe 0.9 is available", updateActCopy, update.BrewCommand, "brew upgrade --cask lathe"},
		{"restart", update.Result{Current: "0.8.0", Notice: &update.Notice{Kind: update.KindRestart, Version: "0.9.0", Label: "0.9"}}, nil,
			"Lathe 0.9 is ready", updateActRestart, "", "Restart Lathe"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := updateDialogFor(c.res, c.err)
			if d.Title != c.title || d.Action != c.action || d.Arg != c.arg {
				t.Errorf("dialog = %+v", d)
			}
			if !strings.Contains(d.Message, c.has) {
				t.Errorf("message %q lacks %q", d.Message, c.has)
			}
			if d.Dismiss == "" {
				t.Error("no dismiss button")
			}
			if (d.Action == "") != (d.Button == "") {
				t.Errorf("button %q with action %q", d.Button, d.Action)
			}
			if d.Warning != (c.err != nil && !errors.Is(c.err, update.ErrDev)) {
				t.Errorf("warning = %v", d.Warning)
			}
		})
	}
}

// Check for updates… is a built shell command in the Lathe menu on every OS,
// with no accelerator.
func TestMenuPlanCheckForUpdates(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		var found *menuEntry
		for _, sec := range menuPlan(goos) {
			if sec.title != AppName {
				continue
			}
			for i, e := range sec.entries {
				if e.cmd.ID == "app.updates" {
					found = &sec.entries[i]
				}
			}
		}
		if found == nil {
			t.Fatalf("%s: no Check for updates… in the %s menu", goos, AppName)
		}
		if found.cmd.Stub || found.cmd.Handler != shortcuts.HandlerShell || found.accel != "" || found.label != "Check for updates…" {
			t.Errorf("%s: Check for updates… = %+v", goos, *found)
		}
	}
}
