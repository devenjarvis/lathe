package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/devenjarvis/lathe/internal/acp"
	"github.com/devenjarvis/lathe/internal/mcp"
	"github.com/devenjarvis/lathe/internal/queue"
	"github.com/devenjarvis/lathe/internal/store"
)

// Shelf suggestions (issue #135): the reader's agent proposes the library's
// filing, and the reader applies it.
//
//	POST /-/shelves/sort                 run /lathe-shelf sort (agent → worker → handoff)
//	POST /-/shelves/sort/apply           apply the proposal (form: slug=… per ticked row; JSON {"slugs": […]})
//	POST /-/shelves/sort/discard         drop the proposal
//	POST /-/shelves/{id}/split/suggest   run /lathe-shelf split <id>
//	POST /-/shelves/{id}/split           apply the shelf's split proposal
//	POST /-/shelves/{id}/split/dismiss   "Not now" on the split banner
//
// A hosted run gets a fresh session in an empty scratch dir, may read the
// tutorials dir and nothing else (no writes, no commands), and its Lathe MCP
// server is in propose mode (`lathe mcp --propose sort|split`): it can record
// a proposal and move nothing. The catalog of tutorials and shelves is in the
// prompt, so the agent needs no file access at all. Every POST is
// sameOrigin-checked.

const (
	jobShelfSort  = "shelf-sort"
	jobShelfSplit = "shelf-split"

	// shelvesJobKey is the active-run key for the library's one shelf job
	// at a time ("#…" can't collide with a slug's "slug#job" or
	// "slug/part").
	shelvesJobKey = "#shelves"

	shelfRunTimeout = 20 * time.Minute
)

// shelfCatalogEntry is one tutorial as the shelf prompt lists it.
type shelfCatalogEntry struct {
	Slug  string   `json:"slug"`
	Title string   `json:"title"`
	Topic string   `json:"topic,omitempty"`
	Tags  []string `json:"tags,omitempty"`
	Tools []string `json:"tools,omitempty"`
	Shelf string   `json:"shelf"`
}

// shelfCatalog is the prompt's view of the library: the shelves with counts
// and every tutorial (only those on shelf onlyShelf, when set).
func (s *Server) shelfCatalog(onlyShelf string) ([]store.ShelfListing, []shelfCatalogEntry, error) {
	list, err := store.ListShelves(s.tutorialsDir)
	if err != nil {
		return nil, nil, err
	}
	tutorials, err := s.readTutorials()
	if err != nil {
		return nil, nil, err
	}
	shelves := s.readShelvesForView()
	var out []shelfCatalogEntry
	for _, t := range tutorials {
		sh := shelves.ShelfOf(t.Slug)
		if onlyShelf != "" && sh.ID != onlyShelf {
			continue
		}
		e := shelfCatalogEntry{Slug: t.Slug, Title: t.Title, Topic: t.Topic, Tags: t.Tags, Shelf: sh.ID}
		for _, tool := range t.Tools {
			e.Tools = append(e.Tools, strings.TrimSpace(tool.Name+" "+tool.Version))
		}
		out = append(out, e)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Slug < out[b].Slug })
	return list, out, nil
}

