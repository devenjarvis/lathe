// Package shortcuts is Lathe's one registry of commands and their default
// keyboard shortcuts. The desktop app's menu bar (internal/desktop), the
// in-page key handlers (internal/serve/shortcuts.js, fed the same list as
// JSON), the Go to anything palette and Settings › Shortcuts all read it, so
// a label or a combo is changed in one place.
//
// It has no Wails or HTTP dependencies, so the plain `lathe serve` build and
// tests use it directly.
//
// # Combos
//
// Each command has a default combo per platform family: Mac (Cmd, Option,
// Shift, Ctrl) and Other, for Windows and Linux (Ctrl, Alt, Shift). They are
// written in the accelerator syntax Wails takes, "Cmd+Shift+K" and
// "Ctrl+Shift+K", with one key after the modifiers: a single character or
// Enter / Home / Backspace. Every default was checked against the system and toolkit
// shortcuts in Reserved; the notes on each command say what was avoided.
package shortcuts

import (
	"encoding/json"
	"net/url"
	"strings"
)

// Scope is where a command applies.
type Scope string

const (
	// ScopeGlobal commands work on every page.
	ScopeGlobal Scope = "global"
	// ScopeHome commands work only on the tutorial list (/).
	ScopeHome Scope = "home"
	// ScopeTutorial commands work only on a tutorial's pages
	// (/{slug}/ and /{slug}/{part}, the agent pop-out included).
	ScopeTutorial Scope = "tutorial"
	// ScopeApp is the scope of Lathe's own app pages under /-/ (Settings):
	// only global commands apply there. No command has this scope.
	ScopeApp Scope = "app"
)

// Group is the heading a command is listed under in Settings › Shortcuts.
type Group string

const (
	GroupLibrary Group = "Library"
	GroupAgent   Group = "Agent"
	GroupReading Group = "Reading"
	GroupWindows Group = "Windows"
)

// groupOrder is the order of Settings › Shortcuts' sections.
var groupOrder = []Group{GroupLibrary, GroupAgent, GroupReading, GroupWindows}

// Menu is the desktop menu a command sits in.
type Menu string

const (
	MenuLathe    Menu = "Lathe"
	MenuFile     Menu = "File"
	MenuView     Menu = "View"
	MenuTutorial Menu = "Tutorial"
	MenuAgent    Menu = "Agent"
	MenuWindow   Menu = "Window"
)

// MenuOrder is the menu bar, left to right (the desktop app also adds the
// toolkit's Edit menu after File, which copy and paste need on macOS).
var MenuOrder = []Menu{MenuLathe, MenuFile, MenuView, MenuTutorial, MenuAgent, MenuWindow}

// Handler says who carries a command out.
type Handler string

const (
	// HandlerPage commands run in the page: the in-page key handler runs
	// them directly, and a desktop menu click reaches the focused page
	// through the server (GET /-/native/commands).
	HandlerPage Handler = "page"
	// HandlerShell commands are the desktop shell's own (About, the
	// Settings window). In a plain browser, those with an Href navigate
	// there instead.
	HandlerShell Handler = "shell"
	// HandlerNative commands are toolkit roles (Close, Quit): the menu item
	// is the OS's own and the page never binds the combo, so a browser keeps
	// its own meaning for it.
	HandlerNative Handler = "native"
)

// Shortcut is one command.
type Shortcut struct {
	// ID is stable: settings, the palette and the page key handlers refer
	// to commands by it.
	ID    string `json:"id"`
	Label string `json:"label"`
	// OtherLabel, when set, replaces Label on Windows and Linux ("Show
	// workspace in Finder" names a macOS app).
	OtherLabel string `json:"otherLabel,omitempty"`
	Menu       Menu   `json:"menu"`
	// Submenu, when set, nests the item one level down in Menu
	// ("Appearance").
	Submenu string  `json:"submenu,omitempty"`
	Group   Group   `json:"group"`
	Scope   Scope   `json:"scope"`
	Handler Handler `json:"handler"`
	// Mac and Other are the default combos ("" for none).
	Mac   string `json:"mac,omitempty"`
	Other string `json:"other,omitempty"`
	// Href is a same-origin path for commands that are a navigation: the
	// page (or, for shell commands, a plain browser) goes there.
	Href string `json:"href,omitempty"`
	// Stub marks a command whose feature isn't built yet. It is listed (in
	// the menu, disabled, and in Settings) but bound nowhere, so its combo
	// keeps whatever meaning the browser or toolkit gives it until then.
	Stub bool `json:"stub,omitempty"`
	// Note records the shortcut conflicts considered for the defaults.
	Note string `json:"note,omitempty"`
}

