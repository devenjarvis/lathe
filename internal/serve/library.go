package serve

import (
	"encoding/json"
	"errors"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
)

// The list page is the library: a Home view (what you're reading, then one row
// per shelf), the flat All tutorials grid (optionally narrowed to a language,
// the tutorials being written or the ones that need a look), a page per shelf,
// the Trash, and a sidebar that links them all. Views are query parameters on
// "/", rendered on the server, so every one of them works with scripts off:
// grid views take ?sort=recent|newest|title and ?group=none|language|status,
// and the existing client-side search/filter runs on top of whichever grid is
// rendered.

// Library views (?view=…).
const (
	viewHome      = "home"
	viewAll       = "all"
	viewWriting   = "writing"
	viewAttention = "attention"
	viewTrash     = "trash"
)

// Grid sorts (?sort=…).
const (
	sortRecent = "recent"
	sortNewest = "newest"
	sortTitle  = "title"
)

// Grid groupings (?group=…).
const (
	groupNone     = "none"
	groupLanguage = "language"
	groupStatus   = "status"
)

// otherLanguages is the group that collects tutorials whose language no other
// tutorial in the grid shares (and those with no language at all).
const otherLanguages = "Other languages"

// homeShelfCovers is how many covers a shelf row on Home shows before its
// "+N more" tile.
const homeShelfCovers = 4

// pickUpLimit caps the "Pick up where you left off" row (after the New
// tutorial cover).
const pickUpLimit = 8

// finishedRatio is how far into its last part a tutorial must be saved to
// count as finished rather than in progress.
const finishedRatio = 0.98

// maxShelfBody caps a POST /-/shelves body.
const maxShelfBody = 4096

// libCard is one cover: the tutorial plus the shelf it sits on, and what its
// ⋯ menu offers.
type libCard struct {
	*store.Tutorial
	ShelfID   string
	ShelfName string
	// Hidden marks a card that doesn't match the ?q= search, so a search
	// submitted with scripts off still narrows the grid. The client script
	// recomputes it from the search box.
	Hidden bool
	// MoveTo is every shelf, Unsorted last, for the menu's Move to shelf.
	// The same slice is shared by every card.
	MoveTo []store.Shelf
	// Reveal is set when the desktop shell can show this tutorial's
	// workspace in the file manager (it has one, and it exists).
	Reveal bool
	// RevealLabel names the file manager ("Show workspace in Finder").
	RevealLabel string
	// TrashKey is the Move to Trash shortcut as the menu shows it ("⌘⌫"),
	// "" where there is none.
	TrashKey string
}

// LastPart is the part "Add a part" opens (its extend form is at the end).
func (c *libCard) LastPart() string {
	if len(c.Parts) == 0 {
		return ""
	}
	return c.Parts[len(c.Parts)-1]
}

// Verified reports whether the tutorial has had a verify run, so its menu
// says "Verify again" rather than "Verify".
func (c *libCard) Verified() bool {
	return c.Status == store.StatusVerified || c.Status == store.StatusFailed || c.Status == store.StatusSkipped
}

// shelfRow is one shelf's row on Home.
type shelfRow struct {
	Shelf  store.Shelf
	Count  int
	Covers []*libCard
	More   int
}

// cardGroup is one group of a grid. An ungrouped grid is a single group with
// no label.
type cardGroup struct {
	Key   string
	Label string
	Cards []*libCard
	// Hidden: every card in it is hidden by ?q=.
	Hidden bool
}

// navItem is a sidebar link.
type navItem struct {
	Label   string
	Href    string
	Count   int
	Current bool
	// ShelfID is set on shelf links: they are drop targets for a dragged
	// cover.
	ShelfID string
}

// libraryNav is the sidebar.
type libraryNav struct {
	Home, All, Writing, Attention, Trash navItem
	Shelves                              []navItem
	Languages                            []navItem
	// CurrentLabel names the open view, for the collapsed (narrow) sidebar's
	// summary line.
	CurrentLabel string
}

