package serve

import (
	"runtime"

	"bytes"
	"encoding/json"
	"github.com/devenjarvis/lathe/internal/shortcuts"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/devenjarvis/lathe/internal/agents"
	"github.com/devenjarvis/lathe/internal/buildinfo"
	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/loginenv"
	"github.com/devenjarvis/lathe/internal/store"
	"github.com/devenjarvis/lathe/internal/update"
)

// The Settings window (issue #131): one page at /-/settings with Agent,
// Voices, Library, Reading and Shortcuts tabs. The desktop app opens it in
// its own window (Lathe › Settings…, ⌘,); plain `lathe serve` serves the same
// page to a browser. Other screens deep-link to a tab with /-/settings#agent
// and so on.
//
//	GET  /-/settings                 the page (tabs render as sections; JS shows one at a time)
//	GET  /-/settings  (Accept: json) the resolved settings
//	POST /-/settings  {key: value}   save some keys → the resolved settings
//	POST /-/settings/agent-path      {"add": dir} | {"remove": dir} → {"agentPath": [...]}
//	POST /-/agent/restart            restart the hosted agent → {"agent": status}
//	GET  /-/settings?voice=<name>#voices  the Voices tab opened on one voice
//	POST /-/voices/…                 Voices tab mutations (voices.go)
//
// The agent pick reuses POST /-/agent (agent_panel.go). Every POST is
// sameOrigin-checked.

// settingsTabs are the tab ids, in order. They double as the deep-link hashes.
var settingsTabs = []struct{ ID, Label string }{
	{"agent", "Agent"},
	{"voices", "Voices"},
	{"library", "Library"},
	{"reading", "Reading"},
	{"shortcuts", "Shortcuts"},
}

// Shortcut is one row of the Settings window's read-only Shortcuts tab.
// Label and Combo are for the server's platform (the desktop app's, and the
// scripts-off fallback); the Mac*/Other* pairs let the page relabel itself
// for the browser's platform, as the agent panel's key hints do, since a
// browser on a Mac can be talking to lathe serve on Linux.
type Shortcut struct {
	Label string // what it does ("Next part")
	Combo string // how to press it, as the menu shows it ("⌘]"); "" = none here

	MacLabel, MacCombo     string
	OtherLabel, OtherCombo string
}

// shortcutGroup is one group of the Shortcuts tab (Library, Agent, ...).
type shortcutGroup struct {
	Name  string
	Items []Shortcut
}

// shortcutGroups is the Shortcuts tab, from the one registry the menus and
// key handlers use (internal/shortcuts), so they can never disagree. It lists
// built commands that have a default combo on either platform family.
func shortcutGroups(goos string) []shortcutGroup {
	var out []shortcutGroup
	for _, g := range shortcuts.Groups() {
		sg := shortcutGroup{Name: string(g.Group)}
		for _, sc := range g.Shortcuts {
			if sc.Stub || (sc.Mac == "" && sc.Other == "") {
				continue
			}
			it := Shortcut{
				Label:    sc.LabelFor(goos),
				MacLabel: sc.LabelFor("darwin"), OtherLabel: sc.LabelFor("linux"),
			}
			if sc.Mac != "" {
				it.MacCombo = sc.Display("darwin")
			}
			if sc.Other != "" {
				it.OtherCombo = sc.Display("linux")
			}
			if sc.Combo(goos) != "" {
				it.Combo = sc.Display(goos)
			}
			sg.Items = append(sg.Items, it)
		}
		if len(sg.Items) > 0 {
			out = append(out, sg)
		}
	}
	return out
}

// textScale and lineMeasure map the text_size and line_width choices to the
// CSS the pages apply (the root font size, and the --measure token).
var (
	textScale = map[string]string{
		"small": "90%", "default": "100%", "large": "112.5%", "larger": "125%", "largest": "140%",
	}
	textSizeLabels = map[string]string{
		"small": "Small", "default": "Default", "large": "Large", "larger": "Larger", "largest": "Largest",
	}
	lineMeasure = map[string]string{
		"narrow": "58ch", "default": "68ch", "wide": "80ch",
	}
	lineWidthLabels = map[string]string{
		"narrow": "Narrow", "default": "Default", "wide": "Wide",
	}
)

// pagePrefs is what every page's head needs from the settings: the served
// appearance (for the pre-paint theme script) and the reading size and
// measure (as CSS, so they apply even with scripts off).
type pagePrefs struct {
	Appearance string
	FontSize   template.CSS
	Measure    template.CSS
	// JS is the client's copy: the current choices plus the CSS for each,
	// so another window's change can be applied live.
	JS map[string]any
}

