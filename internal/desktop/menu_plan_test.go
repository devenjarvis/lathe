package desktop

import (
	"slices"
	"strings"
	"testing"

	"github.com/devenjarvis/lathe/internal/shortcuts"
)

func flatten(entries []menuEntry) []menuEntry {
	var out []menuEntry
	for _, e := range entries {
		if e.submenu != "" {
			out = append(out, flatten(e.children)...)
			continue
		}
		out = append(out, e)
	}
	return out
}

func TestMenuPlanBars(t *testing.T) {
	for goos, want := range map[string][]string{
		"darwin":  {"Lathe", "File", "Edit", "View", "Tutorial", "Agent", "Window"},
		"linux":   {"Lathe", "File", "View", "Tutorial", "Agent", "Window"},
		"windows": {"Lathe", "File", "View", "Tutorial", "Agent", "Window"},
	} {
		var got []string
		for _, sec := range menuPlan(goos) {
			got = append(got, sec.title)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: menus %v, want %v", goos, got, want)
		}
	}
}

// Every registry command is in the bar exactly once, stubs bind no
// accelerator, and no accelerator is bound twice (roles included).
func TestMenuPlanCoversTheRegistry(t *testing.T) {
	roleAccel := map[string]map[bool]string{
		roleQuit:  {true: "Cmd+Q", false: "Ctrl+Q"},
		roleClose: {true: "Cmd+W", false: "Ctrl+W"},
	}
	for _, goos := range []string{"darwin", "linux", "windows"} {
		mac := shortcuts.IsMac(goos)
		seen := map[string]int{}
		accels := map[string]string{}
		for _, sec := range menuPlan(goos) {
			for _, e := range flatten(sec.entries) {
				if e.sep {
					continue
				}
				a := e.accel
				if e.cmd.ID == "" {
					a = roleAccel[e.role][mac]
				} else {
					seen[e.cmd.ID]++
					if e.cmd.Stub && e.accel != "" {
						t.Errorf("%s: stub %s binds %s", goos, e.cmd.ID, e.accel)
					}
					if e.cmd.Handler == shortcuts.HandlerNative && e.role == "" {
						t.Errorf("%s: %s should be a toolkit role", goos, e.cmd.ID)
					}
					if e.role != "" {
						a = roleAccel[e.role][mac]
					}
				}
				if a == "" {
					continue
				}
				k := strings.ToLower(a)
				if prev, dup := accels[k]; dup {
					t.Errorf("%s: %s bound twice (%s)", goos, a, prev)
				}
				accels[k] = e.cmd.ID + e.role
			}
		}
		for _, s := range shortcuts.All() {
			if seen[s.ID] != 1 {
				t.Errorf("%s: %s appears %d times", goos, s.ID, seen[s.ID])
			}
		}
	}
}

func TestMenuPlanDetails(t *testing.T) {
	var view menuSection
	var lathe menuSection
	for _, sec := range menuPlan("darwin") {
		switch sec.title {
		case "View":
			view = sec
		case "Lathe":
			lathe = sec
		}
	}
	var appearance *menuEntry
	for i, e := range view.entries {
		if e.submenu == "Appearance" {
			appearance = &view.entries[i]
		}
	}
	if appearance == nil || len(appearance.children) != 2 {
		t.Fatalf("View has no Appearance submenu with Light and Dark: %+v", view.entries)
	}
	var roles []string
	for _, e := range lathe.entries {
		if e.role != "" {
			roles = append(roles, e.role)
		}
	}
	if !slices.Equal(roles, []string{roleServices, roleHide, roleHideApps, roleShowAll, roleQuit}) {
		t.Errorf("macOS app menu roles %v", roles)
	}
	for _, sec := range menuPlan("linux") {
		if sec.title == "Lathe" {
			for _, e := range sec.entries {
				if e.role == roleHide || e.role == roleServices {
					t.Errorf("Linux app menu has macOS role %s", e.role)
				}
			}
		}
		if sec.title == "Tutorial" {
			for _, e := range sec.entries {
				if e.cmd.ID == "tutorial.workspace" && e.label != "Show workspace folder" {
					t.Errorf("Linux workspace label %q", e.label)
				}
				if e.cmd.ID == "tutorial.verify" && e.accel != "" {
					t.Errorf("Linux Verify binds %q (Ctrl+Shift+V is paste as plain text)", e.accel)
				}
			}
		}
	}
}
