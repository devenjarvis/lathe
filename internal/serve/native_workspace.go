package serve

import (
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/devenjarvis/lathe/internal/config"
)

// The reader's bridges to their editor and terminal (issue #145): open a
// workspace file in the configured editor, open a terminal at the workspace
// with a command typed in (never run), and compare a listing with the
// reader's copy of the file.
//
//	POST /-/native/open-editor {"slug", "path"}    → 204 (path relative to the workspace; "" = the folder)
//	POST /-/native/terminal    {"slug", "command"} → {"typed", "clipboard", "history", "app"}
//	POST /-/compare/{slug}     {"path", "code"}    → a line diff (works in plain lathe serve too)
//
// The first two need a shell that is a WorkspaceLauncher and 404 without
// one, like the other /-/native/ endpoints. Every endpoint is sameOrigin-
// checked, and every path is resolved inside the tutorial's recorded
// workspace (WorkspaceTarget): the page names a file relative to it and can
// never name anything else.

// WorkspaceLauncher is an optional part of a Native shell: it opens files in
// the reader's editor and a terminal at the workspace. It is its own
// interface so a shell without it still satisfies Native.
type WorkspaceLauncher interface {
	// OpenInEditor opens path (an absolute file or folder inside a
	// workspace, already checked) in editor, an id from config.Editors; ""
	// opens it with the system's default app.
	OpenInEditor(editor, path string) error
	// OpenTerminal opens terminal (a config.Terminals id; "" = the platform
	// default) at dir with command typed at the prompt and NOT run. When
	// the terminal can't take typed text the command goes on the clipboard
	// instead, and the outcome says so. An empty command just opens the
	// terminal there.
	OpenTerminal(terminal, dir, command string) (TerminalOutcome, error)
}

// TerminalOutcome is how a command reached the terminal.
type TerminalOutcome struct {
	// Typed: the command is at the prompt, waiting for Return.
	Typed bool `json:"typed"`
	// Clipboard: the command was put on the clipboard instead.
	Clipboard bool `json:"clipboard"`
	// History: the command is also the shell's last history entry (↑).
	History bool `json:"history"`
	// App names the terminal that opened.
	App string `json:"app"`
}

// goos is the platform the launchers are for (a var so tests can pick one).
var goos = func() string { return runtime.GOOS }

// launcher returns the installed shell's WorkspaceLauncher, or nil.
func (s *Server) launcher() WorkspaceLauncher {
	if l, ok := s.nativeShell().(WorkspaceLauncher); ok {
		return l
	}
	return nil
}

// workspaceRoutes registers this file's endpoints.
func (s *Server) workspaceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /-/native/open-editor", s.handleNativeOpenEditor)
	mux.HandleFunc("POST /-/native/terminal", s.handleNativeTerminal)
	mux.HandleFunc("POST /-/compare/{slug}", s.handleCompare)
}

// Limits on what the page may send.
const (
	// maxRelPath caps a workspace-relative path.
	maxRelPath = 512
	// MaxCommandBytes and MaxCommandLines cap a command for Run in Terminal.
	MaxCommandBytes = 4 << 10
	MaxCommandLines = 40
	// maxTerminalBody fits a full-size command after JSON escaping.
	maxTerminalBody = 16 << 10
	// maxCompareBody, maxCompareFile: a listing, and the reader's file.
	maxCompareBody = 128 << 10
	maxCompareFile = 1 << 20
)

// ErrOutsideWorkspace is WorkspaceTarget's answer for a path that would
// leave the workspace.
var ErrOutsideWorkspace = errors.New("that path is outside the workspace")

// WorkspaceTarget resolves rel, a slash-separated path relative to the
// workspace folder root ("" is the folder itself), to an absolute path inside
// it. It refuses absolute paths, "~", ".." segments, backslashes, drive or
// stream colons, control characters and over-long paths, then resolves
// symlinks and refuses a target outside the (resolved) root. A target that
// does not exist yet returns its path with an error wrapping fs.ErrNotExist.
func WorkspaceTarget(root, rel string) (string, error) {
	if root == "" || !filepath.IsAbs(root) {
		return "", errors.New("no workspace folder")
	}
	if len(rel) > maxRelPath {
		return "", errBadRequest("That path is too long.")
	}
	for _, r := range rel {
		if r < 0x20 || r == 0x7f || r == '\\' || r == ':' {
			return "", errBadRequest("That isn't a path inside the workspace.")
		}
	}
	if !utf8.ValidString(rel) || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "~") || filepath.IsAbs(rel) {
		return "", errBadRequest("That isn't a path inside the workspace.")
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return "", ErrOutsideWorkspace
		}
	}
	clean := path.Clean("/" + rel)[1:]
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	if clean == "" {
		return realRoot, nil
	}
	full := filepath.Join(realRoot, filepath.FromSlash(clean))
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return full, fmt.Errorf("%s: %w", clean, fs.ErrNotExist)
		}
		return "", err
	}
	if !insideDir(real, realRoot) {
		return "", ErrOutsideWorkspace
	}
	return real, nil
}