// registry is the list, in menu order. Keep IDs stable.
var registry = []Shortcut{
	// Lathe
	{ID: "app.about", Label: "About Lathe", Menu: MenuLathe, Group: GroupWindows, Scope: ScopeGlobal, Handler: HandlerShell},
	{ID: "app.settings", Label: "Settings…", Menu: MenuLathe, Group: GroupWindows, Scope: ScopeGlobal, Handler: HandlerShell,
		Mac: "Cmd+,", Other: "Ctrl+,", Href: "/-/settings",
		Note: "⌘, is the macOS settings convention; Ctrl+, is free on Windows, GNOME and KDE."},
	{ID: "app.updates", Label: "Check for updates…", Menu: MenuLathe, Group: GroupWindows, Scope: ScopeGlobal, Handler: HandlerShell, Stub: true},
	{ID: "app.quit", Label: "Quit Lathe", Menu: MenuLathe, Group: GroupWindows, Scope: ScopeGlobal, Handler: HandlerNative,
		Mac: "Cmd+Q", Other: "Ctrl+Q"},

	// File
	{ID: "library.new", Label: "New tutorial", Menu: MenuFile, Group: GroupLibrary, Scope: ScopeGlobal, Handler: HandlerPage, Stub: true,
		Mac: "Cmd+N", Other: "Ctrl+N",
		Note: "Browsers reserve ⌘N/Ctrl+N (new window), so in plain lathe serve only the app gets it."},
	{ID: "library.open-window", Label: "Open in new window", Menu: MenuFile, Group: GroupWindows, Scope: ScopeGlobal, Handler: HandlerPage,
		Mac: "Cmd+Enter",
		Note: "Opens the focused cover, or the ⌘K result, in its own window; the page enables it only then (never in a text field, so the agent composer keeps ⌘↩). " +
			"⌘-click (Ctrl+click) on a cover does the same. No default on Windows and Linux: Windows fires a menu accelerator even when the item is disabled, which would take Ctrl+Enter from the composer; the ⌘K palette handles Ctrl+Enter itself."},
	{ID: "library.trash", Label: "Move to Trash", Menu: MenuFile, Group: GroupLibrary, Scope: ScopeHome, Handler: HandlerPage,
		Mac: "Cmd+Backspace",
		Note: "Finder's Move to Trash. The page enables it only while a cover (or its ⋯ menu) has focus, never in a text field, where ⌘⌫ deletes to the line start. " +
			"No default on Windows and Linux: Delete and Ctrl+Backspace edit text, and Windows fires a menu accelerator even when the item is disabled. " +
			"Its Undo is not a command here: ⌘Z/Ctrl+Z is the toolkit's Edit › Undo (Reserved), so the library's Trash toast handles the key itself, only while it offers Undo and focus is not in a text field."},
	{ID: "window.close", Label: "Close", Menu: MenuFile, Group: GroupWindows, Scope: ScopeGlobal, Handler: HandlerNative,
		Mac: "Cmd+W", Other: "Ctrl+W"},

	// View
	{ID: "library.home", Label: "Home", Menu: MenuView, Group: GroupLibrary, Scope: ScopeGlobal, Handler: HandlerPage,
		Mac: "Cmd+Shift+H", Other: "Alt+Home", Href: "/",
		Note: "The browsers' own Home combos (⇧⌘H on macOS, Alt+Home on Windows and Linux); neither is a system shortcut."},
	{ID: "library.goto", Label: "Go to anything…", Menu: MenuView, Group: GroupLibrary, Scope: ScopeGlobal, Handler: HandlerPage,
		Mac: "Cmd+K", Other: "Ctrl+K",
		Note: "Ctrl+K is the browsers' search-bar key (Firefox, Chrome); the page can take it."},
	{ID: "reading.bigger", Label: "Bigger text", Menu: MenuView, Group: GroupReading, Scope: ScopeGlobal, Handler: HandlerPage,
		Mac: "Cmd+=", Other: "Ctrl+=",
		Note: "The browser zoom keys; the app has no zoom menu, so they are free there. In a browser the page leaves them to the browser's zoom."},
	{ID: "reading.smaller", Label: "Smaller text", Menu: MenuView, Group: GroupReading, Scope: ScopeGlobal, Handler: HandlerPage,
		Mac: "Cmd+-", Other: "Ctrl+-"},
	{ID: "reading.actualsize", Label: "Default text size", Menu: MenuView, Group: GroupReading, Scope: ScopeGlobal, Handler: HandlerPage,
		Mac: "Cmd+0", Other: "Ctrl+0",
		Note: "The browser's reset-zoom key; app only, like Bigger and Smaller text."},
	{ID: "reading.appearance.light", Label: "Light", Menu: MenuView, Submenu: "Appearance", Group: GroupReading, Scope: ScopeGlobal, Handler: HandlerPage},
	{ID: "reading.appearance.dark", Label: "Dark", Menu: MenuView, Submenu: "Appearance", Group: GroupReading, Scope: ScopeGlobal, Handler: HandlerPage},

	// Tutorial
	{ID: "reading.next", Label: "Next part", Menu: MenuTutorial, Group: GroupReading, Scope: ScopeTutorial, Handler: HandlerPage,
		Mac: "Cmd+]", Other: "Ctrl+]",
		Note: "⌘]/⌘[ are Forward/Back in macOS browsers (Safari keeps them); no system meaning on Windows, GNOME or KDE."},
	{ID: "reading.prev", Label: "Previous part", Menu: MenuTutorial, Group: GroupReading, Scope: ScopeTutorial, Handler: HandlerPage,
		Mac: "Cmd+[", Other: "Ctrl+["},
	{ID: "tutorial.verify", Label: "Verify", Menu: MenuTutorial, Group: GroupAgent, Scope: ScopeTutorial, Handler: HandlerPage,
		Mac:  "Cmd+Shift+V",
		Note: "No default on Windows and Linux: Ctrl+Shift+V is paste as plain text there (Chromium/WebView2, GTK, terminals), and a menu accelerator would take it from the composer."},
	{ID: "tutorial.extend", Label: "Write next part", Menu: MenuTutorial, Group: GroupAgent, Scope: ScopeTutorial, Handler: HandlerPage,
		Mac: "Cmd+Shift+N", Other: "Ctrl+Shift+N",
		Note: "New folder in Finder/Explorer only; browsers reserve it for a private window, so in plain lathe serve only the app gets it."},
	{ID: "tutorial.workspace", Label: "Show workspace in Finder", OtherLabel: "Show workspace folder", Menu: MenuTutorial, Group: GroupLibrary, Scope: ScopeTutorial, Handler: HandlerShell, Stub: true},

	// Agent
	{ID: "agent.panel", Label: "Show panel", Menu: MenuAgent, Group: GroupAgent, Scope: ScopeTutorial, Handler: HandlerPage,
		Mac: "Cmd+J", Other: "Ctrl+J",
		Note: "Ctrl+J is Downloads in Windows and Linux browsers (the page can take it); free in the app."},
	{ID: "agent.ask-selection", Label: "Ask about selection", Menu: MenuAgent, Group: GroupAgent, Scope: ScopeTutorial, Handler: HandlerPage, Stub: true,
		Mac: "Cmd+L", Other: "Ctrl+L",
		Note: "⌘L/Ctrl+L focuses the address bar in browsers; unbound while a stub, so the browser keeps it."},
	{ID: "agent.check", Label: "Check my work", Menu: MenuAgent, Group: GroupAgent, Scope: ScopeTutorial, Handler: HandlerPage,
		Mac: "Cmd+Shift+K", Other: "Ctrl+Shift+K",
		Note: "Firefox's web console on Windows and Linux (browser only); free in the app and in the OS."},
	{ID: "agent.popout", Label: "Pop out", Menu: MenuAgent, Group: GroupAgent, Scope: ScopeTutorial, Handler: HandlerPage,
		Mac: "Cmd+Option+J", Other: "Ctrl+Shift+J",
		Note: "Not Ctrl+Alt+J on Windows/Linux: Ctrl+Alt is AltGr on many layouts. ⌥⌘J/Ctrl+Shift+J are browser devtools consoles, so in plain lathe serve the browser may keep them."},
	{ID: "agent.settings", Label: "Agent settings…", Menu: MenuAgent, Group: GroupAgent, Scope: ScopeGlobal, Handler: HandlerShell,
		Href: "/-/settings#agent"},
}