// pagePrefsNow reads the settings for one page render.
func pagePrefsNow() pagePrefs {
	st := config.LoadSettings()
	return pagePrefs{
		Appearance: st.Appearance,
		FontSize:   template.CSS(textScale[st.TextSize]),
		Measure:    template.CSS(lineMeasure[st.LineWidth]),
		JS: map[string]any{
			"appearance": st.Appearance,
			"text_size":  st.TextSize,
			"line_width": st.LineWidth,
			"sizes":      config.TextSizes,
			"scale":      textScale,
			"measure":    lineMeasure,
		},
	}
}

type choice struct {
	Value, Label string
}

func choices(values []string, labels map[string]string) []choice {
	out := make([]choice, 0, len(values))
	for _, v := range values {
		out = append(out, choice{Value: v, Label: labels[v]})
	}
	return out
}

// settingsAgent is one registry agent's card on the Agent tab.
type settingsAgent struct {
	agents.Detected
	Current bool
	// Program is the ACP command's program ("claude-agent-acp"), named when
	// it is missing.
	Program string
	// InstallCmd is set when Install is a command the reader can copy (it
	// starts with npm, brew, pip, …), else Install is prose with a URL.
	InstallCmd bool
	// State and Status are the current agent's live status ("ready",
	// "Running · model"), refreshed by the page from GET /-/worker.
	State, Status string
}

type settingsPathDir struct {
	Raw     string // as stored ("~/.nvm/…/bin")
	Missing bool   // not a directory on this machine
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		writeJSON(w, config.LoadSettings())
		return
	}
	home, _ := os.UserHomeDir()
	h := s.hostedAgent()
	var cur *agentStatus
	custom := ""
	if h != nil {
		st := h.status()
		cur = &st
		if _, ok := agents.ByCommand(h.command); !ok {
			custom = h.command
		}
	}
	var cards []settingsAgent
	for _, d := range agents.Detect(s.lookPath, home) {
		c := settingsAgent{Detected: d, Program: firstField(d.Command)}
		c.Current = h != nil && h.command == d.Command
		c.InstallCmd = looksLikeCommand(d.Install)
		if c.Current && cur != nil {
			c.State, c.Status = cur.State, describeAgent(*cur)
		}
		cards = append(cards, c)
	}

	var dirs []settingsPathDir
	if c, err := config.ReadConfig(); err == nil {
		for _, raw := range c.AgentPath {
			d := settingsPathDir{Raw: raw}
			if p, err := config.ExpandHome(raw); err != nil {
				d.Missing = true
			} else if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
				d.Missing = true
			}
			dirs = append(dirs, d)
		}
	}

	// ?voice= opens the Voices tab on a voice, or on the New voice or
	// Compare samples pane.
	voicePane := r.URL.Query().Get("voice")
	if voicePane != "new" && voicePane != "compare" {
		voicePane = ""
	}
	builtinVoices, yourVoices := s.settingsVoices(r.URL.Query().Get("voice"), voicePane == "")

	st := config.LoadSettings()
	data := map[string]any{
		"CSS":             s.designCSS,
		"HighlightCSS":    s.highlightCSS,
		"Native":          s.nativeShell() != nil,
		"CanReveal":       s.revealer() != nil,
		"Tabs":            settingsTabs,
		"Agents":          cards,
		"Current":         cur,
		"CustomCommand":   custom,
		"WorkerConnected": s.queue.WorkerConnected(),
		"AgentPath":       dirs,
		"BuiltinVoices":   builtinVoices,
		"YourVoices":      yourVoices,
		"AllVoices":       append(append([]settingsVoice{}, builtinVoices...), yourVoices...),
		"VoicesDir":       voicesDirDisplay(),
		"VoicePane":       voicePane,
		// AgentName is the hosted agent New voice drafts with ("" when none
		// is set up: the pane offers /lathe-voice instead).
		"AgentName":       agentName(h),
		"DefaultVoice":    config.DefaultVoice(),
		"TutorialsDir":    s.tutorialsDir,
		"TutorialsShort":  displayPath(s.tutorialsDir),
		"Settings":        st,
		"CodeRootExample": config.CodeRootFor(st.CodeRoot, "digital-synth-zig"),
		"Appearances": []choice{
			{config.AppearanceSystem, "Match system"},
			{config.AppearanceLight, "Light"},
			{config.AppearanceDark, "Dark"},
		},
		"TextSizes": choices(config.TextSizes, textSizeLabels),
		// TrashChoices is the Trash retention (trash_days), in days; 0 is
		// Never.
		"TrashChoices": []choice{{"7", "7 days"}, {"30", "30 days"}, {"0", "Never"}},
		"TrashCount":   s.trashCount(),
		"LineWidths":   choices(config.LineWidths, lineWidthLabels),
		// Version is the running release for Settings › Library › Updates,
		// "" for a development build.
		"Version": releaseVersion(),
		// Shortcuts is the read-only list from the shortcut registry.
		"Shortcuts": shortcutGroups(runtime.GOOS),
	}
	var buf bytes.Buffer
	if err := s.settingsTmpl.Execute(&buf, data); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