// insideDir reports whether p is root or below it (both cleaned, absolute).
func insideDir(p, root string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// CleanCommand checks a command for Run in Terminal and returns it tidied:
// CRLF to LF, outer blank lines and trailing spaces dropped. It refuses
// invalid UTF-8, control characters other than tab and newline (an escape
// sequence could drive the terminal), and anything over MaxCommandBytes or
// MaxCommandLines. The command is never run by Lathe; this keeps what the
// reader sees at the prompt exactly what the page showed.
func CleanCommand(raw string) (string, error) {
	cmd := strings.ReplaceAll(raw, "\r\n", "\n")
	cmd = strings.TrimRight(strings.TrimLeft(cmd, "\n"), " \t\n")
	if len(cmd) > MaxCommandBytes {
		return "", errBadRequest(fmt.Sprintf("That command is too long to type in (over %d bytes). Copy it instead.", MaxCommandBytes))
	}
	if !utf8.ValidString(cmd) {
		return "", errBadRequest("That command isn't valid text.")
	}
	for _, r := range cmd {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return "", errBadRequest("That command has control characters in it. Copy it instead.")
		}
	}
	if strings.Count(cmd, "\n")+1 > MaxCommandLines {
		return "", errBadRequest(fmt.Sprintf("That command is over %d lines. Copy it instead.", MaxCommandLines))
	}
	return cmd, nil
}

// workspaceFor is the slug's workspace folder for the handlers below, or ""
// after writing the error.
func (s *Server) workspaceFor(w http.ResponseWriter, slug string) string {
	if _, ok := s.safeTutorialPath(slug); !ok || slug == "" {
		http.Error(w, "unknown tutorial", http.StatusNotFound)
		return ""
	}
	dir := s.workspaceDir(slug)
	if dir == "" {
		http.Error(w, "Set a workspace folder for this tutorial first.", http.StatusConflict)
		return ""
	}
	return dir
}

// writeTargetError words a WorkspaceTarget failure.
func writeTargetError(w http.ResponseWriter, err error) {
	var bad errBadRequest
	switch {
	case errors.As(err, &bad):
		http.Error(w, bad.Error(), http.StatusBadRequest)
	case errors.Is(err, ErrOutsideWorkspace):
		http.Error(w, "That path is outside the workspace.", http.StatusBadRequest)
	case errors.Is(err, fs.ErrNotExist):
		http.Error(w, "There's no such file in your workspace yet.", http.StatusNotFound)
	default:
		http.Error(w, "could not read the workspace: "+err.Error(), http.StatusInternalServerError)
	}
}