// Reserved are combos a default must not take, per platform family: system
// and window-manager shortcuts, and the toolkit roles the desktop menu
// carries (Edit: undo/redo/cut/copy/paste/select all; Window: minimize;
// macOS app menu: hide). Commands that are those roles themselves
// (HandlerNative) are exempt. TestDefaultsAvoidReserved enforces it.
var Reserved = map[string][]string{
	"mac": {
		"Cmd+Q", "Cmd+W", "Cmd+H", "Cmd+Option+H", "Cmd+M", "Cmd+Tab", "Cmd+Space", "Cmd+`",
		"Cmd+Z", "Cmd+Shift+Z", "Cmd+X", "Cmd+C", "Cmd+V", "Cmd+A", "Cmd+Option+Shift+V",
		"Cmd+F", "Cmd+G", "Cmd+Shift+G", "Cmd+P", "Cmd+S", "Cmd+O", "Cmd+E",
		"Cmd+Shift+3", "Cmd+Shift+4", "Cmd+Shift+5", "Cmd+Ctrl+F", "Cmd+Ctrl+Q", "Cmd+Ctrl+Space",
		"Cmd+Option+D", "Cmd+Option+Escape", "Cmd+Shift+/", "Cmd+Shift+Q",
	},
	"other": {
		"Ctrl+Q", "Ctrl+W", "Ctrl+Z", "Ctrl+Y", "Ctrl+Shift+Z", "Ctrl+X", "Ctrl+C", "Ctrl+V", "Ctrl+A",
		"Ctrl+Shift+V", "Ctrl+Shift+C", "Ctrl+Shift+U", "Ctrl+F", "Ctrl+P", "Ctrl+S", "Ctrl+O",
		"Ctrl+Shift+Escape", "Ctrl+Escape", "Alt+F4", "Alt+Tab", "Alt+Space", "Ctrl+Alt+Delete",
		"Ctrl+Alt+T", "Ctrl+Alt+L", "Ctrl+Alt+D", "Ctrl+Space", "Ctrl+Shift+I",
	},
}