func jsonBlock(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

// shelfPrompt frames a hosted /lathe-shelf run: what it is for, the
// catalog, the skill text, then the invocation.
func shelfPrompt(job string, shelf store.Shelf, list []store.ShelfListing, tutorials []shelfCatalogEntry, command string) string {
	var b strings.Builder
	switch job {
	case jobShelfSort:
		b.WriteString("You are sorting the reader's Lathe library onto shelves for the \"Sort my shelves…\" button in Lathe's Settings, which hosts you. ")
		b.WriteString("Follow the /lathe-shelf skill below in sort mode, start to finish, without stopping to ask questions: nobody is at a terminal, only a reader watching your progress.\n\n")
		b.WriteString("- Record one proposal per tutorial — every tutorial listed below, including ones whose shelf stays the same — with the lathe_shelf_propose tool (or `lathe shelf propose <slug> <shelf>` if you don't have it).\n")
	case jobShelfSplit:
		fmt.Fprintf(&b, "You are proposing how to split the reader's Lathe shelf %q (id %s, %d tutorials) in two, for the split banner on its page in the Lathe reader, which hosts you. ", shelf.Name, shelf.ID, len(tutorials))
		b.WriteString("Follow the /lathe-shelf skill below in split mode, start to finish, without stopping to ask questions: nobody is at a terminal, only a reader watching your progress.\n\n")
		fmt.Fprintf(&b, "- Record the split once with the lathe_shelf_split_propose tool (it defaults to %s), or `lathe shelf propose-split %s --group … --group …` if you don't have it. Every tutorial listed below goes in exactly one of the two groups.\n", shelf.ID, shelf.ID)
	}
	b.WriteString("- Everything you need is below; you don't need to read files. Lathe lets this session read the tutorials folder and nothing else, and run no commands.\n")
	b.WriteString("- Proposals move nothing: the reader reviews yours and applies it with one click.\n")
	b.WriteString("- Keep a short plan (read the library, decide shelves, record) and finish with a two- or three-line summary for the reader (markdown).\n\n")
	b.WriteString("The reader's shelves, in their order (Unsorted is the virtual shelf for unfiled tutorials):\n\n```json\n")
	b.WriteString(jsonBlock(list))
	b.WriteString("\n```\n\n")
	if job == jobShelfSplit {
		fmt.Fprintf(&b, "The tutorials on %s:\n\n```json\n", shelf.Name)
	} else {
		b.WriteString("Every tutorial in the library, with the id of the shelf it is on now:\n\n```json\n")
	}
	b.WriteString(jsonBlock(tutorials))
	b.WriteString("\n```\n\n")
	writeSkill(&b, "lathe-shelf")
	b.WriteString(command)
	return b.String()
}

// mcpServersPropose is the session's Lathe MCP server in propose mode.
func (h *agentHost) mcpServersPropose(mode, shelf string) []acp.MCPServer {
	servers := h.mcpServers("x") // the slug is replaced below
	for i := range servers {
		if servers[i].Name == "lathe" {
			args := []string{"mcp", "--propose", mode}
			if shelf != "" {
				args = append(args, "--shelf", shelf)
			}
			servers[i].Args = args
		}
	}
	return servers
}

// activeShelfJob returns the shelf job in flight, if any.
func (h *agentHost) activeShelfJob() (*agentRun, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	id, ok := h.active[shelvesJobKey]
	if !ok {
		return nil, false
	}
	r, ok := h.runs[id]
	return r, ok
}

// startShelfJob opens a fresh session for a sort or split run and starts it.
func (h *agentHost) startShelfJob(job, tutorialsDir string, shelf store.Shelf, prompt, command string) (*agentRun, error) {
	if _, busy := h.activeShelfJob(); busy {
		return nil, errShelfJobBusy
	}
	c, err := h.ensure()
	if err != nil {
		return nil, err
	}
	scratch, err := os.MkdirTemp("", "lathe-shelves-")
	if err != nil {
		return nil, fmt.Errorf("make a scratch dir: %w", err)
	}
	if real, rerr := filepath.EvalSymlinks(scratch); rerr == nil {
		scratch = real
	}
	read := tutorialsDir
	if real, rerr := filepath.EvalSymlinks(read); rerr == nil {
		read = real
	}
	mode, shelfArg := mcp.ProposeSort, ""
	if job == jobShelfSplit {
		mode, shelfArg = mcp.ProposeSplit, shelf.ID
	}
	ctx, cancel := context.WithTimeout(context.Background(), agentStartTimeout)
	sid, err := c.NewSession(ctx, scratch, h.mcpServersPropose(mode, shelfArg)...)
	cancel()
	if err != nil {
		_ = os.RemoveAll(scratch)
		return nil, err
	}
	c.SetPolicy(sid, &acp.Policy{Job: job, Read: []string{read, scratch}})
	started := time.Now()
	run, err := h.launch(c, shelvesJobKey, job, jobTarget{shelf: shelf.ID}, sid, prompt, command, shelfRunTimeout, func() string {
		c.SetPolicy(sid, nil)
		_ = os.RemoveAll(scratch)
		return settleShelfJob(job, tutorialsDir, shelf.ID, started)
	})
	if err != nil {
		c.SetPolicy(sid, nil)
		_ = os.RemoveAll(scratch)
		if errors.Is(err, errJobBusy) {
			err = errShelfJobBusy
		}
		return nil, err
	}
	return run, nil
}

var errShelfJobBusy = errors.New("the agent is already working on your shelves")

// settleShelfJob says, for the run's final event, when it ended without
// recording a proposal. Nothing needs undoing: proposals move nothing.
func settleShelfJob(job, tutorialsDir, shelfID string, started time.Time) string {
	sug, err := store.ReadShelfSuggestions(tutorialsDir)
	if err != nil {
		return ""
	}
	switch job {
	case jobShelfSort:
		if sug.Sort == nil {
			return "It ended without proposing any shelves."
		}
	case jobShelfSplit:
		if p := sug.Splits[shelfID]; p == nil || p.Created.Before(started.Add(-time.Second)) {
			return "It ended without proposing a split."
		}
	}
	return ""
}

// runningShelfJob reports the hosted shelf job, if one is running and is
// job (for a split, on shelfID).
func (s *Server) runningShelfJob(job, shelfID string) *jobRunInfo {
	agent := s.hostedAgent()
	if agent == nil {
		return nil
	}
	run, ok := agent.activeShelfJob()
	if !ok || run.job != job || (job == jobShelfSplit && run.target.shelf != shelfID) {
		return nil
	}
	return &jobRunInfo{ID: run.id, Job: run.job, Started: run.started.UnixMilli()}
}

// startShelf routes a shelf job: hosted agent, else a /lathe-work worker,
// else the command to paste.
func (s *Server) startShelf(w http.ResponseWriter, job string, shelf store.Shelf) {
	command := "/lathe-shelf sort"
	if job == jobShelfSplit {
		command = "/lathe-shelf split " + shelf.ID
	}
	var notice string
	if agent := s.hostedAgent(); agent != nil {
		onlyShelf := ""
		if job == jobShelfSplit {
			onlyShelf = shelf.ID
		}
		list, tutorials, err := s.shelfCatalog(onlyShelf)
		if err != nil {
			http.Error(w, "could not read the library: "+err.Error(), http.StatusInternalServerError)
			return
		}
		run, err := agent.startShelfJob(job, s.tutorialsDir, shelf, shelfPrompt(job, shelf, list, tutorials, command), command)
		switch {
		case err == nil:
			writeJSON(w, map[string]string{"mode": "agent", "runId": run.id})
			return
		case errors.Is(err, errShelfJobBusy):
			http.Error(w, "conflict: "+err.Error(), http.StatusConflict)
			return
		}
		notice = "The agent (" + agent.command + ") could not start: " + err.Error()
	}
	if s.queue.WorkerConnected() {
		writeQueued(w, s.queue.Enqueue(queue.Job{Type: queue.JobShelf, Command: command}))
		return
	}
	if notice != "" {
		writeHandoffNotice(w, command, notice)
		return
	}
	writeHandoff(w, command)
}

func (s *Server) handleShelfSort(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if tuts, err := s.readTutorials(); err != nil || len(tuts) == 0 {
		http.Error(w, "There are no tutorials to sort yet.", http.StatusBadRequest)
		return
	}
	if agent := s.hostedAgent(); agent != nil {
		if _, busy := agent.activeShelfJob(); busy {
			http.Error(w, "conflict: "+errShelfJobBusy.Error(), http.StatusConflict)
			return
		}
	}
	// A new run starts from a clean slate, whichever path takes it.
	if err := store.ResetSortProposal(s.tutorialsDir); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.startShelf(w, jobShelfSort, store.Shelf{})
}

// splitShelf resolves {id} to a real (not Unsorted) shelf, or answers 404.
func (s *Server) splitShelf(w http.ResponseWriter, r *http.Request) (store.Shelf, bool) {
	sh, ok := s.readShelvesForView().Get(r.PathValue("id"))
	if !ok || sh.Unsorted() {
		http.NotFound(w, r)
		return store.Shelf{}, false
	}
	return sh, true
}

func (s *Server) handleShelfSplitSuggest(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	sh, ok := s.splitShelf(w, r)
	if !ok {
		return
	}
	if n := len(store.SlugsOnShelf(s.tutorialsDir, s.readShelvesForView(), sh.ID)); n < 2 {
		http.Error(w, "A shelf needs at least two tutorials to split.", http.StatusBadRequest)
		return
	}
	s.startShelf(w, jobShelfSplit, sh)
}

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json") ||
		strings.HasPrefix(r.Header.Get("Content-Type"), "application/json")
}

