package serve

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
)

// fakeLauncher is a shell that can also open the editor and terminal.
type fakeLauncher struct {
	*fakeNative
	lmu       sync.Mutex
	editors   [][2]string // editor, path
	terminals [][3]string // terminal, dir, command
	outcome   TerminalOutcome
	err       error
}

func (f *fakeLauncher) OpenInEditor(editor, path string) error {
	f.lmu.Lock()
	defer f.lmu.Unlock()
	f.editors = append(f.editors, [2]string{editor, path})
	return f.err
}

func (f *fakeLauncher) OpenTerminal(terminal, dir, command string) (TerminalOutcome, error) {
	f.lmu.Lock()
	defer f.lmu.Unlock()
	f.terminals = append(f.terminals, [3]string{terminal, dir, command})
	return f.outcome, f.err
}

// workspaceServer is a tutorial with a workspace holding src/main.zig, and
// a launcher shell.
func workspaceServer(t *testing.T) (*Server, *fakeLauncher, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	tutDir := makeTutFixture(t, dir, "series", true)
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "src", "main.zig"), []byte("const std = @import(\"std\");\n\npub fn main() void {\n    std.debug.print(\"hi\\n\", .{});\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetWorkspace(tutDir, ws); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(dir)
	t.Cleanup(srv.Close)
	f := &fakeLauncher{fakeNative: newFakeNative(), outcome: TerminalOutcome{Typed: true, App: "Terminal"}}
	srv.SetNative(f)
	real, _ := filepath.EvalSymlinks(ws)
	return srv, f, real
}