// All returns the registry, in menu order. The slice is a copy.
func All() []Shortcut {
	return append([]Shortcut(nil), registry...)
}

// ByID returns the command with id.
func ByID(id string) (Shortcut, bool) {
	for _, s := range registry {
		if s.ID == id {
			return s, true
		}
	}
	return Shortcut{}, false
}

// GroupList is one section of Settings › Shortcuts.
type GroupList struct {
	Group     Group      `json:"group"`
	Shortcuts []Shortcut `json:"shortcuts"`
}

// Groups returns the registry grouped for Settings › Shortcuts (Library,
// Agent, Reading, Windows), each group in menu order.
func Groups() []GroupList {
	out := make([]GroupList, 0, len(groupOrder))
	for _, g := range groupOrder {
		gl := GroupList{Group: g}
		for _, s := range registry {
			if s.Group == g {
				gl.Shortcuts = append(gl.Shortcuts, s)
			}
		}
		out = append(out, gl)
	}
	return out
}

// InMenu returns the commands of one menu, in order.
func InMenu(m Menu) []Shortcut {
	var out []Shortcut
	for _, s := range registry {
		if s.Menu == m {
			out = append(out, s)
		}
	}
	return out
}

// IsMac reports whether goos ("darwin", "linux", ...) uses the Mac combos.
func IsMac(goos string) bool { return goos == "darwin" || goos == "ios" }