func (s *Server) handleShelfSortApply(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	slugs := []string{}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			Slugs []string `json:"slugs"`
		}
		if !readJSONBody(w, r, 64<<10, &body) {
			return
		}
		if body.Slugs != nil {
			slugs = body.Slugs
		}
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		slugs = append(slugs, r.PostForm["slug"]...)
	}
	res, err := store.ApplySort(s.tutorialsDir, slugs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"moved": res.Moved, "created": len(res.Created)})
		return
	}
	http.Redirect(w, r, "/-/settings?sorted="+strconv.Itoa(res.Moved)+"#library", http.StatusSeeOther)
}

func (s *Server) handleShelfSortDiscard(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := store.DiscardSort(s.tutorialsDir); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if wantsJSON(r) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/-/settings#library", http.StatusSeeOther)
}

func (s *Server) handleShelfSplitApply(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	sh, ok := s.splitShelf(w, r)
	if !ok {
		return
	}
	res, err := store.ApplySplit(s.tutorialsDir, sh.ID)
	if err != nil {
		log.Printf("lathe: split shelf %s: %v", sh.ID, err)
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if wantsJSON(r) {
		writeJSON(w, map[string]string{"kept": res.Kept.ID, "into": res.Into.ID})
		return
	}
	http.Redirect(w, r, "/?"+url.Values{"shelf": {res.Kept.ID}, "split": {res.Kept.ID}, "into": {res.Into.ID}}.Encode(), http.StatusSeeOther)
}

func (s *Server) handleShelfSplitDismiss(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	sh, ok := s.splitShelf(w, r)
	if !ok {
		return
	}
	n := len(store.SlugsOnShelf(s.tutorialsDir, s.readShelvesForView(), sh.ID))
	if err := store.DismissSplit(s.tutorialsDir, sh.ID, n); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if wantsJSON(r) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, viewHref("shelf", sh.ID), http.StatusSeeOther)
}

