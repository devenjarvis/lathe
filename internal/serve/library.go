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
	"sort"
	"strings"

	"github.com/devenjarvis/lathe/internal/store"
)

// The list page is the library: a Home view (what you're reading, then one row
// per shelf), the flat All tutorials grid (optionally narrowed to a shelf, a
// language, the tutorials being written or the ones that need a look), and a
// sidebar that links them all. Views are query parameters on "/", rendered on
// the server, so every one of them works with scripts off; the existing
// client-side search/filter/sort runs on top of whichever grid is rendered.

// Library views (?view=…).
const (
	viewHome      = "home"
	viewAll       = "all"
	viewWriting   = "writing"
	viewAttention = "attention"
)

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

// libCard is one cover: the tutorial plus the shelf it sits on.
type libCard struct {
	*store.Tutorial
	ShelfID   string
	ShelfName string
	// Hidden marks a card that doesn't match the ?q= search, so a search
	// submitted with scripts off still narrows the grid. The client script
	// recomputes it from the search box.
	Hidden bool
}

// shelfRow is one shelf's row on Home.
type shelfRow struct {
	Shelf  store.Shelf
	Count  int
	Covers []*libCard
	More   int
}

// navItem is a sidebar link.
type navItem struct {
	Label   string
	Href    string
	Count   int
	Current bool
}

// libraryNav is the sidebar.
type libraryNav struct {
	Home, All, Writing, Attention navItem
	Shelves                       []navItem
	Languages                     []navItem
	// CurrentLabel names the open view, for the collapsed (narrow) sidebar's
	// summary line.
	CurrentLabel string
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

// beingWritten reports whether an agent is writing a part of the tutorial now.
func beingWritten(t *store.Tutorial) bool {
	return t.Status == store.StatusExtending
}

// needsALook reports whether the tutorial failed verification.
func needsALook(t *store.Tutorial) bool {
	return t.Status == store.StatusFailed
}

// inProgress reports whether the reader has started the tutorial and not yet
// reached the end of its last part. Saved progress on a part that no longer
// exists doesn't count (cardProgress shows nothing for it either).
func inProgress(t *store.Tutorial) bool {
	if t.Progress == nil || cardProgress(t) == nil {
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
		v.Set(params[i], params[i+1])
	}
	return "/?" + v.Encode()
}

// buildLibrary turns the tutorials, the shelf list and the request's query
// into the page model. ok is false for a ?shelf= that names no shelf.
func buildLibrary(tutorials []*store.Tutorial, shelves *store.Shelves, q url.Values) (*libraryPage, bool) {
	cards := make([]*libCard, len(tutorials))
	perShelf := map[string][]*libCard{}
	for i, t := range tutorials {
		sh := shelves.ShelfOf(t.Slug)
		cards[i] = &libCard{Tutorial: t, ShelfID: sh.ID, ShelfName: sh.Name}
		perShelf[sh.ID] = append(perShelf[sh.ID], cards[i])
	}

	page := &libraryPage{
		View:       strings.TrimSpace(q.Get("view")),
		ShelfID:    strings.TrimSpace(q.Get("shelf")),
		Tool:       toolKey(q.Get("tool")),
		Query:      strings.TrimSpace(q.Get("q")),
		Total:      len(tutorials),
		ShelfCount: len(shelves.Shelves),
	}
	switch {
	case page.ShelfID != "" || page.Tool != "":
		page.View = viewAll
	case page.View == viewAll, page.View == viewWriting, page.View == viewAttention:
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
	for _, t := range tutorials {
		if beingWritten(t) {
			nav.Writing.Count++
		}
		if needsALook(t) {
			nav.Attention.Count++
		}
	}
	ordered := append([]store.Shelf{}, shelves.Shelves...)
	if len(perShelf[store.UnsortedShelfID]) > 0 {
		ordered = append(ordered, store.UnsortedShelf())
	}
	for _, sh := range ordered {
		nav.Shelves = append(nav.Shelves, navItem{
			Label:   sh.Name,
			Href:    viewHref("shelf", sh.ID),
			Count:   len(perShelf[sh.ID]),
			Current: page.ShelfID == sh.ID,
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

	case viewWriting:
		nav.Writing.Current = true
		page.Heading, page.Empty = nav.Writing.Label, "Nothing is being written right now. When your agent writes a new part, it shows up here."
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
			page.Heading, page.Empty = sh.Name, "Nothing on this shelf yet."
			page.Cards = perShelf[sh.ID]
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
			page.Cards = cards
		}
	}
	nav.CurrentLabel = page.Heading
	page.Count = len(page.Cards)
	visible := 0
	for _, c := range page.Cards {
		c.Hidden = !matchesQuery(c, page.Query)
		if !c.Hidden {
			visible++
		}
	}
	page.NoMatches = page.Query != "" && len(page.Cards) > 0 && visible == 0
	return page, true
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