// segOption is one choice of a segmented control made of links (sort, group
// by): each is a plain GET, so it works with scripts off.
type segOption struct {
	Label   string
	Href    string
	Current bool
}

// trashCard is one row of the Trash view.
type trashCard struct {
	store.TrashEntry
	// Expires is when the purge will delete it (zero for "Never").
	Expires time.Time
	// Taken: a tutorial with this slug is in the library again, so Restore
	// would be refused.
	Taken bool
}

// toast is the bottom notice a redirect asks for (?trashed=, ?restored=,
// ?moved=, ?shelfdeleted=), rendered on the server so it shows with scripts
// off; the page script takes it over (Undo, ⌘Z, dismiss).
type toast struct {
	Message string
	// UndoAction is the form action of the Undo button ("" for none).
	UndoAction string
	// UndoKey is how the Undo shortcut reads on this platform ("⌘Z").
	UndoKey string
	// Close is the page without the toast's query, for the dismiss link.
	Close string
}

// libraryPage is everything list.html renders.
type libraryPage struct {
	View string
	// Heading, Count and Empty describe a grid view.
	Heading string
	Count   int
	Empty   string
	// ShelfID and Tool are the active grid filters ("" when none).
	ShelfID string
	Tool    string
	Query   string
	// Shelf is the open shelf on a shelf page; Description is its one-line
	// note, and Editable is false for the virtual Unsorted shelf.
	Shelf    *store.Shelf
	Editable bool
	// Sort and Group are the grid's order and grouping, with their
	// segmented controls.
	Sort, Group   string
	Sorts, Groups []segOption
	CardGroups    []cardGroup
	Grouped       bool
	// NoMatches is set when ?q= hid every card, so the "no results" note
	// shows with scripts off.
	NoMatches bool
	Cards     []*libCard
	// PickUp and Rows are Home's rows.
	PickUp []*libCard
	// PickUpFull is set when the Pick up row hit pickUpLimit.
	PickUpFull bool
	Rows       []shelfRow
	Nav        libraryNav
	// Total is the number of tutorials in the library, ShelfCount the number
	// of shelves (Unsorted excluded).
	Total      int
	ShelfCount int
	// Trash is the Trash view's entries; TrashDays the retention (0: never).
	Trash     []trashCard
	TrashDays int
	// Toast is the notice to show at the bottom (nil for none).
	Toast *toast
	// UndoKey and TrashKey are the Undo and Move to Trash shortcuts as this
	// platform shows them ("⌘Z", "⌘⌫"), for the page script's toast and the
	// cover menu.
	UndoKey  string
	TrashKey string
	// Mac is set when the server runs on macOS, so the page binds ⌘Z
	// rather than Ctrl+Z for Undo.
	Mac bool
}

// libraryEnv is what the library needs beyond the tutorials and shelves:
// the Trash, and whether the desktop shell can reveal a workspace.
type libraryEnv struct {
	Trash     []store.TrashEntry
	TrashDays int
	// Reveal reports whether a tutorial's workspace can be shown in the file
	// manager (nil: never).
	Reveal func(slug string) bool
	GOOS   string
	Now    time.Time
}

// shelvesPath is where this server reads and writes shelves.json.
func (s *Server) shelvesPath() string {
	return store.ShelvesPath(s.tutorialsDir)
}

// readTutorials loads every stored tutorial, newest first.
func (s *Server) readTutorials() ([]*store.Tutorial, error) {
	entries, err := os.ReadDir(s.tutorialsDir)
	if err != nil {
		return nil, err
	}
	var tutorials []*store.Tutorial
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		tut, err := store.ReadMetadata(filepath.Join(s.tutorialsDir, e.Name()))
		if err != nil {
			continue
		}
		tutorials = append(tutorials, tut)
	}
	// Newest first: the JS-off order of every grid, and the order within each
	// shelf row. The client sort control re-orders the All grid from here.
	sort.SliceStable(tutorials, func(a, b int) bool {
		return tutorials[a].Created.After(tutorials[b].Created)
	})
	return tutorials, nil
}