func agentName(h *agentHost) string {
	if h == nil {
		return ""
	}
	return h.name
}

// settingsPatch is a POST /-/settings body: any subset of the keys. Unknown
// keys are refused, so a typo can't silently do nothing.
type settingsPatch struct {
	Appearance       *string `json:"appearance"`
	TextSize         *string `json:"text_size"`
	LineWidth        *string `json:"line_width"`
	RememberPlace    *bool   `json:"remember_place"`
	PanelSuggestions *bool   `json:"panel_suggestions"`
	PanelWindow      *bool   `json:"panel_window"`
	CodeRoot         *string `json:"code_root"`
	KeepVerifiedCode *bool   `json:"keep_verified_code"`
	AutoVerify       *bool   `json:"auto_verify"`
	NotifyVerify     *bool   `json:"notify_verify"`
	NotifyExtend     *bool   `json:"notify_extend"`
	NotifyCheck      *bool   `json:"notify_check"`
	TrashDays        *int    `json:"trash_days"`
	UpdateCheck      *bool   `json:"update_check"`
}

func (p *settingsPatch) apply(c *config.Config) error {
	if p.Appearance != nil {
		v := strings.TrimSpace(*p.Appearance)
		if err := config.ValidateChoice("appearance", v, config.Appearances); err != nil {
			return err
		}
		c.Appearance = v
	}
	if p.TextSize != nil {
		v := strings.TrimSpace(*p.TextSize)
		if err := config.ValidateChoice("text_size", v, config.TextSizes); err != nil {
			return err
		}
		c.TextSize = blankDefault(v)
	}
	if p.LineWidth != nil {
		v := strings.TrimSpace(*p.LineWidth)
		if err := config.ValidateChoice("line_width", v, config.LineWidths); err != nil {
			return err
		}
		c.LineWidth = blankDefault(v)
	}
	if p.CodeRoot != nil {
		v := strings.TrimSpace(*p.CodeRoot)
		if v != "" {
			full, err := config.ExpandHome(v)
			if err != nil || !filepath.IsAbs(full) {
				return errBadRequest("The code folder must be a full path, or start with ~/.")
			}
		}
		if v == config.DefaultCodeRoot {
			v = ""
		}
		c.CodeRoot = v
	}
	setOn := func(dst **bool, v *bool) {
		if v == nil {
			return
		}
		if *v {
			*dst = nil // on is the default: keep config.json small
		} else {
			*dst = config.Bool(false)
		}
	}
	setOn(&c.RememberPlace, p.RememberPlace)
	setOn(&c.PanelSuggestions, p.PanelSuggestions)
	setOn(&c.KeepVerifiedCode, p.KeepVerifiedCode)
	setOn(&c.UpdateCheck, p.UpdateCheck)
	if p.PanelWindow != nil {
		c.PanelWindow = *p.PanelWindow
	}
	if p.AutoVerify != nil {
		c.AutoVerify = *p.AutoVerify
	}
	if p.TrashDays != nil {
		if !slices.Contains(config.TrashDayChoices, *p.TrashDays) {
			return errBadRequest("trash_days must be 7, 30 or 0 (never)")
		}
		if *p.TrashDays == config.DefaultTrashDays {
			c.TrashDays = nil
		} else {
			v := *p.TrashDays
			c.TrashDays = &v
		}
	}
	if p.NotifyVerify != nil || p.NotifyExtend != nil || p.NotifyCheck != nil {
		if c.Notify == nil {
			c.Notify = &config.NotifyConfig{}
		}
		setOn(&c.Notify.Verify, p.NotifyVerify)
		setOn(&c.Notify.Extend, p.NotifyExtend)
		setOn(&c.Notify.Check, p.NotifyCheck)
		if c.Notify.Verify == nil && c.Notify.Extend == nil && c.Notify.Check == nil {
			c.Notify = nil
		}
	}
	return nil
}

