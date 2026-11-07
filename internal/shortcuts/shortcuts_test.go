package shortcuts

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestIDsUniqueAndComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range All() {
		if s.ID == "" || s.Label == "" {
			t.Errorf("%+v: needs an id and a label", s)
		}
		if seen[s.ID] {
			t.Errorf("duplicate id %q", s.ID)
		}
		seen[s.ID] = true
		if !slices.Contains(MenuOrder, s.Menu) {
			t.Errorf("%s: unknown menu %q", s.ID, s.Menu)
		}
		if !slices.Contains(groupOrder, s.Group) {
			t.Errorf("%s: unknown group %q", s.ID, s.Group)
		}
		switch s.Scope {
		case ScopeGlobal, ScopeHome, ScopeTutorial:
		default:
			t.Errorf("%s: unknown scope %q", s.ID, s.Scope)
		}
		switch s.Handler {
		case HandlerPage, HandlerShell, HandlerNative:
		default:
			t.Errorf("%s: unknown handler %q", s.ID, s.Handler)
		}
		if s.Href != "" && (!strings.HasPrefix(s.Href, "/") || strings.HasPrefix(s.Href, "//")) {
			t.Errorf("%s: href %q must be a same-origin path", s.ID, s.Href)
		}
		if got, ok := ByID(s.ID); !ok || got.ID != s.ID {
			t.Errorf("ByID(%q) = %v, %v", s.ID, got, ok)
		}
	}
	if _, ok := ByID("nope"); ok {
		t.Error("ByID found an unknown id")
	}
}

// The issue's menu list, so a rename or a dropped item is deliberate.
func TestMenusCarryTheIssueItems(t *testing.T) {
	want := map[Menu][]string{
		MenuLathe:    {"app.about", "app.settings", "app.updates"},
		MenuFile:     {"library.new", "library.open-window", "window.close"},
		MenuView:     {"library.home", "library.goto", "reading.bigger", "reading.smaller", "reading.appearance.light", "reading.appearance.dark"},
		MenuTutorial: {"reading.next", "reading.prev", "tutorial.verify", "tutorial.extend", "tutorial.workspace"},
		MenuAgent:    {"agent.panel", "agent.ask-selection", "agent.check", "agent.popout", "agent.settings"},
	}
	for m, ids := range want {
		var got []string
		for _, s := range InMenu(m) {
			got = append(got, s.ID)
		}
		for _, id := range ids {
			if !slices.Contains(got, id) {
				t.Errorf("menu %s lacks %s (has %v)", m, id, got)
			}
		}
	}
	// The issue's combos.
	for id, mac := range map[string]string{
		"app.settings": "Cmd+,", "library.new": "Cmd+N", "library.open-window": "Cmd+Enter", "window.close": "Cmd+W",
		"library.goto": "Cmd+K", "reading.next": "Cmd+]", "reading.prev": "Cmd+[",
		"agent.panel": "Cmd+J", "agent.ask-selection": "Cmd+L", "agent.check": "Cmd+Shift+K", "agent.popout": "Cmd+Option+J",
	} {
		if s, _ := ByID(id); s.Mac != mac {
			t.Errorf("%s: mac combo %q, want %q", id, s.Mac, mac)
		}
	}
}

func TestCombosWellFormed(t *testing.T) {
	macMods := []string{"Cmd", "Option", "Shift", "Ctrl"}
	otherMods := []string{"Ctrl", "Alt", "Shift"}
	for _, s := range All() {
		for _, c := range []struct {
			combo string
			mods  []string
			mac   bool
		}{{s.Mac, macMods, true}, {s.Other, otherMods, false}} {
			if c.combo == "" {
				continue
			}
			mods, key := SplitCombo(c.combo)
			if len(mods) == 0 {
				t.Errorf("%s: %q has no modifier", s.ID, c.combo)
			}
			for _, m := range mods {
				if !slices.Contains(c.mods, m) {
					t.Errorf("%s: %q uses modifier %q (mac=%v)", s.ID, c.combo, m, c.mac)
				}
			}
			if len(key) != 1 && key != "Enter" && key != "Home" && key != "Backspace" {
				t.Errorf("%s: %q: key %q must be one character, Enter, Home or Backspace", s.ID, c.combo, key)
			}
			if len(key) == 1 && key != strings.ToUpper(key) {
				t.Errorf("%s: %q: write letter keys upper-case", s.ID, c.combo)
			}
			if c.mac && !slices.Contains(mods, "Cmd") {
				t.Errorf("%s: mac combo %q should use Cmd", s.ID, c.combo)
			}
			if !c.mac && slices.Contains(mods, "Ctrl") && slices.Contains(mods, "Alt") {
				t.Errorf("%s: %q: Ctrl+Alt is AltGr on many Windows layouts", s.ID, c.combo)
			}
		}
	}
}

// Menu accelerators are app-wide, so a combo is unique per platform family
// whatever the scope (which also rules out two commands sharing a combo in
// one scope).
func TestNoDuplicateCombos(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		seen := map[string]string{}
		for _, s := range All() {
			c := strings.ToLower(s.Combo(goos))
			if c == "" {
				continue
			}
			if other, dup := seen[c]; dup {
				t.Errorf("%s: %s and %s share %s", goos, other, s.ID, c)
			}
			seen[c] = s.ID
		}
	}
}

