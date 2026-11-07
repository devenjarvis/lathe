package serve

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/devenjarvis/lathe/internal/store"
)

// Actionable notifications and the Dock badge (#146).
//
// A notification may carry one action: a button label and the Lathe page the
// app shows when the reader clicks it ("Extend finished" → Open, "Verify
// failed" → View report, "<agent> needs you" → Answer). A shell that can
// post actions implements ActionNotifier; any other shell, or an action whose
// path isn't a reading page, gets the plain Notify(title, body), so callers
// always pass the action and never check what the platform supports.
//
// The badge counts what waits on the reader: the hosted agent's open
// permission prompts and questions. A shell with a badge implements Badger.

// NotifyAction is a notification's button: Label names it and Path is the
// same-origin reading page the app opens ("/{slug}/{part}#agent").
type NotifyAction struct {
	Label string
	Path  string
}

// Notice is one system notification.
type Notice struct {
	Title  string
	Body   string
	Action *NotifyAction // nil: a plain notification
}

// ActionNotifier is an optional part of a Native shell: notifications with
// an action button (and a click on the notification itself doing the same).
// It is a separate interface so a shell without it still satisfies Native.
type ActionNotifier interface {
	// NotifyWithAction posts n. Best effort, like Native.Notify: it never
	// blocks a job, and where the platform can't show the button the shell
	// posts the notification without it.
	NotifyWithAction(n Notice)
}

// Badger is an optional part of a Native shell: a count on the app's Dock
// (or taskbar) icon. 0 clears it.
type Badger interface {
	SetBadge(count int)
}

// noticeNeedsYou is the notifyEnabled key of "<agent> needs you".
const noticeNeedsYou = "needs_you"

// postNotice posts n through shell sh on its own goroutine, with its action
// when the shell can show one and the action opens a reading page.
func (s *Server) postNotice(sh Native, n Notice) {
	if sh == nil {
		return
	}
	if n.Action != nil && n.Action.Label != "" {
		if an, ok := sh.(ActionNotifier); ok {
			if _, ok := s.tutorialPage(n.Action.Path); ok || HomePage(n.Action.Path) {
				go an.NotifyWithAction(n)
				return
			}
		}
	}
	go sh.Notify(n.Title, n.Body)
}

// jobAction is the button on a finished job's notification: View report on
// a failed verify (the part that failed), View feedback on a check, Open on
// the rest (the new part, for an extend). nil when the tutorial is gone.
func (s *Server) jobAction(o jobOutcome) *NotifyAction {
	tut, err := store.ReadMetadata(o.tutDir)
	if err != nil {
		return nil
	}
	slug := tut.Slug
	if slug == "" {
		slug = filepath.Base(o.tutDir)
	}
	open := &NotifyAction{Label: "Open", Path: "/" + slug + "/"}
	switch o.job {
	case jobVerify:
		if o.stopped || tut.Status != store.StatusFailed {
			return open
		}
		path := "/" + slug + "/"
		if vr, err := store.ReadVerifyResult(o.tutDir); err == nil && vr != nil && isKnownPart(tut, vr.Part) {
			path = "/" + slug + "/" + vr.Part
		}
		// The verify result sits in the page's #verifyRegion.
		return &NotifyAction{Label: "View report", Path: path + "#verifyRegion"}
	case jobExtend:
		if !o.stopped && len(tut.Parts) > 0 {
			open.Path = "/" + slug + "/" + tut.Parts[len(tut.Parts)-1]
		}
		return open
	case jobCheck:
		if isKnownPart(tut, o.part) {
			return &NotifyAction{Label: "View feedback", Path: "/" + slug + "/" + o.part + "#checkWork"}
		}
		return open
	}
	// Anything else (a new tutorial's "Part 1 is ready") opens it.
	return open
}

// attentionItem is one thing the hosted agent waits on the reader for.
type attentionItem struct {
	key   string // run id + prompt id: stable while it waits
	title string // what it asks
	path  string // the page to answer it on
}

