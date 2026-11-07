package serve

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
	"github.com/devenjarvis/lathe/internal/voice"
)

// The New tutorial sheet (#138): the form that collects /lathe's setup
// answers up front and submits them to POST /-/new (agent_new.go).
//
// It is one partial (newsheet.html "newSheetForm") rendered two ways:
//
//   - GET /-/new is a full page holding the form, so the title-bar button,
//     the blank cover on Home and every other way in work with scripts off
//     (the form posts urlencoded to POST /-/new, which answers with a
//     redirect or this page again).
//   - GET /-/new?partial=1 is the bare form, which newsheet.js drops into
//     the sheet (a modal <dialog> under the title bar) when ⌘N, a "New
//     tutorial" button or ⌘K's "New tutorial about “…”" opens it.
//
// GET /-/new/suggest answers the sheet's live updates as the reader types:
// the slug, the workspace under the code folder, the suggested shelf, and
// what was detected in an existing workspace folder.

// newLevels are the skill's experience-level answers ("beginner, some
// familiarity, or experienced in adjacent areas"), in the sheet's words.
var newLevels = []struct{ Value, Label, Hint string }{
	{"beginner", "New to this", "Explain the basics as they come up."},
	{"some familiarity", "Some familiarity", "Skip the basics, explain the rest."},
	{"experienced in adjacent areas", "Experienced nearby", "You know a neighbouring field well."},
}

// newTopicIdeas are the sheet's "Or try" chips. The first few whose slug
// isn't in the library already are shown.
var newTopicIdeas = []string{
	"Build a digital synth in Zig",
	"Write a tiny Lisp interpreter in Go",
	"A Raft key-value store in Rust",
	"A ray tracer in C, one pixel at a time",
	"A regex engine from scratch in Python",
	"A Game Boy emulator in TypeScript",
}

const newIdeaCount = 3

type newLevelOption struct {
	Value, Label, Hint string
	Checked            bool
}

type newShelfOption struct {
	ID, Name string
	Selected bool
}

type newVoiceCard struct {
	Name, Description string
	// Sample is the first sentence of the voice's sample paragraph: the
	// card's one-line "sounds like".
	Sample           string
	Default, Checked bool
}

// newAgentLine is the sheet's footer: who writes Part 1.
type newAgentLine struct {
	// Mode is "agent" (the hosted agent), "worker" (a /lathe-work session)
	// or "handoff" (nothing to run it: Start writing gives the command).
	Mode        string
	Name, Model string
}

// newResult is what a scripts-off submit answers when it did not start a
// hosted run: the job went to a worker, or here is the command to paste.
type newResult struct {
	Mode, Command, Notice string
}

// newSheetView is the data of the "newSheetForm" partial and the /-/new page.
type newSheetView struct {
	Topic       string
	Ideas       []string
	Levels      []newLevelOption
	Shelves     []newShelfOption
	Suggested   string // the suggested shelf's name, "" for none
	Voices      []newVoiceCard
	Workspace   string // the field's value
	WorkspaceAt string // <code root>/<slug-placeholder>, for the hint
	// AutoWorkspace marks a workspace the sheet filled in itself, which it
	// keeps following the topic until the reader edits it.
	AutoWorkspace bool
	Versions      string
	Repo          string
	RepoBranch    string
	Detected      workspaceInfo
	Agent         newAgentLine
	Native, Mac   bool
	Error         string
	Result        *newResult
	// Page is set for the full /-/new page (not the sheet's fragment).
	Page bool
}

// shelfSuggester picks the shelf a new tutorial on topic should go on: a
// shelf id, or "" for Unsorted. It is a variable so the agent's own pick
// (#135) can replace or wrap the deterministic match without touching the
// sheet.
type shelfSuggester func(topic string, shelves *store.Shelves, tutorials []*store.Tutorial) string

var suggestShelf shelfSuggester = suggestShelfByMatch