func TestWorkspaceTarget(t *testing.T) {
	root := t.TempDir()
	real, _ := filepath.EvalSymlinks(root)
	if err := os.MkdirAll(filepath.Join(root, "src", "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "a", "x.go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "secret-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "src"), filepath.Join(root, "inner")); err != nil {
		t.Fatal(err)
	}

	for rel, want := range map[string]string{
		"":              real,
		".":             real,
		"src/a/x.go":    filepath.Join(real, "src", "a", "x.go"),
		"./src//a/x.go": filepath.Join(real, "src", "a", "x.go"),
		"src/":          filepath.Join(real, "src"),
		"inner/a/x.go":  filepath.Join(real, "src", "a", "x.go"), // a link that stays inside
	} {
		got, err := WorkspaceTarget(root, rel)
		if err != nil || got != want {
			t.Errorf("WorkspaceTarget(%q) = %q, %v; want %q", rel, got, err, want)
		}
	}

	for _, rel := range []string{
		"..", "../x", "src/../../x", "src/a/../../..", "/etc/passwd", "~/x", "~", `src\a\x.go`, `..\x`,
		"C:/Windows", "src/x.go:stream", "src/\x00x", "src/\nx", strings.Repeat("a/", 300),
		"escape/secret", "secret-link",
	} {
		if got, err := WorkspaceTarget(root, rel); err == nil || errors.Is(err, fs.ErrNotExist) {
			t.Errorf("WorkspaceTarget(%q) = %q, %v; want refused", rel, got, err)
		}
	}

	if got, err := WorkspaceTarget(root, "src/new.go"); !errors.Is(err, fs.ErrNotExist) || got != filepath.Join(real, "src", "new.go") {
		t.Errorf("missing file = %q, %v; want its path and ErrNotExist", got, err)
	}
	if _, err := WorkspaceTarget("", "x"); err == nil {
		t.Error("no root accepted")
	}
	if _, err := WorkspaceTarget("relative/root", "x"); err == nil {
		t.Error("relative root accepted")
	}
}

func TestCleanCommand(t *testing.T) {
	for in, want := range map[string]string{
		"zig build run":                    "zig build run",
		"\n\nzig build run  \n\n":          "zig build run",
		"mkdir x\r\ncd x\r\n":              "mkdir x\ncd x",
		"echo 'a\tb'":                      "echo 'a\tb'",
		"":                                 "",
		"cat <<EOF\n$HOME `rm -rf /`\nEOF": "cat <<EOF\n$HOME `rm -rf /`\nEOF", // text, never run
	} {
		got, err := CleanCommand(in)
		if err != nil || got != want {
			t.Errorf("CleanCommand(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for name, in := range map[string]string{
		"escape":    "echo \x1b]0;pwned\x07",
		"nul":       "ls\x00",
		"bell":      "ls\x07",
		"del":       "ls\x7f",
		"c1":        "ls\u009b31m",
		"cr alone":  "ls\rrm -rf ~",
		"too long":  strings.Repeat("a", MaxCommandBytes+1),
		"too many":  strings.Repeat("ls\n", MaxCommandLines+1),
		"bad utf-8": "ls \xff",
	} {
		if _, err := CleanCommand(in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestOpenEditorAndTerminalNeedALauncher(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	makeTutFixture(t, dir, "series", true)
	srv := NewServer(dir)
	t.Cleanup(srv.Close)
	for _, p := range []string{"/-/native/open-editor", "/-/native/terminal"} {
		if w := nativePost(t, srv, p, `{"slug":"series"}`, ""); w.Code != http.StatusNotFound {
			t.Errorf("%s without a shell = %d, want 404", p, w.Code)
		}
	}
	// A shell that can't launch (no WorkspaceLauncher) is the same.
	srv.SetNative(newFakeNative())
	for _, p := range []string{"/-/native/open-editor", "/-/native/terminal"} {
		if w := nativePost(t, srv, p, `{"slug":"series"}`, ""); w.Code != http.StatusNotFound {
			t.Errorf("%s with a plain shell = %d, want 404", p, w.Code)
		}
	}
}

func TestOpenEditor(t *testing.T) {
	srv, f, ws := workspaceServer(t)
	if err := config.UpdateConfig(func(c *config.Config) error { c.Editor = "zed"; return nil }); err != nil {
		t.Fatal(err)
	}
	if w := nativePost(t, srv, "/-/native/open-editor", `{"slug":"series","path":"src/main.zig"}`, "wails://localhost"); w.Code != http.StatusNoContent {
		t.Fatalf("open = %d %s", w.Code, w.Body)
	}
	if w := nativePost(t, srv, "/-/native/open-editor", `{"slug":"series","path":""}`, ""); w.Code != http.StatusNoContent {
		t.Fatalf("open folder = %d %s", w.Code, w.Body)
	}
	want := [][2]string{{"zed", filepath.Join(ws, "src", "main.zig")}, {"zed", ws}}
	if len(f.editors) != 2 || f.editors[0] != want[0] || f.editors[1] != want[1] {
		t.Errorf("editor calls = %v, want %v", f.editors, want)
	}

	calls := len(f.editors)
	for name, tc := range map[string]struct {
		body, origin string
		want         int
	}{
		"cross-site":   {`{"slug":"series","path":"src/main.zig"}`, "https://evil.example", http.StatusForbidden},
		"traversal":    {`{"slug":"series","path":"../../etc/passwd"}`, "", http.StatusBadRequest},
		"absolute":     {`{"slug":"series","path":"/etc/passwd"}`, "", http.StatusBadRequest},
		"missing":      {`{"slug":"series","path":"src/nope.zig"}`, "", http.StatusNotFound},
		"bad slug":     {`{"slug":"../series","path":"src/main.zig"}`, "", http.StatusNotFound},
		"unknown slug": {`{"slug":"nope","path":"src/main.zig"}`, "", http.StatusConflict},
		"oversize":     {`{"slug":"series","path":"` + strings.Repeat("a", 5<<10) + `"}`, "", http.StatusRequestEntityTooLarge},
	} {
		if w := nativePost(t, srv, "/-/native/open-editor", tc.body, tc.origin); w.Code != tc.want {
			t.Errorf("%s: %d %s, want %d", name, w.Code, w.Body, tc.want)
		}
	}
	if len(f.editors) != calls {
		t.Errorf("refused requests reached the shell: %v", f.editors[calls:])
	}

	// The default app can open a file but not the folder.
	if err := config.UpdateConfig(func(c *config.Config) error { c.Editor = ""; return nil }); err != nil {
		t.Fatal(err)
	}
	if w := nativePost(t, srv, "/-/native/open-editor", `{"slug":"series","path":""}`, ""); w.Code != http.StatusConflict {
		t.Errorf("folder with no editor = %d, want 409", w.Code)
	}
	if w := nativePost(t, srv, "/-/native/open-editor", `{"slug":"series","path":"src/main.zig"}`, ""); w.Code != http.StatusNoContent {
		t.Errorf("file with the default app = %d", w.Code)
	}
	if got := f.editors[len(f.editors)-1]; got[0] != "" {
		t.Errorf("default-app call = %v", got)
	}
}

func TestNativeTerminal(t *testing.T) {
	srv, f, ws := workspaceServer(t)
	if err := config.UpdateConfig(func(c *config.Config) error { c.Terminal = config.TerminalsFor(goos())[0].ID; return nil }); err != nil {
		t.Fatal(err)
	}
	w := nativePost(t, srv, "/-/native/terminal", `{"slug":"series","command":"zig build run\n"}`, "http://wails.localhost")
	if w.Code != http.StatusOK {
		t.Fatalf("terminal = %d %s", w.Code, w.Body)
	}
	var out TerminalOutcome
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if !out.Typed || out.App != "Terminal" {
		t.Errorf("outcome = %+v", out)
	}
	want := [3]string{config.TerminalsFor(goos())[0].ID, ws, "zig build run"}
	if len(f.terminals) != 1 || f.terminals[0] != want {
		t.Errorf("terminal calls = %v, want %v", f.terminals, want)
	}
	// Open in Terminal: no command.
	if w := nativePost(t, srv, "/-/native/terminal", `{"slug":"series"}`, ""); w.Code != http.StatusOK {
		t.Errorf("open terminal = %d %s", w.Code, w.Body)
	}

	calls := len(f.terminals)
	long, _ := json.Marshal(strings.Repeat("x", MaxCommandBytes+1))
	for name, tc := range map[string]struct {
		body, origin string
		want         int
	}{
		"cross-site":  {`{"slug":"series","command":"ls"}`, "https://evil.example", http.StatusForbidden},
		"escape":      {`{"slug":"series","command":"ls \u001b[31m"}`, "", http.StatusBadRequest},
		"too long":    {`{"slug":"series","command":` + string(long) + `}`, "", http.StatusBadRequest},
		"bad slug":    {`{"slug":"../x","command":"ls"}`, "", http.StatusNotFound},
		"no such":     {`{"slug":"nope","command":"ls"}`, "", http.StatusConflict},
		"bad json":    {`{`, "", http.StatusBadRequest},
		"huge body":   {`{"slug":"series","command":"` + strings.Repeat("a", 20<<10) + `"}`, "", http.StatusRequestEntityTooLarge},
		"no dir sent": {`{"slug":"series","command":"ls","dir":"/etc"}`, "", http.StatusOK},
	} {
		if w := nativePost(t, srv, "/-/native/terminal", tc.body, tc.origin); w.Code != tc.want {
			t.Errorf("%s: %d %s, want %d", name, w.Code, w.Body, tc.want)
		}
	}
	// Only the last case (an ignored extra "dir") reached the shell, and at
	// the workspace.
	if len(f.terminals) != calls+1 || f.terminals[calls][1] != ws {
		t.Errorf("terminal calls = %v", f.terminals[calls:])
	}

	f.err = errors.New("boom")
	if w := nativePost(t, srv, "/-/native/terminal", `{"slug":"series","command":"ls"}`, ""); w.Code != http.StatusInternalServerError {
		t.Errorf("failing terminal = %d", w.Code)
	}
}

func TestTerminalNeedsAWorkspace(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	makeTutFixture(t, dir, "series", true)
	srv := NewServer(dir)
	t.Cleanup(srv.Close)
	f := &fakeLauncher{fakeNative: newFakeNative()}
	srv.SetNative(f)
	if w := nativePost(t, srv, "/-/native/terminal", `{"slug":"series","command":"ls"}`, ""); w.Code != http.StatusConflict {
		t.Errorf("no workspace = %d, want 409", w.Code)
	}
	if len(f.terminals) != 0 {
		t.Error("reached the shell without a workspace")
	}
}

func TestCompareEndpoint(t *testing.T) {
	srv, _, _ := workspaceServer(t)
	srv.SetNative(nil) // works in a plain browser too
	body := func(path, code string) string {
		b, _ := json.Marshal(map[string]string{"path": path, "code": code})
		return string(b)
	}
	w := nativePost(t, srv, "/-/compare/series", body("src/main.zig", "pub fn main() void {\n    std.debug.print(\"hello\\n\", .{});\n}\n"), "")
	if w.Code != http.StatusOK {
		t.Fatalf("compare = %d %s", w.Code, w.Body)
	}
	var res compareResult
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res.Same || res.Missing || res.Above != 2 || res.Below != 0 {
		t.Errorf("result = %+v", res)
	}
	var ops []string
	for _, l := range res.Lines {
		ops = append(ops, l.Op)
	}
	if strings.Join(ops, ",") != "same,yours,tutorial,same" && strings.Join(ops, ",") != "same,tutorial,yours,same" {
		t.Errorf("ops = %v", ops)
	}

	w = nativePost(t, srv, "/-/compare/series", body("src/new.zig", "x"), "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"missing":true`) {
		t.Errorf("missing file = %d %s", w.Code, w.Body)
	}
	for name, tc := range map[string]struct {
		slug, body, origin string
		want               int
	}{
		"cross-site":   {"series", body("src/main.zig", "x"), "https://evil.example", http.StatusForbidden},
		"traversal":    {"series", body("../../../etc/passwd", "x"), "", http.StatusBadRequest},
		"absolute":     {"series", body("/etc/passwd", "x"), "", http.StatusBadRequest},
		"folder":       {"series", body("src", "x"), "", http.StatusBadRequest},
		"no code":      {"series", body("src/main.zig", " "), "", http.StatusBadRequest},
		"no workspace": {"nope", body("src/main.zig", "x"), "", http.StatusConflict},
	} {
		if w := nativePost(t, srv, "/-/compare/"+tc.slug, tc.body, tc.origin); w.Code != tc.want {
			t.Errorf("%s: %d %s, want %d", name, w.Code, w.Body, tc.want)
		}
	}
}

func TestCompareListing(t *testing.T) {
	file := "package main\n\nimport \"fmt\"\n\nfunc add(a, b int) int {\n\treturn a + b\n}\n\nfunc main() {\n\tfmt.Println(add(1, 2))\n}\n"
	// An excerpt that matches: the rest of the file is above and below.
	res, err := compareListing("func add(a, b int) int {\n\treturn a + b\n}", file)
	if err != nil || !res.Same || res.Above != 4 || res.Below != 4 {
		t.Errorf("excerpt = %+v, %v", res, err)
	}
	// Trailing spaces don't count.
	if res, _ := compareListing("func main() {   \n\tfmt.Println(add(1, 2))\n}", file); !res.Same {
		t.Errorf("trailing spaces = %+v", res)
	}
	// A changed line shows on both sides.
	res, _ = compareListing("func add(a, b int) int {\n\treturn a - b\n}", file)
	got := map[string]string{}
	for _, l := range res.Lines {
		got[l.Op] += l.Text
	}
	if res.Same || got["tutorial"] != "\treturn a - b" || got["yours"] != "\treturn a + b" {
		t.Errorf("changed line = %+v", res)
	}
	// Long matching runs fold.
	long := strings.Repeat("x\n", 30)
	res, _ = compareListing(long+"new\n"+long, long+long)
	folded := 0
	for _, l := range res.Lines {
		if l.Op == "gap" {
			folded += l.N
		}
	}
	if folded == 0 || len(res.Lines) > 20 {
		t.Errorf("not folded: %d lines, %d folded", len(res.Lines), folded)
	}
	// An empty file: everything is the tutorial's.
	res, _ = compareListing("a\nb", "")
	if len(res.Lines) != 2 || res.Lines[0].Op != "tutorial" {
		t.Errorf("empty file = %+v", res)
	}
	if _, err := compareListing("", file); err == nil {
		t.Error("empty listing accepted")
	}
	if _, err := compareListing(strings.Repeat("a\n", maxListingLines+1), file); err == nil {
		t.Error("over-long listing accepted")
	}
}

func TestSettingsSavesLaunchers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := NewServer(t.TempDir())
	t.Cleanup(srv.Close)
	term := config.TerminalsFor(goos())[0].ID
	w := nativePost(t, srv, "/-/settings", `{"editor":"zed","terminal":"`+term+`"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("save = %d %s", w.Code, w.Body)
	}
	st := config.LoadSettings()
	if st.Editor != "zed" || st.Terminal != term {
		t.Errorf("settings = %+v", st)
	}
	for _, body := range []string{`{"editor":"notepad++; rm -rf ~"}`, `{"terminal":"bash -c x"}`} {
		if w := nativePost(t, srv, "/-/settings", body, ""); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", body, w.Code)
		}
	}
	// "" is the default.
	if w := nativePost(t, srv, "/-/settings", `{"editor":"","terminal":""}`, ""); w.Code != http.StatusOK {
		t.Errorf("reset = %d", w.Code)
	}
	if st := config.LoadSettings(); st.Editor != "" || st.Terminal != "" {
		t.Errorf("after reset = %+v", st)
	}
}

func TestReadingPageTools(t *testing.T) {
	srv, _, _ := workspaceServer(t)
	tutDir := filepath.Join(srv.tutorialsDir, "series")
	md := "# Part\n\n```zig src/main.zig\npub fn main() void {}\n```\n\n```bash\nzig build run\n```\n"
	if err := os.WriteFile(filepath.Join(tutDir, "part-01.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	page := func() string {
		w := httptestGet(srv, "/series/part-01.md")
		return w
	}
	desk := page()
	for _, want := range []string{`data-path="src/main.zig"`, `src/main.zig · zig`, `id="selectionBar"`, `id="listingMenu"`,
		`id="wsMenuButton"`, `id="askQuote"`, `"launch":true`, `latheReadingTools`} {
		if !strings.Contains(desk, want) {
			t.Errorf("desktop page lacks %q", want)
		}
	}
	srv.SetNative(nil)
	plain := page()
	if strings.Contains(plain, `id="wsMenuButton"`) || !strings.Contains(plain, `"launch":false`) || !strings.Contains(plain, `id="selectionBar"`) {
		t.Error("plain page: want the toolbar but no launchers or workspace menu")
	}
}

func httptestGet(srv *Server, path string) string {
	req, _ := http.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w.Body.String()
}

func TestListingPath(t *testing.T) {
	for info, want := range map[string]string{
		"zig":                              "",
		"zig src/envelope.zig":             "src/envelope.zig",
		`zig title="src/envelope.zig"`:     "src/envelope.zig",
		"go file=cmd/main.go":              "cmd/main.go",
		"rust path=Cargo.toml":             "Cargo.toml",
		"zig linenos src/a.zig":            "src/a.zig",
		"zig {hl_lines=[1]}":               "",
		"zig ../etc/passwd":                "",
		"zig /etc/passwd":                  "",
		"zig ~/x.zig":                      "",
		`zig src\x.zig`:                    "",
		`zig title="a b.zig"`:              "",
		"zig src/<script>.zig":             "",
		"zig -rf":                          "",
		"zig ./src/a.zig":                  "",
		"zig " + strings.Repeat("a/", 120): "",
	} {
		if got := listingPath(info); got != want {
			t.Errorf("listingPath(%q) = %q, want %q", info, got, want)
		}
	}
}