func TestDefaultsAvoidReserved(t *testing.T) {
	for _, s := range All() {
		if s.Handler == HandlerNative {
			continue
		}
		for fam, combo := range map[string]string{"mac": s.Mac, "other": s.Other} {
			for _, r := range Reserved[fam] {
				if combo != "" && strings.EqualFold(combo, r) {
					t.Errorf("%s: %s default %s is reserved", s.ID, fam, combo)
				}
			}
		}
	}
}

func TestGroups(t *testing.T) {
	gs := Groups()
	var names []Group
	n := 0
	for _, g := range gs {
		names = append(names, g.Group)
		n += len(g.Shortcuts)
		for _, s := range g.Shortcuts {
			if s.Group != g.Group {
				t.Errorf("%s listed under %s", s.ID, g.Group)
			}
		}
	}
	if !slices.Equal(names, []Group{GroupLibrary, GroupAgent, GroupReading, GroupWindows}) {
		t.Errorf("groups %v", names)
	}
	if n != len(All()) {
		t.Errorf("groups hold %d commands, registry %d", n, len(All()))
	}
}

func TestScopeForPath(t *testing.T) {
	for path, want := range map[string]Scope{
		"/":                                ScopeHome,
		"":                                 ScopeHome,
		"/?q=x":                            ScopeHome,
		"/-/settings":                      ScopeApp,
		"/synth/":                          ScopeTutorial,
		"/synth/part-01.md":                ScopeTutorial,
		"/synth/part-01.md?agent=popout":   ScopeTutorial,
		"/synth/part-01.md#checkpoint-one": ScopeTutorial,
	} {
		if got := ScopeForPath(path); got != want {
			t.Errorf("ScopeForPath(%q) = %s, want %s", path, got, want)
		}
	}
	next, _ := ByID("reading.next")
	home, _ := ByID("library.home")
	if next.Allowed(ScopeHome) || next.Allowed(ScopeApp) || !next.Allowed(ScopeTutorial) {
		t.Error("tutorial commands apply on tutorial pages only")
	}
	if !home.Allowed(ScopeHome) || !home.Allowed(ScopeApp) || !home.Allowed(ScopeTutorial) {
		t.Error("global commands apply everywhere")
	}
}

func TestDisplay(t *testing.T) {
	check, _ := ByID("agent.check")
	pop, _ := ByID("agent.popout")
	smaller, _ := ByID("reading.smaller")
	for _, c := range []struct{ got, want string }{
		{check.Display("darwin"), "⇧⌘K"},
		{check.Display("linux"), "Ctrl+Shift+K"},
		{pop.Display("darwin"), "⌥⌘J"},
		{pop.Display("windows"), "Ctrl+Shift+J"},
		{smaller.Display("darwin"), "⌘-"},
		{smaller.Display("windows"), "Ctrl+-"},
		{DisplayCombo("Cmd+Enter", true), "⌘↩"},
		{DisplayCombo("Cmd+Backspace", true), "⌘⌫"},
		{DisplayCombo("", true), ""},
	} {
		if c.got != c.want {
			t.Errorf("display %q, want %q", c.got, c.want)
		}
	}
	ws, _ := ByID("tutorial.workspace")
	if ws.LabelFor("darwin") != "Show workspace in Finder" || ws.LabelFor("linux") != "Show workspace folder" {
		t.Errorf("workspace labels %q / %q", ws.LabelFor("darwin"), ws.LabelFor("linux"))
	}
}

func TestJSON(t *testing.T) {
	var got struct {
		Shortcuts []Shortcut  `json:"shortcuts"`
		Groups    []GroupList `json:"groups"`
	}
	raw := JSON()
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Shortcuts) != len(All()) || len(got.Groups) != 4 {
		t.Errorf("JSON has %d shortcuts, %d groups", len(got.Shortcuts), len(got.Groups))
	}
	if strings.ContainsAny(string(raw), "<>") {
		t.Error("JSON must be safe to inline in a <script>")
	}
}

// Move to Trash is the library's (home scope), takes Finder's ⌘⌫ on macOS
// only, and Undo is deliberately not a command: ⌘Z/Ctrl+Z stays reserved for
// Edit › Undo (the library's toast handles it itself).
func TestTrashCommand(t *testing.T) {
	s, ok := ByID("library.trash")
	if !ok {
		t.Fatal("library.trash missing")
	}
	if s.Scope != ScopeHome || s.Handler != HandlerPage || s.Menu != MenuFile || s.Stub {
		t.Errorf("library.trash = %+v", s)
	}
	if s.Mac != "Cmd+Backspace" || s.Other != "" {
		t.Errorf("library.trash combos = %q / %q", s.Mac, s.Other)
	}
	if got := s.Display("darwin"); got != "⌘⌫" {
		t.Errorf("display = %q", got)
	}
	for _, sc := range All() {
		if sc.Mac == "Cmd+Z" || sc.Other == "Ctrl+Z" {
			t.Errorf("%s binds the reserved Undo combo", sc.ID)
		}
	}
}
