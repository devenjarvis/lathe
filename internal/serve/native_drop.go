package serve

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
)

// Folder drops in the desktop app (#147).
//
// The shell reports a folder dropped on one of its windows with ReportDrop.
// The server checks the path, works out what the drop means for the page it
// landed on, keeps it under a one-time token and sends the page a "drop"
// event on its command stream (GET /-/native/commands) with the plan:
//
//   - on a tutorial: "Use <path> as this tutorial's workspace?" Use / Cancel
//     → POST /-/native/drop/workspace {token} (store.SetWorkspace);
//   - on Home, a folder with part-01.md: "Add to your shelf" with the title,
//     shelf and tags prefilled → POST /-/native/drop/add {token, title,
//     shelf, tags} (store.Store, the same as `lathe store`, then the shelf).
//
// The page never sends a path: it can only accept (or let expire) a drop the
// shell reported, so these endpoints can't be used to read or copy any other
// folder. Like every /-/native/ endpoint they 404 without a shell.

// Drop limits. A tutorial folder holds a few markdown files; anything much
// bigger is a project folder dropped on Home by mistake.
const (
	maxDropPath  = 4096
	maxDropFiles = 500
	maxDropBytes = 50 << 20
	// dropTTL is how long a drop waits for the reader's answer.
	dropTTL = 15 * time.Minute
	// maxPendingDrops bounds the drops waiting at once.
	maxPendingDrops = 16
)

// Drop kinds.
const (
	dropWorkspace = "workspace"
	dropAdd       = "add"
	dropError     = "error"
)

// pendingDrop is a drop the reader hasn't answered yet.
type pendingDrop struct {
	kind    string
	path    string // the folder, symlinks resolved
	slug    string // workspace: the tutorial it is for; add: the slug it gets
	created time.Time
}

// dropBook holds the pending drops by token. The zero value is ready to use.
type dropBook struct {
	mu sync.Mutex
	m  map[string]pendingDrop
}

func (b *dropBook) put(d pendingDrop) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.m == nil {
		b.m = map[string]pendingDrop{}
	}
	b.pruneLocked(d.created)
	for len(b.m) >= maxPendingDrops {
		// Forget the oldest: the reader moved on from it.
		var oldest string
		for k, v := range b.m {
			if oldest == "" || v.created.Before(b.m[oldest].created) {
				oldest = k
			}
		}
		delete(b.m, oldest)
	}
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	tok := hex.EncodeToString(raw[:])
	b.m[tok] = d
	return tok
}

// take removes and returns the pending drop of kind under token.
func (b *dropBook) take(token, kind string, now time.Time) (pendingDrop, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneLocked(now)
	d, ok := b.m[token]
	if !ok || d.kind != kind {
		return pendingDrop{}, false
	}
	delete(b.m, token)
	return d, true
}

func (b *dropBook) pruneLocked(now time.Time) {
	for k, v := range b.m {
		if now.Sub(v.created) > dropTTL {
			delete(b.m, k)
		}
	}
}

// shelfChoice is a shelf the add dialog offers.
type shelfChoice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// dropPlan is the "drop" event a page gets: what the drop would do, for
// its confirmation dialog.
type dropPlan struct {
	Kind    string `json:"kind"`
	Token   string `json:"token,omitempty"`
	Path    string `json:"path,omitempty"`
	Display string `json:"display,omitempty"`
	// Workspace: the tutorial and the workspace it would replace.
	Tutorial string `json:"tutorial,omitempty"`
	Current  string `json:"current,omitempty"`
	// Add: the prefilled title, tags and shelf, the shelves to pick from,
	// the slug it is stored under and how many parts it has.
	Slug    string        `json:"slug,omitempty"`
	Title   string        `json:"title,omitempty"`
	Tags    []string      `json:"tags,omitempty"`
	Shelf   string        `json:"shelf,omitempty"`
	Shelves []shelfChoice `json:"shelves,omitempty"`
	Parts   int           `json:"parts,omitempty"`
	// Error: why the drop can't be used.
	Message string `json:"message,omitempty"`
}

// ReportDrop is the shell's way in: paths were dropped on window (its app
// window name), on the page whose command-stream client id is client (the
// drop target's data-drop-client; either may be empty). It works out the
// plan, keeps the drop for the page's answer and sends the page its "drop"
// event. It returns the plan (Kind "" when nothing was sent).
func (s *Server) ReportDrop(window, client string, paths []string) dropPlan {
	c := s.cmds.dropTarget(window, client)
	if c == nil || len(paths) == 0 {
		return dropPlan{}
	}
	plan := s.planDrop(c.path, paths, time.Now())
	if plan.Kind == "" {
		return plan
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		return dropPlan{}
	}
	select {
	case c.drops <- raw:
	default:
		// The page isn't reading its stream; it'll get the next one.
	}
	return plan
}

// dropFail is an error plan.
func dropFail(format string, args ...any) dropPlan {
	return dropPlan{Kind: dropError, Message: fmt.Sprintf(format, args...)}
}