// readShelvesForView loads shelves.json for rendering. A corrupt file is logged
// and treated as empty (everything Unsorted) so the library still opens; it is
// never overwritten from here.
func (s *Server) readShelvesForView() *store.Shelves {
	sh, err := store.ReadShelves(s.shelvesPath())
	if err != nil {
		log.Printf("lathe: %v; showing every tutorial as Unsorted", err)
		sh = &store.Shelves{}
	}
	return sh
}

// libraryEnvFor gathers the Trash and the reveal check for one render.
func (s *Server) libraryEnvFor() libraryEnv {
	env := libraryEnv{TrashDays: config.TrashDays(), GOOS: runtime.GOOS, Now: time.Now()}
	if list, err := store.ListTrash(s.tutorialsDir); err != nil {
		log.Printf("lathe: read the Trash: %v", err)
	} else {
		env.Trash = list
	}
	if s.revealer() != nil {
		env.Reveal = func(slug string) bool { return s.workspaceDir(slug) != "" }
	}
	return env
}

// workspaceDir is slug's workspace folder when it is set and exists, else "".
func (s *Server) workspaceDir(slug string) string {
	tutDir, ok := s.safeTutorialPath(slug)
	if !ok {
		return ""
	}
	ws, err := store.ReadWorkspace(tutDir)
	if err != nil || ws == nil || ws.Path == "" {
		return ""
	}
	p, err := store.ExpandPath(ws.Path)
	if err != nil {
		return ""
	}
	if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
		return ""
	}
	return p
}

// PurgeTrash deletes the Trash entries older than the reader's retention
// setting (trash_days). lathe serve and the desktop app call it on start.
func (s *Server) PurgeTrash() (int, error) {
	return store.PurgeTrash(s.tutorialsDir, config.TrashDays(), time.Now())
}

// beingWritten reports whether an agent is writing the tutorial (a new one,
// reserved by POST /-/new) or a part of it now.
func beingWritten(t *store.Tutorial) bool {
	return t.Status == store.StatusExtending || t.Status == store.StatusWriting
}

// needsALook reports whether the tutorial failed verification.
func needsALook(t *store.Tutorial) bool {
	return t.Status == store.StatusFailed
}

// started reports whether the reader has saved a place in the tutorial (on a
// part that still exists).
func started(t *store.Tutorial) bool {
	return t.Progress != nil && cardProgress(t) != nil
}

// inProgress reports whether the reader has started the tutorial and not yet
// reached the end of its last part. Saved progress on a part that no longer
// exists doesn't count (cardProgress shows nothing for it either).
func inProgress(t *store.Tutorial) bool {
	if !started(t) {
		return false
	}
	return !(isLastPart(t, t.Progress.Part) && t.Progress.Ratio >= finishedRatio)
}