// attention lists the open permission prompts and questions of the host's
// unfinished runs.
func (h *agentHost) attention() []attentionItem {
	h.mu.Lock()
	runs := make([]*agentRun, 0, len(h.runs))
	for _, r := range h.runs {
		runs = append(runs, r)
	}
	pages := map[string]string{} // run id → "slug/part" of an ask
	for key, id := range h.active {
		if !strings.Contains(key, "#") {
			pages[id] = key
		}
	}
	h.mu.Unlock()

	var out []attentionItem
	for _, r := range runs {
		page := r.answerPage(pages[r.id])
		r.mu.Lock()
		if !r.finished.IsZero() {
			r.mu.Unlock()
			continue
		}
		for id := range r.perms {
			title := "It asks for permission to use a tool."
			for i := len(r.events) - 1; i >= 0; i-- {
				if ev := r.events[i]; ev.Type == "permission" && ev.ID == id {
					if ev.Title != "" {
						title = ev.Title
					}
					break
				}
			}
			out = append(out, attentionItem{key: r.id + "/" + id, title: title, path: page})
		}
		if r.waiting {
			out = append(out, attentionItem{key: r.id + "/input", title: "It's waiting for your answer.", path: page})
		}
		r.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out
}

// answerPage is where the reader answers run's prompts: its part with the
// agent panel open, or Home for a new tutorial (it has no page yet). ask is
// the "slug/part" an ask run is active under.
func (r *agentRun) answerPage(ask string) string {
	if r.job == jobNew {
		return "/"
	}
	if r.target.tutDir != "" {
		slug := filepath.Base(r.target.tutDir)
		return "/" + slug + "/" + r.target.part + "#agent"
	}
	if slug, part, ok := strings.Cut(ask, "/"); ok && slug != "" {
		return "/" + slug + "/" + part + "#agent"
	}
	return "/"
}

// attentionWatch turns successive attention snapshots into the new prompts
// (to notify about once each) and the count (for the badge).
type attentionWatch struct {
	seen  map[string]bool
	count int
	init  bool
}

func (a *attentionWatch) step(items []attentionItem) (fresh []attentionItem, count int, changed bool) {
	now := make(map[string]bool, len(items))
	for _, it := range items {
		now[it.key] = true
		if !a.seen[it.key] {
			fresh = append(fresh, it)
		}
	}
	a.seen = now
	changed = !a.init || len(items) != a.count
	a.init = true
	a.count = len(items)
	return fresh, a.count, changed
}

// attentionEvery is how often the app looks for new prompts. A prompt waits
// minutes for an answer, so a second is plenty.
var attentionEvery = time.Second

// attentionLoop keeps the shell's badge and "needs you" notifications in
// step with the hosted agent until stop closes. SetNative starts it.
func (s *Server) attentionLoop(stop <-chan struct{}) {
	t := time.NewTicker(attentionEvery)
	defer t.Stop()
	var watch attentionWatch
	for {
		s.attentionTick(&watch)
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}

// attentionTick is one look: notify about prompts that are new since the
// last one, and update the badge when the count changed.
func (s *Server) attentionTick(watch *attentionWatch) {
	sh := s.nativeShell()
	if sh == nil {
		return
	}
	var items []attentionItem
	agentName := "Your agent"
	if h := s.hostedAgent(); h != nil {
		items = h.attention()
		if h.name != "" {
			agentName = h.name
		}
	}
	fresh, count, changed := watch.step(items)
	if changed {
		if b, ok := sh.(Badger); ok {
			b.SetBadge(count)
		}
	}
	if len(fresh) == 0 || !notifyEnabled(noticeNeedsYou) {
		return
	}
	for _, it := range fresh {
		s.postNotice(sh, Notice{
			Title:  agentName + " needs you",
			Body:   it.title,
			Action: &NotifyAction{Label: "Answer", Path: it.path},
		})
	}
}

// attentionRunner runs the attention loop while a shell is installed.
type attentionRunner struct {
	mu   sync.Mutex
	stop chan struct{}
}

// start runs the loop once (SetNative may be called again).
func (a *attentionRunner) start(s *Server) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stop != nil {
		return
	}
	a.stop = make(chan struct{})
	go s.attentionLoop(a.stop)
}

func (a *attentionRunner) close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stop != nil {
		close(a.stop)
		a.stop = nil
	}
}