// planDrop works out what dropping paths on the page at pagePath means.
func (s *Server) planDrop(pagePath string, paths []string, now time.Time) dropPlan {
	u, err := url.Parse(pagePath)
	if err != nil {
		return dropPlan{}
	}
	var tut *store.Tutorial
	home := u.Path == "/"
	if !home {
		t, ok := s.tutorialPage(u.Path)
		if !ok {
			return dropFail("Drop a folder on a tutorial to use it as the workspace, or on Home to add a tutorial.")
		}
		tut = t
	}
	if len(paths) != 1 {
		return dropFail("Drop one folder at a time.")
	}
	dir, err := s.dropDir(paths[0])
	if err != nil {
		return dropFail("%s", err.Error())
	}
	display := displayPath(dir)

	if tut != nil {
		tutDir, ok := s.safeTutorialPath(tut.Slug)
		if !ok {
			return dropPlan{}
		}
		_, current := workspaceDisplay(tutDir)
		if current == display {
			return dropFail("%s is already this tutorial's workspace.", display)
		}
		tok := s.drops.put(pendingDrop{kind: dropWorkspace, path: dir, slug: tut.Slug, created: now})
		return dropPlan{Kind: dropWorkspace, Token: tok, Path: dir, Display: display, Tutorial: tutorialName(tut), Slug: tut.Slug, Current: current}
	}

	plan, err := s.planAdd(dir)
	if err != nil {
		return dropFail("%s", err.Error())
	}
	plan.Token = s.drops.put(pendingDrop{kind: dropAdd, path: dir, slug: plan.Slug, created: now})
	plan.Path, plan.Display = dir, display
	return plan
}

// dropDir checks a dropped path: an absolute, clean path to an existing
// folder (symlinks resolved), not the filesystem root and not inside Lathe's
// own store. It returns the resolved folder.
func (s *Server) dropDir(raw string) (string, error) {
	if raw == "" || len(raw) > maxDropPath || strings.ContainsAny(raw, "\x00\r\n") {
		return "", dropMsg("Lathe can't use that path.")
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return "", dropMsg("Lathe can't use that path.")
		}
	}
	if !filepath.IsAbs(raw) {
		return "", dropMsg("Lathe can't use that path.")
	}
	clean := filepath.Clean(raw)
	real, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", dropMsg(fmt.Sprintf("%s isn't there any more.", displayPath(clean)))
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", dropMsg(fmt.Sprintf("%s isn't there any more.", displayPath(clean)))
	}
	if !info.IsDir() {
		return "", dropMsg(fmt.Sprintf("%s is a file. Drop a folder.", filepath.Base(clean)))
	}
	if filepath.Dir(real) == real {
		return "", dropMsg("Pick a folder, not the whole disk.")
	}
	lathe := filepath.Dir(filepath.Clean(s.tutorialsDir))
	if realLathe, err := filepath.EvalSymlinks(lathe); err == nil {
		lathe = realLathe
	}
	if pathWithin(real, lathe) {
		return "", dropMsg("That folder is inside Lathe's own store.")
	}
	return real, nil
}

// dropMsg is a reason a drop can't be used, worded for the reader.
type dropMsg string

func (m dropMsg) Error() string { return string(m) }

// partOne matches the first part's file name.
const partOne = "part-01.md"

// partFile matches a part's file name, as store.Store detects them.
var partFile = regexp.MustCompile(`^part-\d+\.md$`)

// planAdd checks that dir is a tutorial folder Lathe can store (part-01.md,
// a size a tutorial has, a slug not taken) and prefills the add dialog the
// way `lathe store` would: the slug from the folder name, the title from its
// metadata.json if it has one, else the slug title-cased.
func (s *Server) planAdd(dir string) (dropPlan, error) {
	name := filepath.Base(dir)
	if fi, err := os.Lstat(filepath.Join(dir, partOne)); err != nil || !fi.Mode().IsRegular() {
		return dropPlan{}, dropMsg(fmt.Sprintf("%s has no part-01.md, so it isn't a tutorial folder.", name))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return dropPlan{}, dropMsg(fmt.Sprintf("Lathe can't read %s.", name))
	}
	files, parts := 0, 0
	var size int64
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue // store.Store copies top-level files only, never links
		}
		files++
		if partFile.MatchString(e.Name()) {
			parts++
		}
		if info, err := e.Info(); err == nil {
			size += info.Size()
		}
		if files > maxDropFiles || size > maxDropBytes {
			return dropPlan{}, dropMsg(fmt.Sprintf("%s is too big to be a tutorial folder.", name))
		}
	}

	slug := strings.TrimPrefix(name, "lathe-")
	if err := store.ValidateSlug(slug); err != nil || strings.HasPrefix(slug, "-") || strings.HasPrefix(slug, "_") || strings.HasPrefix(slug, ".") {
		return dropPlan{}, dropMsg(fmt.Sprintf("Lathe can't name a tutorial %q. Rename the folder.", name))
	}
	if tutDir, ok := s.safeTutorialPath(slug); !ok {
		return dropPlan{}, dropMsg(fmt.Sprintf("Lathe can't name a tutorial %q. Rename the folder.", name))
	} else if _, err := os.Stat(tutDir); err == nil {
		title := slug
		if t, err := store.ReadMetadata(tutDir); err == nil {
			title = tutorialName(t)
		}
		return dropPlan{}, dropMsg(fmt.Sprintf("Your library already has “%s” (%s). Rename the folder to add this one, or replace it with lathe store.", title, slug))
	}

	plan := dropPlan{Kind: dropAdd, Slug: slug, Title: store.SlugToTitle(slug), Parts: parts, Shelf: store.UnsortedShelfID}
	// A folder that was stored before carries its own title and tags.
	if t, err := store.ReadMetadata(dir); err == nil {
		if tt := strings.TrimSpace(t.Title); tt != "" {
			plan.Title = tt
		}
		plan.Tags = t.Tags
	}
	if sh, err := store.ReadShelves(s.shelvesPath()); err == nil {
		for _, x := range sh.Shelves {
			plan.Shelves = append(plan.Shelves, shelfChoice{ID: x.ID, Name: x.Name})
		}
	}
	u := store.UnsortedShelf()
	plan.Shelves = append(plan.Shelves, shelfChoice{ID: u.ID, Name: u.Name})
	return plan, nil
}