// toolKey is the case-folded language name used for ?tool= and grouping.
func toolKey(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

func hasTool(t *store.Tutorial, key string) bool {
	for _, tool := range t.Tools {
		if toolKey(tool.Name) == key {
			return true
		}
	}
	return false
}

// matchesQuery mirrors the client search: a case-insensitive substring of the
// title, slug, topic, tags, repo, tools or shelf.
func matchesQuery(c *libCard, q string) bool {
	if q == "" {
		return true
	}
	hay := []string{c.Title, c.Slug, c.Topic, strings.Join(c.Tags, " "), c.RepoDisplay(), c.ShelfName}
	for _, tool := range c.Tools {
		hay = append(hay, tool.Name+" "+tool.Version)
	}
	return strings.Contains(strings.ToLower(strings.Join(hay, " ")), strings.ToLower(q))
}

func viewHref(params ...string) string {
	if len(params) == 0 {
		return "/"
	}
	v := url.Values{}
	for i := 0; i+1 < len(params); i += 2 {
		if params[i+1] != "" {
			v.Set(params[i], params[i+1])
		}
	}
	if len(v) == 0 {
		return "/"
	}
	return "/?" + v.Encode()
}

// Status groups, in display order.
var statusGroups = []struct{ Key, Label string }{
	{"writing", "Being written"},
	{"in-progress", "In progress"},
	{"attention", "Needs a look"},
	{"verified", "Verified"},
	{"finished", "Finished"},
	{"not-started", "Not started"},
}

// statusGroup files a tutorial under one status group. A failed verify wins
// (it needs the reader's attention whatever else is true), then reading
// state, then a passed verify. A tutorial still being written (status
// writing) has its own group. "Finished" (read to the end, never verified)
// is shown only when something is in it.
func statusGroup(t *store.Tutorial) string {
	switch {
	case t.Status == store.StatusWriting:
		// A New tutorial run's reservation: no parts to read or verify yet.
		return "writing"
	case needsALook(t):
		return "attention"
	case inProgress(t):
		return "in-progress"
	case t.Status == store.StatusVerified:
		return "verified"
	case started(t):
		return "finished"
	default:
		return "not-started"
	}
}

// groupCards splits a grid into groups. Language groups are by each
// tutorial's first (primary) tool, largest first; a language only one
// tutorial uses folds into "Other languages", with the tutorials that name
// none, last.
func groupCards(cards []*libCard, by string) []cardGroup {
	switch by {
	case groupStatus:
		buckets := map[string][]*libCard{}
		for _, c := range cards {
			k := statusGroup(c.Tutorial)
			buckets[k] = append(buckets[k], c)
		}
		var out []cardGroup
		for _, g := range statusGroups {
			if len(buckets[g.Key]) > 0 {
				out = append(out, cardGroup{Key: g.Key, Label: g.Label, Cards: buckets[g.Key]})
			}
		}
		return out
	case groupLanguage:
		type lang struct {
			name  string
			cards []*libCard
		}
		langs := map[string]*lang{}
		var other []*libCard
		for _, c := range cards {
			if len(c.Tools) == 0 || toolKey(c.Tools[0].Name) == "" {
				other = append(other, c)
				continue
			}
			k := toolKey(c.Tools[0].Name)
			if langs[k] == nil {
				langs[k] = &lang{name: strings.TrimSpace(c.Tools[0].Name)}
			}
			langs[k].cards = append(langs[k].cards, c)
		}
		var keys []string
		for k, l := range langs {
			if len(l.cards) < 2 {
				other = append(other, l.cards...)
				continue
			}
			keys = append(keys, k)
		}
		sort.Slice(keys, func(a, b int) bool {
			if la, lb := len(langs[keys[a]].cards), len(langs[keys[b]].cards); la != lb {
				return la > lb
			}
			return keys[a] < keys[b]
		})
		var out []cardGroup
		for _, k := range keys {
			out = append(out, cardGroup{Key: "lang-" + k, Label: langs[k].name, Cards: langs[k].cards})
		}
		if len(other) > 0 {
			// Keep the grid's order inside the folded group.
			pos := map[*libCard]int{}
			for i, c := range cards {
				pos[c] = i
			}
			sort.SliceStable(other, func(a, b int) bool { return pos[other[a]] < pos[other[b]] })
			out = append(out, cardGroup{Key: "lang-other", Label: otherLanguages, Cards: other})
		}
		return out
	}
	return []cardGroup{{Key: "all", Cards: cards}}
}

// sortCards orders a grid in place. Recently read puts the tutorials with a
// saved place first, most recent first, then the rest newest first.
func sortCards(cards []*libCard, by string) {
	switch by {
	case sortTitle:
		sort.SliceStable(cards, func(a, b int) bool {
			return strings.ToLower(cards[a].Title) < strings.ToLower(cards[b].Title)
		})
	case sortRecent:
		sort.SliceStable(cards, func(a, b int) bool {
			ra, rb := started(cards[a].Tutorial), started(cards[b].Tutorial)
			if ra != rb {
				return ra
			}
			if ra {
				return cards[a].Progress.UpdatedAt.After(cards[b].Progress.UpdatedAt)
			}
			return cards[a].Created.After(cards[b].Created)
		})
	default:
		sort.SliceStable(cards, func(a, b int) bool { return cards[a].Created.After(cards[b].Created) })
	}
}

// undoKey and trashKey are the Undo and Move to Trash shortcuts as goos shows
// them.
func undoKey(goos string) string {
	if goos == "darwin" {
		return "⌘Z"
	}
	return "Ctrl+Z"
}

func trashKey(goos string) string {
	if goos == "darwin" {
		return "⌘⌫"
	}
	return ""
}

// revealLabel names the file manager the way the platform does.
func revealLabel(goos string) string {
	switch goos {
	case "darwin":
		return "Show workspace in Finder"
	case "windows":
		return "Show workspace in Explorer"
	}
	return "Show workspace folder"
}

// buildLibrary turns the tutorials, the shelf list and the request's query
// into the page model. ok is false for a ?shelf= that names no shelf.
func buildLibrary(tutorials []*store.Tutorial, shelves *store.Shelves, q url.Values, env libraryEnv) (*libraryPage, bool) {
	ordered := append([]store.Shelf{}, shelves.Shelves...)
	moveTo := append(append([]store.Shelf{}, ordered...), store.UnsortedShelf())
	cards := make([]*libCard, len(tutorials))
	perShelf := map[string][]*libCard{}
	for i, t := range tutorials {
		sh := shelves.ShelfOf(t.Slug)
		c := &libCard{Tutorial: t, ShelfID: sh.ID, ShelfName: sh.Name, MoveTo: moveTo, RevealLabel: revealLabel(env.GOOS), TrashKey: trashKey(env.GOOS)}
		if env.Reveal != nil {
			c.Reveal = env.Reveal(t.Slug)
		}
		cards[i] = c
		perShelf[sh.ID] = append(perShelf[sh.ID], c)
	}

	page := &libraryPage{
		View:       strings.TrimSpace(q.Get("view")),
		ShelfID:    strings.TrimSpace(q.Get("shelf")),
		Tool:       toolKey(q.Get("tool")),
		Query:      strings.TrimSpace(q.Get("q")),
		Total:      len(tutorials),
		ShelfCount: len(shelves.Shelves),
		TrashDays:  env.TrashDays,
		UndoKey:    undoKey(env.GOOS),
		TrashKey:   trashKey(env.GOOS),
		Mac:        env.GOOS == "darwin",
	}
	switch {
	case page.ShelfID != "" || page.Tool != "":
		page.View = viewAll
	case page.View == viewAll, page.View == viewWriting, page.View == viewAttention, page.View == viewTrash:
	case page.Query != "":
		page.View = viewAll
	default:
		page.View = viewHome
	}

	// Sidebar.
	nav := &page.Nav
	nav.Home = navItem{Label: "Home", Href: "/"}
	nav.All = navItem{Label: "All tutorials", Href: viewHref("view", viewAll), Count: len(cards)}
	nav.Writing = navItem{Label: "Being written", Href: viewHref("view", viewWriting)}
	nav.Attention = navItem{Label: "Needs a look", Href: viewHref("view", viewAttention)}
	nav.Trash = navItem{Label: "Trash", Href: viewHref("view", viewTrash), Count: len(env.Trash)}
	for _, t := range tutorials {
		if beingWritten(t) {
			nav.Writing.Count++
		}
		if needsALook(t) {
			nav.Attention.Count++
		}
	}
	if len(perShelf[store.UnsortedShelfID]) > 0 || page.ShelfID == store.UnsortedShelfID {
		ordered = append(ordered, store.UnsortedShelf())
	}
	for _, sh := range ordered {
		nav.Shelves = append(nav.Shelves, navItem{
			Label:   sh.Name,
			Href:    viewHref("shelf", sh.ID),
			Count:   len(perShelf[sh.ID]),
			Current: page.ShelfID == sh.ID,
			ShelfID: sh.ID,
		})
	}
	type lang struct {
		name  string
		count int
	}
	langs := map[string]*lang{}
	for _, t := range tutorials {
		seen := map[string]bool{}
		for _, tool := range t.Tools {
			k := toolKey(tool.Name)
			if k == "" || seen[k] {
				continue
			}
			seen[k] = true
			if langs[k] == nil {
				langs[k] = &lang{name: strings.TrimSpace(tool.Name)}
			}
			langs[k].count++
		}
	}
	keys := make([]string, 0, len(langs))
	for k := range langs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool {
		if langs[keys[a]].count != langs[keys[b]].count {
			return langs[keys[a]].count > langs[keys[b]].count
		}
		return keys[a] < keys[b]
	})
	for _, k := range keys {
		nav.Languages = append(nav.Languages, navItem{
			Label:   langs[k].name,
			Href:    viewHref("tool", k),
			Count:   langs[k].count,
			Current: page.Tool == k,
		})
	}

	switch page.View {
	case viewHome:
		nav.Home.Current = true
		nav.CurrentLabel = nav.Home.Label
		var writing, reading []*libCard
		for _, c := range cards {
			switch {
			case beingWritten(c.Tutorial):
				writing = append(writing, c)
			case inProgress(c.Tutorial):
				reading = append(reading, c)
			}
		}
		sort.SliceStable(reading, func(a, b int) bool {
			return reading[a].Progress.UpdatedAt.After(reading[b].Progress.UpdatedAt)
		})
		page.PickUp = append(writing, reading...)
		if len(page.PickUp) >= pickUpLimit {
			page.PickUp, page.PickUpFull = page.PickUp[:pickUpLimit], true
		}
		for _, sh := range ordered {
			on := perShelf[sh.ID]
			row := shelfRow{Shelf: sh, Count: len(on), Covers: on}
			if len(on) > homeShelfCovers {
				row.Covers, row.More = on[:homeShelfCovers], len(on)-homeShelfCovers
			}
			page.Rows = append(page.Rows, row)
		}
		return page, true

	case viewTrash:
		nav.Trash.Current = true
		page.Heading = nav.Trash.Label
		nav.CurrentLabel = page.Heading
		live := map[string]bool{}
		for _, t := range tutorials {
			live[t.Slug] = true
		}
		for _, e := range env.Trash {
			tc := trashCard{TrashEntry: e, Taken: live[e.Slug]}
			if env.TrashDays > 0 {
				tc.Expires = e.TrashedAt.Add(time.Duration(env.TrashDays) * 24 * time.Hour)
			}
			page.Trash = append(page.Trash, tc)
		}
		page.Count = len(page.Trash)
		return page, true

	case viewWriting:
		nav.Writing.Current = true
		page.Heading, page.Empty = nav.Writing.Label, "Nothing is being written right now. When your agent writes a new tutorial or part, it shows up here."
		page.Cards = filterCards(cards, func(c *libCard) bool { return beingWritten(c.Tutorial) })
	case viewAttention:
		nav.Attention.Current = true
		page.Heading, page.Empty = nav.Attention.Label, "Nothing needs a look: no tutorial has failed verification."
		page.Cards = filterCards(cards, func(c *libCard) bool { return needsALook(c.Tutorial) })
	default:
		switch {
		case page.ShelfID != "":
			sh, ok := shelves.Get(page.ShelfID)
			if !ok {
				return nil, false
			}
			page.Shelf = &sh
			page.Editable = !sh.Unsorted()
			page.Heading, page.Empty = sh.Name, "Nothing on this shelf yet. Drag a cover onto it in the sidebar, or use a cover's ⋯ menu › Move to shelf."
			page.Cards = append([]*libCard(nil), perShelf[sh.ID]...)
		case page.Tool != "":
			label := page.Tool
			if l := langs[page.Tool]; l != nil {
				label = l.name
			}
			page.Heading, page.Empty = label, "No tutorials use "+label+"."
			page.Cards = filterCards(cards, func(c *libCard) bool { return hasTool(c.Tutorial, page.Tool) })
		default:
			nav.All.Current = true
			page.Heading, page.Empty = nav.All.Label, "No tutorials yet."
			page.Cards = append([]*libCard(nil), cards...)
		}
	}
	nav.CurrentLabel = page.Heading

	// Sort and group the grid. A shelf page opens on what you read last;
	// the other grids keep their newest-first order.
	defSort := sortNewest
	if page.Shelf != nil {
		defSort = sortRecent
	}
	page.Sort = strings.TrimSpace(q.Get("sort"))
	if page.Sort != sortRecent && page.Sort != sortNewest && page.Sort != sortTitle {
		page.Sort = defSort
	}
	page.Group = strings.TrimSpace(q.Get("group"))
	if page.Group != groupLanguage && page.Group != groupStatus {
		page.Group = groupNone
	}
	base := []string{"shelf", page.ShelfID, "tool", page.Tool, "q", page.Query}
	if page.ShelfID == "" && page.Tool == "" {
		base = append(base, "view", page.View)
	}
	param := func(key, val, def string) []string {
		out := append([]string{}, base...)
		if key != "sort" && page.Sort != defSort {
			out = append(out, "sort", page.Sort)
		}
		if key != "group" && page.Group != groupNone {
			out = append(out, "group", page.Group)
		}
		if val != def {
			out = append(out, key, val)
		}
		return out
	}
	for _, o := range []struct{ val, label string }{{sortRecent, "Recently read"}, {sortNewest, "Newest"}, {sortTitle, "Title"}} {
		page.Sorts = append(page.Sorts, segOption{Label: o.label, Href: viewHref(param("sort", o.val, defSort)...), Current: page.Sort == o.val})
	}
	for _, o := range []struct{ val, label string }{{groupNone, "None"}, {groupLanguage, "Language"}, {groupStatus, "Status"}} {
		page.Groups = append(page.Groups, segOption{Label: o.label, Href: viewHref(param("group", o.val, groupNone)...), Current: page.Group == o.val})
	}
	sortCards(page.Cards, page.Sort)

	page.Count = len(page.Cards)
	visible := 0
	for _, c := range page.Cards {
		c.Hidden = !matchesQuery(c, page.Query)
		if !c.Hidden {
			visible++
		}
	}
	page.CardGroups = groupCards(page.Cards, page.Group)
	page.Grouped = page.Group != groupNone
	for i := range page.CardGroups {
		g := &page.CardGroups[i]
		g.Hidden = true
		for _, c := range g.Cards {
			if !c.Hidden {
				g.Hidden = false
				break
			}
		}
	}
	page.NoMatches = page.Query != "" && len(page.Cards) > 0 && visible == 0
	return page, true
}

