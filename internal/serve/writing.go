package serve

import (
	"bufio"
	"bytes"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/devenjarvis/lathe/internal/store"
)

// "Being written": what the reader sees while the hosted agent writes a new
// tutorial into a reservation (agent_new.go). The same model feeds three
// places — the dark "Writing Part 1" cover on Home and in Being written, the
// progress page at /{slug}/ (writing.html), and GET /-/new/{slug}/progress,
// which both poll to stay live.
//
// Every signal is real:
//   - the step and the sources come from the reservation's writing.json, which
//     the /lathe skill reports through lathe_progress / `lathe writing-step`
//     (store.RecordWriting); with no report, what is on disk sets a floor
//     (pinned versions, sources read, a draft started);
//   - the draft is the part-01.md the agent is writing in the run's scratch
//     dir, which Lathe made and only reads here;
//   - "Needs you" is the run waiting for a reply (agentRun.waiting), with the
//     question the agent ended its turn on and the label and quick replies it
//     reported just before.

// maxDraftBytes caps how much of the draft the progress page renders.
const maxDraftBytes = 512 << 10

// writingStepView is one of the four steps.
type writingStepView struct {
	N     int    `json:"n"`
	Label string `json:"label"`
	// State is "done", "active" or "todo".
	State string `json:"state"`
}

// writingNeeds is the question the agent is waiting on.
type writingNeeds struct {
	// Label is the short form for the cover ("confirm versions").
	Label        string        `json:"label"`
	Question     string        `json:"question,omitempty"`
	QuestionHTML template.HTML `json:"questionHTML,omitempty"`
	Replies      []string      `json:"replies,omitempty"`
}

// writingRun identifies the hosted run, so the panel can attach to it.
type writingRun struct {
	ID      string `json:"id"`
	Started int64  `json:"started"`
}

// writingView is a reservation's progress, for the cover, the page and the
// JSON endpoint.
type writingView struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
	// State is "writing", "stored" (the agent stored it: open it) or "gone"
	// (the run ended without storing; the reservation was removed).
	State  string            `json:"state"`
	Href   string            `json:"href,omitempty"`
	Active int               `json:"active"`
	Step   string            `json:"step"`
	Steps  []writingStepView `json:"steps"`
	// Sources are the pages read so far.
	Sources []store.Source `json:"sources"`
	// DraftTitle and Sections come from the draft's h1 and h2s; DraftHTML is
	// the draft rendered (the page only).
	DraftTitle string        `json:"draftTitle,omitempty"`
	Sections   []string      `json:"sections"`
	DraftHTML  template.HTML `json:"draftHTML,omitempty"`
	Needs      *writingNeeds `json:"needs,omitempty"`
	Run        *writingRun   `json:"run,omitempty"`
	// Live is false when no hosted run is writing it any more (lathe serve
	// was restarted mid-run): the reservation is left over.
	Live bool `json:"live"`
}

// NeedsHref is where the cover's "Needs you" link goes.
func (v *writingView) NeedsHref() string { return "/" + v.Slug + "/#needs" }

// writingFor builds the view for a reservation. withDraft renders the draft
// (the page and its poll; the cover needs only the step).
func (s *Server) writingFor(tut *store.Tutorial, tutDir string, withDraft bool) *writingView {
	v := &writingView{Slug: tut.Slug, Title: tutorialName(tut), State: "writing"}
	if tut.Status != store.StatusWriting {
		v.State = "stored"
		v.Href = "/" + tut.Slug + "/"
		return v
	}
	w, err := store.ReadWriting(tutDir)
	if err != nil {
		log.Printf("lathe: read %s progress: %v", tut.Slug, err)
	}
	if w == nil {
		w = &store.Writing{}
	}
	v.Sources = w.Sources
	if v.Sources == nil {
		v.Sources = []store.Source{}
	}
	v.Sections = []string{}

	var run *agentRun
	if agent := s.hostedAgent(); agent != nil {
		if r, ok := agent.activeJob(tut.Slug); ok && r.job == jobNew {
			run = r
		}
	}
	var draft []byte
	if run != nil {
		v.Live = true
		v.Run = &writingRun{ID: run.id, Started: run.started.UnixMilli()}
		draft = readDraft(run.target.draft, run.target.scratch)
	}
	if len(draft) > 0 {
		v.DraftTitle, v.Sections = draftOutline(draft)
		if withDraft {
			if html, err := RenderMarkdown(draft); err == nil {
				v.DraftHTML = template.HTML(html)
			}
		}
	}

	// The step the agent reported, with what is on disk as a floor.
	active := store.WritingStepIndex(w.Step)
	floor := 1
	if w.Step == "" && len(tut.Tools) > 0 {
		floor = 2 // the reader pinned the versions in the form
	}
	if len(w.Sources) > 0 && floor < 2 {
		floor = 2
	}
	if len(draft) > 0 {
		floor = 3
	}
	if active < floor {
		active = floor
	}
	v.Active = active
	for i, st := range store.WritingSteps {
		sv := writingStepView{N: i + 1, Label: st.Label, State: "todo"}
		switch {
		case i+1 < active:
			sv.State = "done"
		case i+1 == active:
			sv.State = "active"
			v.Step = st.Label
		}
		v.Steps = append(v.Steps, sv)
	}

	if run != nil {
		if q, waiting, replied := run.question(); waiting {
			n := &writingNeeds{Label: "answer a question", Question: q}
			if w.Ask != nil && w.Ask.At.After(replied) {
				n.Label, n.Replies = w.Ask.Label, w.Ask.Replies
			}
			if q != "" {
				if html, err := RenderMarkdown([]byte(q)); err == nil {
					n.QuestionHTML = template.HTML(html)
				}
			}
			v.Needs = n
		}
	}
	return v
}