// splitBanner is the yellow banner on a shelf page that has grown past the
// split threshold.
type splitBanner struct {
	ShelfID   string
	ShelfName string
	Count     int
	// Sides is the proposed split (empty until the agent proposed one).
	Sides []splitSide
	// Run is the hosted split job in flight (set by the handler).
	Run *jobRunInfo
}

// splitSide is one half of a proposed split.
type splitSide struct {
	Name string
	// Keeps: this half keeps the shelf (renamed); else it becomes a new
	// shelf (Existing: joins a shelf of that name).
	Keeps, Existing bool
	Cards           []*libCard
}

// buildSplitBanner returns the banner for a shelf page, or nil when the
// shelf is not due a split.
func buildSplitBanner(sh store.Shelf, cards []*libCard, shelves *store.Shelves, env libraryEnv) *splitBanner {
	if sh.Unsorted() || env.Suggestions == nil {
		return nil
	}
	if !store.SplitDue(len(cards), env.SplitAt, env.Suggestions.Dismissed[sh.ID]) {
		return nil
	}
	b := &splitBanner{ShelfID: sh.ID, ShelfName: sh.Name, Count: len(cards)}
	slugs := make([]string, 0, len(cards))
	bySlug := map[string]*libCard{}
	for _, c := range cards {
		slugs = append(slugs, c.Slug)
		bySlug[c.Slug] = c
	}
	groups, ok := env.Suggestions.CurrentSplit(slugs, sh.ID)
	if !ok {
		return b
	}
	for i, g := range groups {
		side := splitSide{Name: g.Name, Keeps: i == 0}
		if i == 1 {
			if other, ok := shelves.Find(g.Name); ok && other.ID != sh.ID {
				side.Existing = true
			}
		}
		for _, slug := range g.Slugs {
			side.Cards = append(side.Cards, bySlug[slug])
		}
		sort.SliceStable(side.Cards, func(a, b int) bool {
			return strings.ToLower(side.Cards[a].Title) < strings.ToLower(side.Cards[b].Title)
		})
		b.Sides = append(b.Sides, side)
	}
	return b
}

// sortReview is Settings › Library's review of a sort proposal.
type sortReview struct {
	Rows []sortRow
	// Moves counts the rows that change shelf; NewShelves names the
	// shelves applying would create.
	Moves      int
	NewShelves []string
}

// sortRow is one tutorial in the review: where it is and where it would go.
type sortRow struct {
	Slug, Title string
	From, To    string
	New, Same   bool
}

// buildSortReview turns the pending sort proposal into the review list, or
// nil when there is none.
func (s *Server) buildSortReview() *sortReview {
	sug, err := store.ReadShelfSuggestions(s.tutorialsDir)
	if err != nil || sug.Sort == nil {
		return nil
	}
	tutorials, err := s.readTutorials()
	if err != nil {
		return nil
	}
	shelves := s.readShelvesForView()
	rv := &sortReview{}
	newSeen := map[string]bool{}
	for _, t := range tutorials {
		pick, ok := sug.Sort.Picks[t.Slug]
		if !ok {
			continue
		}
		from := shelves.ShelfOf(t.Slug)
		row := sortRow{Slug: t.Slug, Title: t.Title, From: from.Name, To: pick}
		if to, ok := shelves.Find(pick); ok {
			row.To = to.Name
			row.Same = to.ID == from.ID
		} else {
			row.New = true
			if key := strings.ToLower(pick); !newSeen[key] {
				newSeen[key] = true
				rv.NewShelves = append(rv.NewShelves, pick)
			}
		}
		if !row.Same {
			rv.Moves++
		}
		rv.Rows = append(rv.Rows, row)
	}
	if len(rv.Rows) == 0 {
		return nil
	}
	// Moves first (grouped by destination), unchanged rows last.
	sort.SliceStable(rv.Rows, func(a, b int) bool {
		ra, rb := rv.Rows[a], rv.Rows[b]
		if ra.Same != rb.Same {
			return !ra.Same
		}
		if ra.To != rb.To {
			return strings.ToLower(ra.To) < strings.ToLower(rb.To)
		}
		return strings.ToLower(ra.Title) < strings.ToLower(rb.Title)
	})
	return rv
}