// shelfStopwords are words too common to say which shelf a topic is about.
var shelfStopwords = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true, "in": true, "on": true,
	"of": true, "for": true, "to": true, "with": true, "from": true, "into": true,
	"using": true, "via": true, "by": true, "how": true, "build": true, "building": true,
	"make": true, "making": true, "write": true, "writing": true, "create": true,
	"learn": true, "my": true, "your": true, "own": true, "simple": true, "tiny": true,
	"tutorial": true, "tutorials": true, "scratch": true, "one": true, "time": true,
	"at": true, "is": true, "it": true, "its": true, "s": true,
}

// shelfTokens splits text into lowercase words, without stopwords, with a
// plural "s" trimmed so "synths" meets "synth".
func shelfTokens(text string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		alnum := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		return !alnum && r != '+' && r != '#'
	}) {
		if shelfStopwords[w] {
			continue
		}
		if len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
			w = strings.TrimSuffix(w, "s")
		}
		out[w] = true
	}
	return out
}

func overlap(a, b map[string]bool) int {
	n := 0
	for w := range a {
		if b[w] {
			n++
		}
	}
	return n
}

// suggestShelfByMatch is the deterministic shelf pick: each real shelf scores
// 3 per topic word in its name, 2 per word in its description, and 1 per
// topic word shared with each tutorial already on it (title, topic, tags).
// Language and tool names (any tutorial's Tools) count only against a
// shelf's own name and description: shelves are about subjects, and "in
// Rust" says little about which one. The highest score wins, ties going to
// the shelf listed first; nothing matching means Unsorted ("").
func suggestShelfByMatch(topic string, shelves *store.Shelves, tutorials []*store.Tutorial) string {
	if shelves == nil || len(shelves.Shelves) == 0 {
		return ""
	}
	words := shelfTokens(topic)
	if len(words) == 0 {
		return ""
	}
	score := map[string]int{}
	for _, sh := range shelves.Shelves {
		score[sh.ID] = 3*overlap(words, shelfTokens(sh.Name)) + 2*overlap(words, shelfTokens(sh.Description))
	}
	subject := map[string]bool{}
	for w := range words {
		subject[w] = true
	}
	for _, t := range tutorials {
		for _, tool := range t.Tools {
			for w := range shelfTokens(tool.Name) {
				delete(subject, w)
			}
		}
	}
	for _, t := range tutorials {
		sh := shelves.ShelfOf(t.Slug)
		if sh.Unsorted() {
			continue
		}
		score[sh.ID] += overlap(subject, shelfTokens(t.Title+" "+t.Topic+" "+strings.Join(t.Tags, " ")))
	}
	best, bestScore := "", 0
	for _, sh := range shelves.Shelves {
		if score[sh.ID] > bestScore {
			best, bestScore = sh.ID, score[sh.ID]
		}
	}
	return best
}

// workspaceInfo is what the sheet found in an existing workspace folder: its
// git origin and branch and the toolchain versions its manifests pin. It
// reads a handful of files and runs nothing.
type workspaceInfo struct {
	Exists bool         `json:"exists"`
	Repo   string       `json:"repo,omitempty"`
	Branch string       `json:"branch,omitempty"`
	Tools  []store.Tool `json:"tools,omitempty"`
}

// Versions is Tools in the sheet's "name version, name version" form.
func (w workspaceInfo) Versions() string { return formatVersions(w.Tools) }

func formatVersions(tools []store.Tool) string {
	parts := make([]string, 0, len(tools))
	for _, t := range tools {
		parts = append(parts, strings.TrimSpace(t.Name+" "+t.Version))
	}
	return strings.Join(parts, ", ")
}

// parseVersions reads the sheet's Versions field: "zig 0.13.0, llvm 18" (or
// "zig:0.13.0"), comma- or newline-separated.
func parseVersions(s string) []store.Tool {
	var tools []store.Tool
	for _, item := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == ';' }) {
		f := strings.Fields(strings.ReplaceAll(item, ":", " "))
		switch len(f) {
		case 0:
			continue
		case 1:
			tools = append(tools, store.Tool{Name: f[0]})
		default:
			tools = append(tools, store.Tool{Name: f[0], Version: strings.Join(f[1:], "")})
		}
	}
	return store.NormalizeTools(tools)
}