// readDraft reads the draft part the agent is writing: <scratch>/<slug>/
// part-01.md. The agent controls the scratch dir, so a link out of it (to a
// file it may not read) is refused rather than followed.
func readDraft(dir, scratch string) []byte {
	if dir == "" || scratch == "" {
		return nil
	}
	p := filepath.Join(dir, "part-01.md")
	real, err := filepath.EvalSymlinks(p)
	if err != nil || !pathWithin(real, scratch) {
		return nil
	}
	fi, err := os.Lstat(real)
	if err != nil || !fi.Mode().IsRegular() {
		return nil
	}
	f, err := os.Open(real)
	if err != nil {
		return nil
	}
	defer f.Close() //nolint:errcheck
	b, _ := io.ReadAll(io.LimitReader(f, maxDraftBytes))
	if len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	return b
}

// draftOutline returns the draft's title (its h1) and section headings (its
// h2s), skipping fenced code.
func draftOutline(md []byte) (string, []string) {
	var title string
	sections := []string{}
	fence := ""
	sc := bufio.NewScanner(bytes.NewReader(md))
	sc.Buffer(make([]byte, 64<<10), maxDraftBytes)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \t")
		trimmed := strings.TrimLeft(line, " ")
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			mark := trimmed[:3]
			switch fence {
			case "":
				fence = mark
			case mark:
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "# ") && title == "":
			title = strings.TrimSpace(line[2:])
		case strings.HasPrefix(line, "## "):
			if h := strings.TrimSpace(strings.TrimRight(line[3:], "#")); h != "" {
				sections = append(sections, h)
			}
		}
	}
	return title, sections
}

// writingForSlug reads a reservation by slug. ok is false when there is no
// such tutorial; a stored one comes back with State "stored".
func (s *Server) writingForSlug(slug string, withDraft bool) (*writingView, bool) {
	tutDir, ok := s.safeTutorialPath(slug)
	if !ok {
		return nil, false
	}
	tut, err := store.ReadMetadata(tutDir)
	if err != nil {
		return nil, false
	}
	return s.writingFor(tut, tutDir, withDraft), true
}

// handleWritingProgress is GET /-/new/{slug}/progress: the reservation's
// progress as JSON, for the cover and the progress page to poll. A slug that
// is gone answers {"state":"gone"} (the run ended without storing).
func (s *Server) handleWritingProgress(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	v, ok := s.writingForSlug(slug, r.URL.Query().Get("draft") != "0")
	if !ok {
		if err := store.ValidateSlug(slug); err != nil {
			http.NotFound(w, r)
			return
		}
		v = &writingView{Slug: slug, State: "gone", Steps: []writingStepView{}, Sources: []store.Source{}, Sections: []string{}}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, v)
}

// renderWriting is the progress page for a reservation (GET /{slug}/).
func (s *Server) renderWriting(w http.ResponseWriter, tut *store.Tutorial, tutDir string) {
	v := s.writingFor(tut, tutDir, true)
	var buf bytes.Buffer
	if err := s.writingTmpl.Execute(&buf, map[string]any{
		"W":            v,
		"CSS":          s.designCSS,
		"HighlightCSS": s.highlightCSS,
		"Native":       s.nativeShell() != nil,
		"Topic":        tut.Topic,
		"Tools":        tut.Tools,
	}); err != nil {
		log.Printf("lathe: writing template: %v", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = buf.WriteTo(w)
}

// isFormPost reports whether r is a plain HTML form submission (the
// scripts-off path), which wants a redirect back rather than JSON.
func isFormPost(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	return strings.HasPrefix(ct, "application/x-www-form-urlencoded") || strings.HasPrefix(ct, "multipart/form-data")
}

// handleNewReply answers the question a new-tutorial run is waiting on, by
// slug: POST /-/new/{slug}/reply with {"text"} (204) or a form field text
// (303 back to the progress page, so it works with scripts off). 404 when no
// new-tutorial run is writing it, 409 when it isn't waiting.
func (s *Server) handleNewReply(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	slug := r.PathValue("slug")
	agent := s.hostedAgent()
	if agent == nil {
		http.NotFound(w, r)
		return
	}
	run, ok := agent.activeJob(slug)
	if !ok || run.job != jobNew {
		http.NotFound(w, r)
		return
	}
	var text string
	form := isFormPost(r)
	if form {
		r.Body = http.MaxBytesReader(w, r.Body, maxNewBytes)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		text = r.PostForm.Get("text")
	} else {
		var payload struct {
			Text string `json:"text"`
		}
		if !readJSONBody(w, r, maxNewBytes, &payload) {
			return
		}
		text = payload.Text
	}
	text = strings.TrimSpace(text)
	if text == "" {
		http.Error(w, "text is required", http.StatusBadRequest)
		return
	}
	if !run.reply(text) {
		http.Error(w, "the agent is not waiting for a reply", http.StatusConflict)
		return
	}
	if form {
		http.Redirect(w, r, "/"+slug+"/", http.StatusSeeOther)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// question returns the question a run is waiting on (the last message block
// of the agent's turn), whether it is waiting, and when the reader last
// replied (zero before the first reply).
func (r *agentRun) question() (string, bool, time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.asked, r.waiting, r.repliedAt
}