// blankDefault stores "default" as an absent key.
func blankDefault(v string) string {
	if v == "default" {
		return ""
	}
	return v
}

type errBadRequest string

func (e errBadRequest) Error() string { return string(e) }

func (s *Server) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var p settingsPatch
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		http.Error(w, "invalid settings: "+err.Error(), http.StatusBadRequest)
		return
	}
	var bad error
	err := config.UpdateConfig(func(c *config.Config) error {
		bad = p.apply(c)
		return bad
	})
	switch {
	case bad != nil:
		http.Error(w, bad.Error(), http.StatusBadRequest)
		return
	case err != nil:
		http.Error(w, "could not save settings: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, config.LoadSettings())
}

// handleAgentPath adds a folder to, or removes one from, agent_path. An added
// folder is put on this process's PATH at once, so the Agent tab can find an
// agent in it without a restart; a removed one leaves PATH until Lathe
// restarts.
func (s *Server) handleAgentPath(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var payload struct {
		Add    string `json:"add"`
		Remove string `json:"remove"`
	}
	if !readJSONBody(w, r, 4<<10, &payload) {
		return
	}
	add, remove := strings.TrimSpace(payload.Add), strings.TrimSpace(payload.Remove)
	if (add == "") == (remove == "") {
		http.Error(w, "send one of add or remove", http.StatusBadRequest)
		return
	}
	if add != "" {
		add = displayPath(add) // store a path under home in its ~ form
		full, err := config.ExpandHome(add)
		if err != nil || !filepath.IsAbs(full) {
			http.Error(w, "Enter a full path, or one that starts with ~/.", http.StatusBadRequest)
			return
		}
		if fi, err := os.Stat(full); err != nil || !fi.IsDir() {
			http.Error(w, "There's no folder at "+add+".", http.StatusBadRequest)
			return
		}
	}
	var list []string
	err := config.UpdateConfig(func(c *config.Config) error {
		var keep []string
		for _, d := range c.AgentPath {
			if d == remove || (add != "" && d == add) {
				continue
			}
			keep = append(keep, d)
		}
		if add != "" {
			keep = append(keep, add)
		}
		c.AgentPath = keep
		list = keep
		return nil
	})
	if err != nil {
		http.Error(w, "could not save: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if add != "" {
		_, _ = loginenv.ApplyAgentPath()
	}
	if list == nil {
		list = []string{}
	}
	writeJSON(w, map[string]any{"agentPath": list})
}

// notifyEnabled reports whether the reader wants a notification for job.
func notifyEnabled(job string) bool {
	st := config.LoadSettings()
	switch job {
	case jobVerify:
		return st.NotifyVerify
	case jobExtend:
		return st.NotifyExtend
	case jobCheck:
		return st.NotifyCheck
	}
	return true
}

// describeAgent words the current agent's status line ("Running · model ·
// isolation enforced"). settings.html's describe() words the same thing when
// it polls; keep the two in step.
func describeAgent(a agentStatus) string {
	var parts []string
	switch a.State {
	case "starting":
		parts = append(parts, "Starting…")
	case "ready":
		if a.Working != "" {
			parts = append(parts, "Running · "+a.Working)
		} else {
			parts = append(parts, "Running")
		}
	default:
		if a.Error != "" {
			parts = append(parts, "Stopped: "+a.Error)
		} else {
			parts = append(parts, "Stopped")
		}
	}
	if a.Model != "" {
		parts = append(parts, a.Model)
	}
	if iso := a.Isolation; iso != nil && iso.State != "" && iso.State != "unknown" {
		if iso.State == "enforced" {
			parts = append(parts, "isolation enforced")
		} else {
			parts = append(parts, "isolation soft")
		}
	}
	return strings.Join(parts, " · ")
}

func firstField(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return ""
}

// looksLikeCommand reports whether an install hint is a shell command (worth
// a Copy button) rather than prose with a link.
func looksLikeCommand(s string) bool {
	switch firstField(s) {
	case "npm", "npx", "brew", "pip", "pipx", "cargo", "go", "curl", "uv":
		return true
	}
	return false
}

// releaseVersion is the running version when it is a release ("0.9.0"),
// else "" (a development build).
func releaseVersion() string {
	if v, ok := update.Parse(buildinfo.Resolve()); ok {
		return v.String()
	}
	return ""
}

// trashCount is how many tutorials are in the Trash, for Settings › Library's
// Show Trash.
func (s *Server) trashCount() int {
	list, err := store.ListTrash(s.tutorialsDir)
	if err != nil {
		return 0
	}
	return len(list)
}