var (
	goModRe     = regexp.MustCompile(`(?m)^go\s+(\S+)`)
	zigZonRe    = regexp.MustCompile(`\.minimum_zig_version\s*=\s*"([^"]+)"`)
	rustChanRe  = regexp.MustCompile(`(?m)^\s*channel\s*=\s*"([^"]+)"`)
	gitRemoteRe = regexp.MustCompile(`(?m)^\[remote "origin"\][^\[]*?^\s*url\s*=\s*(\S+)`)
)

// maxManifestBytes caps how much of a manifest detection reads.
const maxManifestBytes = 64 << 10

func readSmall(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	b, _ := io.ReadAll(io.LimitReader(f, maxManifestBytes))
	return string(b)
}

// detectWorkspace looks at dir (absolute) for the repo and versions. A
// missing folder reports Exists false and nothing else.
func detectWorkspace(dir string) workspaceInfo {
	var info workspaceInfo
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return info
	}
	info.Exists = true
	if cfg := readSmall(filepath.Join(dir, ".git", "config")); cfg != "" {
		if m := gitRemoteRe.FindStringSubmatch(cfg); m != nil {
			info.Repo = m[1]
		}
		head := strings.TrimSpace(readSmall(filepath.Join(dir, ".git", "HEAD")))
		info.Branch = strings.TrimPrefix(head, "ref: refs/heads/")
		if info.Branch == head { // detached
			info.Branch = ""
		}
	}
	var tools []store.Tool
	add := func(name, version string) {
		if version = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(version), "v")); version != "" {
			tools = append(tools, store.Tool{Name: name, Version: version})
		}
	}
	if m := goModRe.FindStringSubmatch(readSmall(filepath.Join(dir, "go.mod"))); m != nil {
		add("go", m[1])
	}
	if m := zigZonRe.FindStringSubmatch(readSmall(filepath.Join(dir, "build.zig.zon"))); m != nil {
		add("zig", m[1])
	}
	if m := rustChanRe.FindStringSubmatch(readSmall(filepath.Join(dir, "rust-toolchain.toml"))); m != nil {
		add("rust", m[1])
	} else if s := strings.TrimSpace(readSmall(filepath.Join(dir, "rust-toolchain"))); s != "" && !strings.Contains(s, "\n") {
		add("rust", s)
	}
	if s := strings.TrimSpace(readSmall(filepath.Join(dir, ".nvmrc"))); s != "" && !strings.Contains(s, "\n") {
		add("node", s)
	} else if pkg := readSmall(filepath.Join(dir, "package.json")); pkg != "" {
		var p struct {
			Engines struct {
				Node string `json:"node"`
			} `json:"engines"`
		}
		if json.Unmarshal([]byte(pkg), &p) == nil {
			add("node", strings.TrimLeft(p.Engines.Node, ">=^~ "))
		}
	}
	if s := strings.TrimSpace(readSmall(filepath.Join(dir, ".python-version"))); s != "" && !strings.Contains(s, "\n") {
		add("python", s)
	}
	// asdf / mise: "<tool> <version>" per line.
	sc := bufio.NewScanner(strings.NewReader(readSmall(filepath.Join(dir, ".tool-versions"))))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 2 && !strings.HasPrefix(f[0], "#") {
			name := f[0]
			switch name {
			case "golang":
				name = "go"
			case "nodejs":
				name = "node"
			}
			add(name, f[1])
		}
	}
	info.Tools = store.NormalizeTools(tools)
	return info
}

// newWorkspaceFor is the suggested workspace for a topic: <code root>/<slug>.
func newWorkspaceFor(topic string) string {
	slug := "tutorial"
	if strings.TrimSpace(topic) != "" {
		slug = store.SlugFromTopic(topic)
	}
	return config.CodeRootFor(config.LoadSettings().CodeRoot, slug)
}

// firstSentence is the opening sentence of a sample, for a voice card.
func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	for i, r := range s {
		if (r == '.' || r == '!' || r == '?') && (i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\n') {
			return s[:i+1]
		}
	}
	return s
}