// Combo is the default combo on goos, or "".
func (s Shortcut) Combo(goos string) string {
	if IsMac(goos) {
		return s.Mac
	}
	return s.Other
}

// LabelFor is the command's label on goos.
func (s Shortcut) LabelFor(goos string) string {
	if !IsMac(goos) && s.OtherLabel != "" {
		return s.OtherLabel
	}
	return s.Label
}

// Display renders the combo on goos the way that platform's menus show it:
// "⇧⌘K" on macOS, "Ctrl+Shift+K" elsewhere. "" when there is none.
func (s Shortcut) Display(goos string) string {
	return DisplayCombo(s.Combo(goos), IsMac(goos))
}

// DisplayCombo renders combo for a menu or a settings list.
func DisplayCombo(combo string, mac bool) string {
	if combo == "" {
		return ""
	}
	mods, key := SplitCombo(combo)
	if !mac {
		parts := append(mods, key)
		return strings.Join(parts, "+")
	}
	var b strings.Builder
	// macOS orders modifiers ⌃⌥⇧⌘.
	for _, m := range []struct{ name, glyph string }{{"Ctrl", "⌃"}, {"Option", "⌥"}, {"Shift", "⇧"}, {"Cmd", "⌘"}} {
		for _, have := range mods {
			if have == m.name {
				b.WriteString(m.glyph)
			}
		}
	}
	switch key {
	case "Enter":
		key = "↩"
	case "Home":
		key = "↖"
	case "Backspace":
		key = "⌫"
	}
	b.WriteString(key)
	return b.String()
}

// SplitCombo splits "Cmd+Shift+K" into its modifiers and key. The key may be
// "+" or "-" itself ("Cmd+-").
func SplitCombo(combo string) (mods []string, key string) {
	if combo == "" {
		return nil, ""
	}
	i := strings.LastIndex(combo[:len(combo)-1], "+")
	if i < 0 {
		return nil, combo
	}
	key = combo[i+1:]
	if head := combo[:i]; head != "" {
		mods = strings.Split(head, "+")
	}
	return mods, key
}

// Allowed reports whether the command applies on a page of scope.
func (s Shortcut) Allowed(scope Scope) bool {
	return s.Scope == ScopeGlobal || s.Scope == scope
}

// ScopeForPath is the scope of the page at path (a same-origin path, query
// allowed): "/" is home, "/-/..." an app page, anything else a tutorial.
// shortcuts.js decides the same way.
func ScopeForPath(path string) Scope {
	if u, err := url.Parse(path); err == nil {
		path = u.Path
	}
	switch {
	case path == "" || path == "/":
		return ScopeHome
	case strings.HasPrefix(path, "/-/") || strings.HasPrefix(path, "/_static/"):
		return ScopeApp
	default:
		return ScopeTutorial
	}
}

// pageList is what the page reads (GET /-/shortcuts and the inlined
// #latheShortcuts script).
type pageList struct {
	Shortcuts []Shortcut  `json:"shortcuts"`
	Groups    []GroupList `json:"groups"`
}

// JSON is the registry as the page reads it: {"shortcuts": [...], "groups":
// [...]}. encoding/json escapes <, > and &, so it is safe inside a <script>.
func JSON() []byte {
	b, err := json.Marshal(pageList{Shortcuts: All(), Groups: Groups()})
	if err != nil {
		// Only strings and slices: Marshal cannot fail.
		panic("shortcuts: " + err.Error())
	}
	return b
}