// withToast fills page.Toast from the query a redirect left (?trashed=<id>,
// ?restored=<slug>, ?moved=<slug>&to=<shelf>, ?shelfdeleted=<n>). Messages
// are built here from what's on disk, never from free text in the URL.
func withToast(page *libraryPage, q url.Values, tutorials []*store.Tutorial, shelves *store.Shelves, trash []store.TrashEntry) {
	title := func(slug string) string {
		for _, t := range tutorials {
			if t.Slug == slug {
				return t.Title
			}
		}
		return ""
	}
	clean := url.Values{}
	for k, v := range q {
		switch k {
		case "trashed", "restored", "moved", "to", "shelfdeleted":
		default:
			clean[k] = v
		}
	}
	closeHref := "/"
	if len(clean) > 0 {
		closeHref = "/?" + clean.Encode()
	}
	t := &toast{UndoKey: page.UndoKey, Close: closeHref}
	switch {
	case q.Get("trashed") != "":
		id := q.Get("trashed")
		for _, e := range trash {
			if e.ID == id {
				t.Message = "Moved “" + e.Title + "” to Trash"
				t.UndoAction = "/-/trash/" + url.PathEscape(e.ID) + "/restore"
			}
		}
	case q.Get("restored") != "":
		if name := title(q.Get("restored")); name != "" {
			t.Message = "Restored “" + name + "”"
		}
	case q.Get("moved") != "":
		name := title(q.Get("moved"))
		sh, ok := shelves.Get(q.Get("to"))
		if name != "" && ok {
			t.Message = "Moved “" + name + "” to " + sh.Name
		}
	case q.Get("shelfdeleted") != "":
		switch n := q.Get("shelfdeleted"); n {
		case "0":
			t.Message = "Shelf deleted"
		case "1":
			t.Message = "Shelf deleted. Its tutorial is on Unsorted now"
		default:
			if strings.Trim(n, "0123456789") == "" && len(n) < 6 {
				t.Message = "Shelf deleted. Its " + n + " tutorials are on Unsorted now"
			}
		}
	}
	if t.Message != "" {
		page.Toast = t
	}
}