// newSheetFor builds the sheet for topic, with the reader's earlier answers
// (a scripts-off submit that failed) in prev, if any.
func (s *Server) newSheetFor(topic string, prev *newRequest) newSheetView {
	v := newSheetView{
		Topic:  strings.TrimSpace(topic),
		Native: s.nativeShell() != nil,
		Mac:    runtime.GOOS == "darwin",
	}
	tutorials, _ := s.readTutorials()
	shelves, err := store.ReadShelves(s.shelvesPath())
	if err != nil {
		shelves = &store.Shelves{}
	}

	taken := map[string]bool{}
	for _, t := range tutorials {
		taken[t.Slug] = true
	}
	for _, idea := range newTopicIdeas {
		if len(v.Ideas) == newIdeaCount {
			break
		}
		if !taken[store.SlugFromTopic(idea)] {
			v.Ideas = append(v.Ideas, idea)
		}
	}

	level, voiceName, shelf := "", "", ""
	if prev != nil {
		level, voiceName, shelf = prev.Level, prev.Voice, prev.Shelf
		v.Workspace, v.Versions, v.Repo, v.RepoBranch = prev.Workspace, prev.Versions, prev.Repo, prev.RepoBranch
	} else {
		shelf = suggestShelf(v.Topic, shelves, tutorials)
		v.Workspace, v.AutoWorkspace = newWorkspaceFor(v.Topic), true
		if p, err := store.ExpandPath(v.Workspace); err == nil {
			v.Detected = detectWorkspace(p)
			v.Versions, v.Repo, v.RepoBranch = v.Detected.Versions(), v.Detected.Repo, v.Detected.Branch
		}
	}
	v.WorkspaceAt = config.CodeRootFor(config.LoadSettings().CodeRoot, "")

	for _, l := range newLevels {
		v.Levels = append(v.Levels, newLevelOption{Value: l.Value, Label: l.Label, Hint: l.Hint, Checked: l.Value == level})
	}

	v.Shelves = append(v.Shelves, newShelfOption{ID: store.UnsortedShelfID, Name: store.UnsortedShelfName, Selected: shelf == "" || shelf == store.UnsortedShelfID})
	for _, sh := range shelves.Shelves {
		v.Shelves = append(v.Shelves, newShelfOption{ID: sh.ID, Name: sh.Name, Selected: sh.ID == shelf})
		if sh.ID == shelf && prev == nil {
			v.Suggested = sh.Name
		}
	}

	def := store.NormalizeVoice(config.DefaultVoice())
	if voiceName = store.NormalizeVoice(voiceName); voiceName == "" {
		voiceName = def
	}
	if list, err := voice.List(); err == nil {
		for _, vc := range list {
			v.Voices = append(v.Voices, newVoiceCard{
				Name: vc.Name, Description: vc.Description,
				Sample:  firstSentence(strings.Join(paragraphs(vc.Sample()), " ")),
				Default: vc.Name == def, Checked: vc.Name == voiceName,
			})
		}
		// The default first, then the rest as voice.List orders them.
		sort.SliceStable(v.Voices, func(i, j int) bool { return v.Voices[i].Default && !v.Voices[j].Default })
	}

	switch h := s.hostedAgent(); {
	case h != nil:
		st := h.status()
		v.Agent = newAgentLine{Mode: "agent", Name: st.Name, Model: st.Model}
		if v.Agent.Name == "" {
			v.Agent.Name = firstField(st.Command)
		}
	case s.queue.WorkerConnected():
		v.Agent = newAgentLine{Mode: "worker"}
	default:
		v.Agent = newAgentLine{Mode: "handoff"}
	}
	return v
}

// handleNewSheet renders the sheet: the whole /-/new page, or with
// ?partial=1 only the form, for newsheet.js to put in its dialog. ?topic=
// prefills the topic (⌘K's "New tutorial about “…”").
func (s *Server) handleNewSheet(w http.ResponseWriter, r *http.Request) {
	v := s.newSheetFor(r.URL.Query().Get("topic"), nil)
	if r.URL.Query().Get("partial") != "" {
		s.renderNewPartial(w, v, http.StatusOK)
		return
	}
	s.renderNewPage(w, v, http.StatusOK)
}