// handleDropWorkspace makes a dropped folder the tutorial's workspace.
func (s *Server) handleDropWorkspace(w http.ResponseWriter, r *http.Request) {
	if s.nativeShell() == nil {
		http.NotFound(w, r)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var payload struct {
		Token string `json:"token"`
	}
	if !readJSONBody(w, r, maxNativeBody, &payload) {
		return
	}
	d, ok := s.drops.take(payload.Token, dropWorkspace, time.Now())
	if !ok {
		http.Error(w, "That drop has expired. Drop the folder again.", http.StatusGone)
		return
	}
	tutDir, ok := s.safeTutorialPath(d.slug)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if _, err := store.ReadMetadata(tutDir); err != nil {
		http.NotFound(w, r)
		return
	}
	setup, err := store.SetWorkspace(tutDir, d.path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{
		"slug":    d.slug,
		"path":    setup.Path,
		"display": displayPath(setup.Path),
		"created": setup.Created,
		"gitInit": setup.GitInit,
	})
}

// maxDropTitle bounds the add dialog's title.
const maxDropTitle = 200

// handleDropAdd stores a dropped tutorial folder, as `lathe store <path>
// --title … --tags …` does, and puts it on the chosen shelf.
func (s *Server) handleDropAdd(w http.ResponseWriter, r *http.Request) {
	if s.nativeShell() == nil {
		http.NotFound(w, r)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var payload struct {
		Token string `json:"token"`
		Title string `json:"title"`
		Shelf string `json:"shelf"`
		Tags  string `json:"tags"`
	}
	if !readJSONBody(w, r, maxNativeBody, &payload) {
		return
	}
	title := strings.Join(strings.Fields(payload.Title), " ")
	if len(title) > maxDropTitle {
		http.Error(w, "That title is too long.", http.StatusBadRequest)
		return
	}
	shelf := strings.TrimSpace(payload.Shelf)
	if shelf == "" {
		shelf = store.UnsortedShelfID
	}
	if shelf != store.UnsortedShelfID {
		sh, err := store.ReadShelves(s.shelvesPath())
		if err != nil {
			http.Error(w, "could not read your shelves: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if _, ok := sh.Get(shelf); !ok {
			http.Error(w, "That shelf is gone. Pick another.", http.StatusBadRequest)
			return
		}
	}
	// store.Store writes into the user's tutorials dir; this server must be
	// reading the same one, or the tutorial would land somewhere it can't
	// show.
	if dir, err := config.TutorialsDir(); err != nil || filepath.Clean(dir) != filepath.Clean(s.tutorialsDir) {
		http.Error(w, "This window can't add tutorials to your library.", http.StatusConflict)
		return
	}
	d, ok := s.drops.take(payload.Token, dropAdd, time.Now())
	if !ok {
		http.Error(w, "That drop has expired. Drop the folder again.", http.StatusGone)
		return
	}
	// Check again: the folder may have changed while the dialog was open.
	if _, err := s.planAdd(d.path); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	tut, err := store.Store(d.path, store.StoreOptions{
		Title: title,
		Tags:  store.SplitTags([]string{payload.Tags}),
		Slug:  d.slug,
	})
	if err != nil {
		http.Error(w, "could not add it: "+err.Error(), http.StatusInternalServerError)
		return
	}
	note := ""
	if shelf != store.UnsortedShelfID {
		if _, err := store.UpdateShelves(s.shelvesPath(), func(sh *store.Shelves) error { return sh.Assign(tut.Slug, shelf) }); err != nil {
			note = "It is in your library, but Lathe could not put it on the shelf: " + err.Error()
		}
	}
	writeJSON(w, map[string]any{"slug": tut.Slug, "title": tut.Title, "url": "/" + tut.Slug + "/", "note": note})
}
