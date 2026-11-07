package serve

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/devenjarvis/lathe/internal/queue"
	"github.com/devenjarvis/lathe/internal/store"
)

// The page side of the desktop shell (see native.go). The templates render
// the native affordances only when a shell is installed (data-native on
// <html>, a "Choose folder…" button in the agent panel, a pop-out that asks
// for an app window), and these endpoints are how the page reaches the shell.
// All of them 404 without a shell, so a plain browser sees exactly today's
// server.
//
//	POST /-/native/pick-folder  {"slug"}  → {"path", "display"} ("" when cancelled)
//	POST /-/native/window       {"path"}  → 204
//	POST /-/native/open-url     {"url"}   → 204 (links to other sites; nativeLinks in components.html)
//
// Notifications need no endpoint: the server posts one when a Verify, Extend
// or Check my work run ends (notifyJob).

// maxNativeBody caps the JSON bodies of the native endpoints: a slug or a
// short path.
const maxNativeBody = 4 << 10

// handleNativePickFolder shows the shell's folder picker for a tutorial's
// workspace, starting in the current workspace (else the home dir), and
// returns the chosen path (and its ~ form). The page saves it through POST /-/workspace/{slug}
// like a typed path, so the validation (and git init) stays in one place.
//
// PickFolder blocks until the dialog closes; it is called on this request's
// goroutine, so the shell marshals it to its UI thread itself.
func (s *Server) handleNativePickFolder(w http.ResponseWriter, r *http.Request) {
	n := s.nativeShell()
	if n == nil {
		http.NotFound(w, r)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var payload struct {
		Slug string `json:"slug"`
	}
	if !readJSONBody(w, r, maxNativeBody, &payload) {
		return
	}
	if payload.Slug == "" {
		http.NotFound(w, r)
		return
	}
	tutDir, ok := s.safeTutorialPath(payload.Slug)
	if !ok {
		http.NotFound(w, r)
		return
	}
	tut, err := store.ReadMetadata(tutDir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	start, _ := workspaceDisplay(tutDir)
	if start == "" {
		start, _ = os.UserHomeDir()
	}
	path, err := n.PickFolder("Choose a workspace for "+tutorialName(tut), start)
	if err != nil {
		http.Error(w, "could not open the folder picker: "+err.Error(), http.StatusInternalServerError)
		return
	}
	display := ""
	if path != "" {
		display = displayPath(path)
	}
	writeJSON(w, map[string]string{"path": path, "display": display})
}

// handleNativeWindow opens the agent panel's pop-out in an app window, where
// window.open has nothing to open. Only the pop-out of a real part is
// accepted ("/{slug}/{part}?agent=popout"), so a page can't make the shell
// open anything else.
func (s *Server) handleNativeWindow(w http.ResponseWriter, r *http.Request) {
	n := s.nativeShell()
	if n == nil {
		http.NotFound(w, r)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var payload struct {
		Path string `json:"path"`
	}
	if !readJSONBody(w, r, maxNativeBody, &payload) {
		return
	}
	tut, ok := s.popoutTutorial(payload.Path)
	if !ok {
		http.Error(w, "not an agent panel pop-out path", http.StatusBadRequest)
		return
	}
	if err := n.OpenWindow(payload.Path, "Agent · "+tutorialName(tut)); err != nil {
		http.Error(w, "could not open a window: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleNativeOpenURL hands a link to another site to the system browser. The
// app window shows Lathe's own pages only; following an external link in it
// would leave the reader with no way back. Only absolute http, https and
// mailto URLs are accepted, so a page can't make the shell launch other
// handlers (file:, custom schemes).
func (s *Server) handleNativeOpenURL(w http.ResponseWriter, r *http.Request) {
	n := s.nativeShell()
	if n == nil {
		http.NotFound(w, r)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var payload struct {
		URL string `json:"url"`
	}
	if !readJSONBody(w, r, maxNativeBody, &payload) {
		return
	}
	target, ok := ExternalURL(payload.URL)
	if !ok {
		http.Error(w, "not an external http, https or mailto URL", http.StatusBadRequest)
		return
	}
	if err := n.OpenURL(target); err != nil {
		http.Error(w, "could not open the link: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ExternalURL reports whether raw is an absolute http(s) URL with a host, or
// a mailto URL, and returns it re-encoded. The desktop shell checks it again
// before launching anything.
func ExternalURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\x00\r\n") {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		if u.Host == "" || u.Opaque != "" {
			return "", false
		}
	case "mailto":
		if u.Opaque == "" {
			return "", false
		}
	default:
		return "", false
	}
	return u.String(), true
}

// popoutTutorial reports whether raw is exactly the pop-out path of one of
// the tutorial's declared parts, "/{slug}/{part}?agent=popout", and returns
// that tutorial. Anything else (another scheme or host, "//", a fragment,
// extra query, an unknown part, path tricks) is refused.
func (s *Server) popoutTutorial(raw string) (*store.Tutorial, bool) {
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.ContainsAny(raw, "\\#") {
		return nil, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.Opaque != "" {
		return nil, false
	}
	if u.RawQuery != "agent=popout" || u.RawPath != "" {
		return nil, false
	}
	segs := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(segs) != 2 || segs[0] == "" || segs[1] == "" || segs[0] == "." || segs[0] == ".." {
		return nil, false
	}
	tutDir, ok := s.safeTutorialPath(segs[0])
	if !ok {
		return nil, false
	}
	tut, err := store.ReadMetadata(tutDir)
	if err != nil || !isKnownPart(tut, segs[1]) {
		return nil, false
	}
	return tut, true
}

// tutorialName is the tutorial's title, or its slug when it has none.
func tutorialName(tut *store.Tutorial) string {
	if t := strings.TrimSpace(tut.Title); t != "" {
		return t
	}
	return tut.Slug
}

// jobOutcome is how a Verify, Extend or Check my work run ended, for its
// notification.
type jobOutcome struct {
	job    string // jobVerify, jobExtend or jobCheck
	tutDir string
	part   string // the checked part (check only)
	// stopped is set when the run ended without its result (crashed, timed
	// out, refused, or finished without recording one); note says why.
	stopped bool
	note    string
	// since, when set, is when the job was queued: a check result older
	// than that is not this job's (the worker path, where no after hook
	// tells).
	since time.Time
}

// notifyJob posts the system notification for a finished job, when a desktop
// shell is installed. It is the one notify point for both paths: a hosted
// run's end (agentHost.finished, wired in setAgentHost) and a worker's
// POST /-/work/{id}/done. Runs the reader cancels never get here.
//
// Native.Notify is best effort by contract, but it is called on its own
// goroutine anyway, so a slow shell can never hold up a job's last event.
func (s *Server) notifyJob(o jobOutcome) {
	n := s.nativeShell()
	if n == nil {
		return
	}
	title, body, ok := jobNotice(o)
	if !ok {
		return
	}
	go n.Notify(title, body)
}

// jobNotice words a finished job's notification from what it left on disk.
// It returns ok=false for jobs that don't notify (ask) or a tutorial that is
// gone.
func jobNotice(o jobOutcome) (title, body string, ok bool) {
	tut, err := store.ReadMetadata(o.tutDir)
	if err != nil {
		return "", "", false
	}
	name := tutorialName(tut)
	stoppedBody := o.note
	switch o.job {
	case jobVerify:
		if o.stopped {
			if stoppedBody == "" {
				stoppedBody = "It stopped before recording a result."
			}
			return "Verify stopped: " + name, stoppedBody, true
		}
		var vr *store.VerifyResult
		if v, err := store.ReadVerifyResult(o.tutDir); err == nil {
			vr = v
		}
		switch tut.Status {
		case store.StatusVerified:
			return "Verified: " + name, "Every part ran end to end.", true
		case store.StatusFailed:
			return "Verify failed: " + name, verifyFailure(o.tutDir, vr), true
		case store.StatusSkipped:
			body := "A tool this tutorial needs isn't installed."
			if vr != nil && vr.Error != "" {
				body = vr.Error
			}
			return "Verify skipped: " + name, body, true
		default:
			return "Verify finished: " + name, "", true
		}
	case jobExtend:
		if o.stopped {
			if stoppedBody == "" {
				stoppedBody = "It stopped before committing the part."
			}
			return "Extend stopped: " + name, stoppedBody, true
		}
		body := ""
		if tut.Status != store.StatusExtending && len(tut.Parts) > 0 {
			last := tut.Parts[len(tut.Parts)-1]
			body = fmt.Sprintf("Part %d is ready: %s", len(tut.Parts), partTitle(o.tutDir, last))
		}
		return "Extend finished: " + name, body, true
	case jobCheck:
		pt := partTitle(o.tutDir, o.part)
		if o.stopped {
			if stoppedBody == "" {
				stoppedBody = "It finished without recording a result, so nothing was saved."
			}
			return "Check stopped: " + pt, stoppedBody, true
		}
		res := store.LatestCheck(o.tutDir, o.part)
		if res == nil || res.Time().Before(o.since) {
			return "Check stopped: " + pt, "It finished without recording a result, so nothing was saved.", true
		}
		// The same headings as the result card (check.html).
		var head string
		switch res.Status {
		case store.CheckPass:
			head = "Your checkpoint passes."
		case store.CheckFail:
			head = "Not quite yet."
		default:
			head = "Couldn't run the checkpoint here."
		}
		if res.Summary != "" {
			head += " " + res.Summary
		}
		return "Check finished: " + pt, head, true
	}
	return "", "", false
}

// verifyFailure is a one-line "where and why" for a failed verify, in the
// failure callout's terms.
func verifyFailure(tutDir string, vr *store.VerifyResult) string {
	if vr == nil {
		return "See the tutorial for what failed."
	}
	var parts []string
	if vr.Part != "" {
		where := partTitle(tutDir, vr.Part)
		if vr.FailedStep > 0 {
			where += fmt.Sprintf(", step %d", vr.FailedStep)
		}
		parts = append(parts, where)
	}
	if vr.Error != "" {
		parts = append(parts, vr.Error)
	}
	if len(parts) == 0 {
		return "See the tutorial for what failed."
	}
	return strings.Join(parts, ": ")
}

// runFinished is the hosted agent's notify point: drive calls it once a run's
// final event is out (after the job's after hook settled the tutorial). Asks
// and runs the reader cancelled don't notify.
func (s *Server) runFinished(run *agentRun, final runEvent, cancelled bool) {
	if cancelled || run.target.tutDir == "" {
		return
	}
	switch run.job {
	case jobVerify, jobExtend, jobCheck:
	default:
		return
	}
	s.notifyJob(jobOutcome{
		job:     run.job,
		tutDir:  run.target.tutDir,
		part:    run.target.part,
		stopped: final.Type != "done" || final.Message != "",
		note:    final.Message,
	})
}

// workJobFinished is the worker path's notify point: a /lathe-work session
// reported a verify, extend or check job done. The worker recorded the result
// through the CLI before reporting, so the outcome is read from disk.
func (s *Server) workJobFinished(job queue.Job) {
	var kind string
	switch job.Type {
	case queue.JobVerify:
		kind = jobVerify
	case queue.JobExtend:
		kind = jobExtend
	case queue.JobCheck:
		kind = jobCheck
	default:
		return
	}
	tutDir, ok := s.safeTutorialPath(job.Slug)
	if !ok {
		return
	}
	s.notifyJob(jobOutcome{job: kind, tutDir: tutDir, part: job.Part, since: job.CreatedAt})
}
