package desktop

import (
	"github.com/devenjarvis/lathe/internal/shortcuts"
)

// The menu bar's layout, from the shortcut registry (internal/shortcuts).
// It is plain Go, so it is tested without the desktop tag; menu.go renders it
// with Wails.

// Toolkit roles the plan uses. menu.go maps them to application.Role.
const (
	roleQuit     = "quit"
	roleClose    = "close"
	roleServices = "services"
	roleHide     = "hide"
	roleHideApps = "hide-others"
	roleShowAll  = "show-all"
	roleMinimise = "minimise"
	roleZoom     = "zoom"
	// Whole-menu roles (macOS): the toolkit's own Edit and Window menus.
	roleEditMenu   = "edit-menu"
	roleWindowMenu = "window-menu"
)

// nativeRoles maps the registry's HandlerNative commands to their role.
var nativeRoles = map[string]string{
	"app.quit":     roleQuit,
	"window.close": roleClose,
}

// sepBefore are the commands a separator goes above.
var sepBefore = map[string]bool{
	"app.settings":             true,
	"app.quit":                 true,
	"window.close":             true,
	"reading.bigger":           true,
	"reading.appearance.light": true,
	"tutorial.verify":          true,
	"tutorial.open-editor":     true,
	"agent.settings":           true,
}

// menuEntry is one item of a menu: a separator, a toolkit role, a registry
// command, or a submenu of commands.
type menuEntry struct {
	sep  bool
	role string
	cmd  shortcuts.Shortcut
	// label is the command's label on this platform.
	label string
	// accel is the accelerator the menu binds, "" for none. Stubs bind
	// none, so their combo keeps its system or browser meaning until the
	// feature exists.
	accel string
	// submenu, when set, makes this entry a submenu of children.
	submenu  string
	children []menuEntry
}

// menuSection is one top-level menu. role, when set, is a whole toolkit menu
// (entries is then empty).
type menuSection struct {
	title   string
	role    string
	entries []menuEntry
}

func commandEntry(s shortcuts.Shortcut, goos string) menuEntry {
	e := menuEntry{cmd: s, label: s.LabelFor(goos)}
	if !s.Stub && s.Handler != shortcuts.HandlerNative {
		e.accel = s.Combo(goos)
	}
	if r, ok := nativeRoles[s.ID]; ok {
		e.role = r
	}
	return e
}

// menuPlan lays the menu bar out for goos.
func menuPlan(goos string) []menuSection {
	mac := shortcuts.IsMac(goos)
	var out []menuSection
	for _, m := range shortcuts.MenuOrder {
		sec := menuSection{title: string(m)}
		if m == shortcuts.MenuLathe {
			sec.title = AppName
		}
		if m == shortcuts.MenuWindow {
			if mac {
				sec.role = roleWindowMenu
			} else {
				// Not the toolkit's Window menu: off macOS it repeats
				// Close (Ctrl+W), which File already has.
				sec.entries = []menuEntry{{role: roleMinimise}, {role: roleZoom}}
			}
			out = append(out, sec)
			continue
		}
		for _, s := range shortcuts.InMenu(m) {
			if sepBefore[s.ID] && len(sec.entries) > 0 {
				if m == shortcuts.MenuLathe && s.ID == "app.quit" && mac {
					sec.entries = append(sec.entries,
						menuEntry{sep: true}, menuEntry{role: roleServices},
						menuEntry{sep: true}, menuEntry{role: roleHide}, menuEntry{role: roleHideApps}, menuEntry{role: roleShowAll})
				}
				sec.entries = append(sec.entries, menuEntry{sep: true})
			}
			e := commandEntry(s, goos)
			if s.Submenu != "" {
				n := len(sec.entries)
				if n > 0 && sec.entries[n-1].submenu == s.Submenu {
					sec.entries[n-1].children = append(sec.entries[n-1].children, e)
					continue
				}
				sec.entries = append(sec.entries, menuEntry{submenu: s.Submenu, children: []menuEntry{e}})
				continue
			}
			sec.entries = append(sec.entries, e)
		}
		out = append(out, sec)
		// Copy and paste in the macOS webview go through the Edit menu's
		// key equivalents, so the bar needs the toolkit's Edit menu there.
		// Windows and Linux webviews handle them themselves.
		if m == shortcuts.MenuFile && mac {
			out = append(out, menuSection{title: "Edit", role: roleEditMenu})
		}
	}
	return out
}
