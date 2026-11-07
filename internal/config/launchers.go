package config

import (
	"runtime"
	"slices"
)

// The editor and terminal the desktop app opens a tutorial's workspace in
// (Settings › Reading, issue #145): "Open src/envelope.zig in Zed", "Run in
// Terminal", the title bar's workspace menu. config.json stores an id from
// the lists below; "" is the system default. Only the desktop app launches
// anything (internal/desktop/launch.go turns an id into a command line);
// plain `lathe serve` keeps the settings but hides the items.

// Editor is one code editor Lathe knows how to open a file or folder in.
type Editor struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// MacApp is the application name `open -a` takes on macOS, where an app
	// launched from the Dock has no shell PATH to find the command in.
	MacApp string `json:"-"`
	// Command is the editor's command-line launcher on Windows and Linux
	// (looked up on PATH).
	Command string `json:"-"`
}

// Editors are the editor choices, in the order Settings lists them.
var Editors = []Editor{
	{ID: "zed", Label: "Zed", MacApp: "Zed", Command: "zed"},
	{ID: "vscode", Label: "Visual Studio Code", MacApp: "Visual Studio Code", Command: "code"},
	{ID: "cursor", Label: "Cursor", MacApp: "Cursor", Command: "cursor"},
	{ID: "windsurf", Label: "Windsurf", MacApp: "Windsurf", Command: "windsurf"},
	{ID: "sublime", Label: "Sublime Text", MacApp: "Sublime Text", Command: "subl"},
	{ID: "idea", Label: "IntelliJ IDEA", MacApp: "IntelliJ IDEA", Command: "idea"},
}

// EditorByID returns the editor with id, or false ("" and unknown ids are
// the system default).
func EditorByID(id string) (Editor, bool) {
	for _, e := range Editors {
		if e.ID == id {
			return e, true
		}
	}
	return Editor{}, false
}

// Terminal is one terminal app Lathe can open at the workspace.
type Terminal struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// GOOS is the one platform the terminal is offered on.
	GOOS string `json:"-"`
}

// Terminals are the terminal choices for every platform; TerminalsFor picks
// one platform's. "" (not listed) is the platform default: Terminal on
// macOS, the first one installed on Linux, Windows Terminal (else
// PowerShell) on Windows.
var Terminals = []Terminal{
	{ID: "terminal", Label: "Terminal", GOOS: "darwin"},
	{ID: "iterm", Label: "iTerm", GOOS: "darwin"},
	{ID: "ghostty", Label: "Ghostty", GOOS: "darwin"},
	{ID: "wezterm", Label: "WezTerm", GOOS: "darwin"},

	{ID: "gnome-terminal", Label: "GNOME Terminal", GOOS: "linux"},
	{ID: "konsole", Label: "Konsole", GOOS: "linux"},
	{ID: "xfce4-terminal", Label: "Xfce Terminal", GOOS: "linux"},
	{ID: "kitty", Label: "kitty", GOOS: "linux"},
	{ID: "alacritty", Label: "Alacritty", GOOS: "linux"},
	{ID: "wezterm", Label: "WezTerm", GOOS: "linux"},
	{ID: "ghostty", Label: "Ghostty", GOOS: "linux"},
	{ID: "foot", Label: "foot", GOOS: "linux"},
	{ID: "xterm", Label: "xterm", GOOS: "linux"},

	{ID: "wt", Label: "Windows Terminal", GOOS: "windows"},
	{ID: "powershell", Label: "PowerShell", GOOS: "windows"},
	{ID: "cmd", Label: "Command Prompt", GOOS: "windows"},
}

// TerminalsFor lists the terminal choices offered on goos.
func TerminalsFor(goos string) []Terminal {
	var out []Terminal
	for _, t := range Terminals {
		if t.GOOS == goos {
			out = append(out, t)
		}
	}
	return out
}

// TerminalByID returns goos's terminal with id, or false.
func TerminalByID(goos, id string) (Terminal, bool) {
	i := slices.IndexFunc(Terminals, func(t Terminal) bool { return t.GOOS == goos && t.ID == id })
	if i < 0 {
		return Terminal{}, false
	}
	return Terminals[i], true
}

// DefaultTerminalLabel names the platform default terminal for the
// Settings choice and the menu items ("Open in Terminal").
func DefaultTerminalLabel(goos string) string {
	switch goos {
	case "darwin":
		return "Terminal"
	case "windows":
		return "Windows Terminal"
	}
	return "Terminal"
}

// TerminalLabel names the terminal the reader chose, or the platform
// default.
func TerminalLabel(goos, id string) string {
	if t, ok := TerminalByID(goos, id); ok {
		return t.Label
	}
	return DefaultTerminalLabel(goos)
}

// resolveLaunchers keeps a stored editor or terminal id only when this
// platform knows it: a hand edit, or a config.json carried from another OS,
// falls back to the default.
func resolveLaunchers(c *Config, s *Settings) {
	if _, ok := EditorByID(c.Editor); ok {
		s.Editor = c.Editor
	}
	if _, ok := TerminalByID(runtime.GOOS, c.Terminal); ok {
		s.Terminal = c.Terminal
	}
}