// handleNativeOpenEditor opens a workspace file (or the workspace folder) in
// the reader's editor.
func (s *Server) handleNativeOpenEditor(w http.ResponseWriter, r *http.Request) {
	l := s.launcher()
	if l == nil {
		http.NotFound(w, r)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var payload struct {
		Slug string `json:"slug"`
		Path string `json:"path"`
	}
	if !readJSONBody(w, r, maxNativeBody, &payload) {
		return
	}
	root := s.workspaceFor(w, payload.Slug)
	if root == "" {
		return
	}
	target, err := WorkspaceTarget(root, payload.Path)
	if err != nil {
		writeTargetError(w, err)
		return
	}
	editor := config.LoadSettings().Editor
	if editor == "" && target == root {
		http.Error(w, "Choose an editor in Settings › Reading to open the folder in it.", http.StatusConflict)
		return
	}
	if err := l.OpenInEditor(editor, target); err != nil {
		http.Error(w, "could not open the editor: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleNativeTerminal opens the reader's terminal at the workspace with a
// command typed in, not run (or just opens it, for an empty command).
func (s *Server) handleNativeTerminal(w http.ResponseWriter, r *http.Request) {
	l := s.launcher()
	if l == nil {
		http.NotFound(w, r)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var payload struct {
		Slug    string `json:"slug"`
		Command string `json:"command"`
	}
	if !readJSONBody(w, r, maxTerminalBody, &payload) {
		return
	}
	cmd, err := CleanCommand(payload.Command)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	root := s.workspaceFor(w, payload.Slug)
	if root == "" {
		return
	}
	dir, err := WorkspaceTarget(root, "")
	if err != nil {
		writeTargetError(w, err)
		return
	}
	out, err := l.OpenTerminal(config.LoadSettings().Terminal, dir, cmd)
	if err != nil {
		http.Error(w, "could not open the terminal: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, out)
}

// diffLine is one line of a comparison: "same", "tutorial" (only in the
// listing), "yours" (only in the reader's file), or "gap" (N matching lines
// folded away).
type diffLine struct {
	Op   string `json:"op"`
	Text string `json:"text,omitempty"`
	N    int    `json:"n,omitempty"`
}

// compareResult is POST /-/compare's answer.
type compareResult struct {
	Path    string     `json:"path"`
	Missing bool       `json:"missing,omitempty"`
	Same    bool       `json:"same,omitempty"`
	Lines   []diffLine `json:"lines,omitempty"`
	// Above and Below count the lines of the reader's file outside the
	// part the listing matched (a listing is often an excerpt).
	Above int `json:"above,omitempty"`
	Below int `json:"below,omitempty"`
}

// handleCompare diffs a listing against the reader's copy of the file it
// names. It only reads, so it works without a desktop shell.
func (s *Server) handleCompare(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var payload struct {
		Path string `json:"path"`
		Code string `json:"code"`
	}
	if !readJSONBody(w, r, maxCompareBody, &payload) {
		return
	}
	if payload.Path == "" || strings.TrimSpace(payload.Code) == "" {
		http.Error(w, "send the listing's path and code", http.StatusBadRequest)
		return
	}
	root := s.workspaceFor(w, r.PathValue("slug"))
	if root == "" {
		return
	}
	target, err := WorkspaceTarget(root, payload.Path)
	if errors.Is(err, fs.ErrNotExist) {
		writeJSON(w, compareResult{Path: payload.Path, Missing: true})
		return
	}
	if err != nil {
		writeTargetError(w, err)
		return
	}
	fi, err := os.Stat(target)
	if err != nil {
		writeTargetError(w, err)
		return
	}
	if fi.IsDir() {
		http.Error(w, payload.Path+" is a folder in your workspace.", http.StatusBadRequest)
		return
	}
	if fi.Size() > maxCompareFile {
		http.Error(w, "Your "+payload.Path+" is too big to compare here.", http.StatusBadRequest)
		return
	}
	data, err := os.ReadFile(target)
	if err != nil {
		writeTargetError(w, err)
		return
	}
	if !utf8.Valid(data) {
		http.Error(w, "Your "+payload.Path+" isn't a text file.", http.StatusBadRequest)
		return
	}
	res, err := compareListing(payload.Code, string(data))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	res.Path = payload.Path
	writeJSON(w, res)
}

// Comparison limits: a table of listing × file lines is kept for the
// traceback.
const (
	maxListingLines = 2000
	maxFileLines    = 20000
	maxDiffCells    = 4_000_000
	// foldAfter: a run of matching lines longer than this keeps
	// diffContext lines at each end and folds the rest.
	foldAfter   = 7
	diffContext = 3
)

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return lines
}

// compareListing aligns the listing with the stretch of the file it best
// matches. Listings are often an excerpt (one function of the file), so the
// file's lines before and after the match are free to skip (a semi-global
// alignment): they are counted in Above and Below, not shown as
// differences. Inside the match every line only one side has costs one and
// every matching line earns one back, so the alignment takes in the lines
// that match rather than stopping short of them. Trailing spaces are
// ignored.
func compareListing(listing, file string) (compareResult, error) {
	a, b := splitLines(listing), splitLines(file)
	n, m := len(a), len(b)
	if n == 0 {
		return compareResult{}, errBadRequest("The listing is empty.")
	}
	if n > maxListingLines || m > maxFileLines || n*(m+1) > maxDiffCells {
		return compareResult{}, errBadRequest("That's too long to compare here.")
	}
	// d[i][j]: the lowest cost (one-sided lines minus matching ones) of
	// aligning a[:i] with a stretch of b that ends at j. |cost| ≤ n+m fits
	// an int16 under the limits above.
	w := m + 1
	d := make([]int16, (n+1)*w)
	at := func(i, j int) int16 { return d[i*w+j] }
	for i := 1; i <= n; i++ {
		d[i*w] = int16(i)
		for j := 1; j <= m; j++ {
			best := at(i-1, j) + 1
			if v := at(i, j-1) + 1; v < best {
				best = v
			}
			if a[i-1] == b[j-1] {
				if v := at(i-1, j-1) - 1; v < best {
					best = v
				}
			}
			d[i*w+j] = best
		}
	}
	// The cheapest end; on a tie the later one, which takes in more of the
	// file's matching lines rather than stopping short of them.
	end := 0
	for j := 1; j <= m; j++ {
		if at(n, j) <= at(n, end) {
			end = j
		}
	}
	var rev []diffLine
	i, j := n, end
	for i > 0 {
		switch {
		case j > 0 && a[i-1] == b[j-1] && at(i, j) == at(i-1, j-1)-1:
			rev = append(rev, diffLine{Op: "same", Text: a[i-1]})
			i, j = i-1, j-1
		case j > 0 && at(i, j) == at(i, j-1)+1:
			rev = append(rev, diffLine{Op: "yours", Text: b[j-1]})
			j--
		default:
			rev = append(rev, diffLine{Op: "tutorial", Text: a[i-1]})
			i--
		}
	}
	res := compareResult{Above: j, Below: m - end}
	if m == 0 {
		res.Above, res.Below = 0, 0
	}
	lines := make([]diffLine, 0, len(rev))
	for k := len(rev) - 1; k >= 0; k-- {
		lines = append(lines, rev[k])
	}
	res.Same = true
	for _, l := range lines {
		if l.Op != "same" {
			res.Same = false
			break
		}
	}
	res.Lines = foldSame(lines)
	return res, nil
}

// foldSame folds long runs of matching lines down to their context.
func foldSame(lines []diffLine) []diffLine {
	var out []diffLine
	for k := 0; k < len(lines); {
		if lines[k].Op != "same" {
			out = append(out, lines[k])
			k++
			continue
		}
		e := k
		for e < len(lines) && lines[e].Op == "same" {
			e++
		}
		run := lines[k:e]
		head, tail := diffContext, diffContext
		if k == 0 {
			head = 0
		}
		if e == len(lines) {
			tail = 0
		}
		if len(run) > foldAfter && len(run) > head+tail {
			out = append(out, run[:head]...)
			out = append(out, diffLine{Op: "gap", N: len(run) - head - tail})
			out = append(out, run[len(run)-tail:]...)
		} else {
			out = append(out, run...)
		}
		k = e
	}
	return out
}

// applyLaunchers saves Settings › Reading's editor and terminal choices.
func (p *settingsPatch) applyLaunchers(c *config.Config) error {
	if p.Editor != nil {
		v := strings.TrimSpace(*p.Editor)
		if _, ok := config.EditorByID(v); v != "" && !ok {
			return errBadRequest("editor must be one of the listed editors")
		}
		c.Editor = v
	}
	if p.Terminal != nil {
		v := strings.TrimSpace(*p.Terminal)
		if _, ok := config.TerminalByID(goos(), v); v != "" && !ok {
			return errBadRequest("terminal must be one of the listed terminals")
		}
		c.Terminal = v
	}
	return nil
}

// launcherChoice is one option of Settings › Reading's editor or terminal
// select.
type launcherChoice struct{ Value, Label string }

type launcherChoicesData struct {
	Editors, Terminals []launcherChoice
}

func launcherChoices(goos string) launcherChoicesData {
	d := launcherChoicesData{
		Editors:   []launcherChoice{{"", "Default app for the file"}},
		Terminals: []launcherChoice{{"", config.DefaultTerminalLabel(goos) + " (default)"}},
	}
	if goos == "linux" {
		d.Terminals[0].Label = "The first one installed (default)"
	}
	for _, e := range config.Editors {
		d.Editors = append(d.Editors, launcherChoice{e.ID, e.Label})
	}
	for _, t := range config.TerminalsFor(goos) {
		d.Terminals = append(d.Terminals, launcherChoice{t.ID, t.Label})
	}
	return d
}

// readingTools is what the reading page's selection toolbar, listing menu,
// command buttons and title-bar workspace menu need (reading_tools.js),
// inlined as JSON.
type readingTools struct {
	// Launch: the desktop shell can open the editor and terminal. Without
	// it the native items are not shown.
	Launch bool `json:"launch"`
	Reveal bool `json:"reveal"`
	// Editor is the chosen editor's name ("" = the file's default app).
	Editor   string `json:"editor"`
	Terminal string `json:"terminal"`
	// RevealLabel is "Show in Finder" on macOS.
	RevealLabel string `json:"revealLabel"`
	// Workspace is the workspace folder when it is set and exists.
	Workspace     string `json:"workspace"`
	WorkspaceName string `json:"workspaceName"`
}

func (s *Server) readingToolsFor(slug string) readingTools {
	st := config.LoadSettings()
	rt := readingTools{
		Launch:      s.launcher() != nil,
		Reveal:      s.revealer() != nil,
		Terminal:    config.TerminalLabel(goos(), st.Terminal),
		RevealLabel: "Show in folder",
	}
	if goos() == "darwin" {
		rt.RevealLabel = "Show in Finder"
	}
	if e, ok := config.EditorByID(st.Editor); ok {
		rt.Editor = e.Label
	}
	if dir := s.workspaceDir(slug); dir != "" {
		rt.Workspace = dir
		rt.WorkspaceName = filepath.Base(dir)
	}
	return rt
}

//go:embed reading_tools.js
var readingToolsScript string

// readingToolsJS is the template helper that inlines reading_tools.js.
func readingToolsJS() template.JS { return template.JS(readingToolsScript) }