func (s *Server) renderNewPartial(w http.ResponseWriter, v newSheetView, code int) {
	var buf bytes.Buffer
	if err := s.newTmpl.ExecuteTemplate(&buf, "newSheetForm", v); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) renderNewPage(w http.ResponseWriter, v newSheetView, code int) {
	v.Page = true
	var buf bytes.Buffer
	if err := s.newTmpl.Execute(&buf, map[string]any{
		"CSS":          s.designCSS,
		"HighlightCSS": s.highlightCSS,
		"Native":       v.Native,
		"Sheet":        v,
	}); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_, _ = w.Write(buf.Bytes())
}

// handleNewSuggest answers the sheet's live updates:
// GET /-/new/suggest?topic=…[&workspace=…] → {slug, workspace, shelf,
// shelfName, detected}. workspace, when given, is the folder to look in;
// else the suggested one is.
func (s *Server) handleNewSuggest(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	q := r.URL.Query()
	topic := oneLine(q.Get("topic"))
	if len(topic) > maxTopicLen {
		topic = topic[:maxTopicLen]
	}
	tutorials, _ := s.readTutorials()
	shelves, err := store.ReadShelves(s.shelvesPath())
	if err != nil {
		shelves = &store.Shelves{}
	}
	shelf := suggestShelf(topic, shelves, tutorials)
	shelfName := ""
	if sh, ok := shelves.Get(shelf); ok && shelf != "" {
		shelfName = sh.Name
	}
	slug := ""
	if topic != "" {
		slug = store.SlugFromTopic(topic)
	}
	suggested := newWorkspaceFor(topic)
	look := strings.TrimSpace(q.Get("workspace"))
	if look == "" {
		look = suggested
	}
	var detected workspaceInfo
	if len(look) <= maxFieldLen {
		if p, err := store.ExpandPath(look); err == nil && filepath.IsAbs(p) && !pathWithin(p, s.tutorialsDir) {
			detected = detectWorkspace(p)
		}
	}
	writeJSON(w, map[string]any{
		"slug": slug, "workspace": suggested,
		"shelf": shelf, "shelfName": shelfName,
		"detected": detected, "versions": detected.Versions(),
	})
}

// newFormRequest reads a scripts-off submit of the sheet (urlencoded).
func newFormRequest(w http.ResponseWriter, r *http.Request) (newRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxNewBytes)
	if err := r.ParseForm(); err != nil {
		return newRequest{}, false
	}
	f := r.PostForm
	// The page's workspace field still holding the suggestion it was
	// rendered with (workspaceAuto) follows the topic.
	auto := f.Get("workspaceAuto") != "" && strings.TrimSpace(f.Get("workspaceAuto")) == strings.TrimSpace(f.Get("workspace"))
	return newRequest{WorkspaceAuto: auto,
		Topic: f.Get("topic"), Level: f.Get("level"), Voice: f.Get("voice"),
		Workspace: f.Get("workspace"), Versions: f.Get("versions"),
		Repo: f.Get("repo"), RepoBranch: f.Get("repoBranch"), Shelf: f.Get("shelf"),
	}, true
}

// isFormPost reports whether r is a plain HTML form submit rather than the
// sheet's JSON.
func isFormPost(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	return strings.HasPrefix(ct, "application/x-www-form-urlencoded") || strings.HasPrefix(ct, "multipart/form-data")
}

// newFormReply answers a scripts-off submit: the hosted run's tutorial page
// (the being-written page) on a start, else the /-/new page again with the
// error, the worker note or the command to paste.
func (s *Server) newFormReply(w http.ResponseWriter, r *http.Request, req newRequest, mode, slug, command, notice, errMsg string, code int) {
	if mode == "agent" {
		http.Redirect(w, r, "/"+slug+"/", http.StatusSeeOther)
		return
	}
	v := s.newSheetFor(req.Topic, &req)
	v.Error = errMsg
	if mode != "" {
		v.Result = &newResult{Mode: mode, Command: command, Notice: notice}
	}
	s.renderNewPage(w, v, code)
}

// newSheetScript is the sheet's page script (newsheet.js), inlined by the
// "newSheetDialog" partial on every page.
//
//go:embed newsheet.js
var newSheetScript string

func newSheetJS() template.JS { return template.JS(newSheetScript) }