func filterCards(cards []*libCard, keep func(*libCard) bool) []*libCard {
	var out []*libCard
	for _, c := range cards {
		if keep(c) {
			out = append(out, c)
		}
	}
	return out
}

// listJSON is one entry of the list page's JSON form (Accept:
// application/json), which the page fetches on a back/forward (bfcache)
// restore to refresh covers in place.
type listJSON struct {
	Slug      string       `json:"slug"`
	Status    store.Status `json:"status"`
	Shelf     string       `json:"shelf"`
	ShelfName string       `json:"shelfName"`
	// InProgress: started and not finished, so Home's Pick up row lists it.
	InProgress bool          `json:"inProgress"`
	Progress   *CardProgress `json:"progress"`
}

func (s *Server) writeListJSON(w http.ResponseWriter, tutorials []*store.Tutorial, shelves *store.Shelves) {
	list := make([]listJSON, 0, len(tutorials))
	for _, t := range tutorials {
		sh := shelves.ShelfOf(t.Slug)
		list = append(list, listJSON{
			Slug:      t.Slug,
			Status:    t.Status,
			Shelf:     sh.ID,
			ShelfName: sh.Name,
			// Being written sorts first in Pick up on its own account.
			InProgress: inProgress(t) && !beingWritten(t),
			Progress:   cardProgress(t),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

// handleShelfCreate is POST /-/shelves: make a new shelf from a name. It
// takes a form post (the sidebar's "+ New shelf" form, which works with
// scripts off and is answered with a redirect to the new shelf) or JSON
// {"name": …} (answered with the shelf as JSON).
func (s *Server) handleShelfCreate(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	isJSON := ct == "application/json"
	var name string
	if isJSON {
		var body struct {
			Name string `json:"name"`
		}
		if !readJSONBody(w, r, maxShelfBody, &body) {
			return
		}
		name = body.Name
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, maxShelfBody)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "could not read request", http.StatusBadRequest)
			return
		}
		name = r.PostFormValue("name")
	}

	var created store.Shelf
	_, err := store.UpdateShelves(s.shelvesPath(), func(sh *store.Shelves) error {
		var err error
		created, err = sh.Create(name)
		return errShelfInput{err}.orNil()
	})
	if err != nil {
		var bad errShelfInput
		if errors.As(err, &bad) {
			http.Error(w, bad.err.Error(), http.StatusBadRequest)
			return
		}
		log.Printf("lathe: create shelf: %v", err)
		http.Error(w, "could not save shelves", http.StatusInternalServerError)
		return
	}
	if isJSON || strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"id":   created.ID,
			"name": created.Name,
			"url":  viewHref("shelf", created.ID),
		})
		return
	}
	http.Redirect(w, r, viewHref("shelf", created.ID), http.StatusSeeOther)
}

// errShelfInput marks a validation error (the reader's input), as opposed to a
// failure to read or write the file.
type errShelfInput struct{ err error }

func (e errShelfInput) Error() string { return e.err.Error() }
func (e errShelfInput) orNil() error {
	if e.err == nil {
		return nil
	}
	return e
}
