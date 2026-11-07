package serve

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/devenjarvis/lathe/internal/acp"
	"github.com/devenjarvis/lathe/internal/queue"
	"github.com/devenjarvis/lathe/internal/store"
)

// "Check my work": the reader asks their agent to look at the code they typed
// for a part. Lathe copies the reader's workspace to a scratch dir and the
// agent runs the part's checkpoint there, under acp.CheckPolicy (read the
// tutorial and the workspace, write and run only in the copy), so the reader's
// files are never changed. The agent compares by meaning, guided by the
// verified code snapshot's diff for the part when there is one, and records a
// hint ladder through lathe_check_result (or `lathe check-result`), which
// appends to the tutorial's checks.jsonl. The page renders the newest result
// for the part under its "## Checkpoint" section.
//
// Like Verify, the order is hosted agent → /lathe-work worker → the paste-able
// /lathe-check command.

const (
	jobCheck = "check"
	// checkRunTimeout caps one check: a build and a checkpoint run.
	checkRunTimeout = 20 * time.Minute
)

var errCheckBusy = errors.New("your agent is already checking this tutorial")

// checkKey is the active-run key for a tutorial's check: one at a time per
// tutorial, separate from verify/extend so a check can run beside them.
func checkKey(slug string) string { return slug + "#check" }

func checkCommand(slug, part string) string { return "/lathe-check " + slug + " " + part }

// errNoWorkspace is the reader-facing reply when no workspace is set.
const errNoWorkspace = "Lathe doesn't know where your code for this tutorial is yet. Set your workspace in the agent panel (or run lathe start <slug> <folder> in a terminal), then check again."

// handleCheck starts a check of the reader's code against a part.
func (s *Server) handleCheck(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	slug, part := r.PathValue("slug"), r.PathValue("part")
	tutDir, ok := s.safeTutorialPath(slug)
	if !ok {
		http.NotFound(w, r)
		return
	}
	tut, err := store.ReadMetadata(tutDir)
	if err != nil || !isKnownPart(tut, part) {
		http.NotFound(w, r)
		return
	}
	ws, err := store.ReadWorkspace(tutDir)
	if err != nil || ws == nil {
		http.Error(w, errNoWorkspace, http.StatusBadRequest)
		return
	}
	if info, err := os.Stat(ws.Path); err != nil || !info.IsDir() {
		http.Error(w, "Your workspace "+displayPath(ws.Path)+" is missing. Pick it again in the agent panel.", http.StatusBadRequest)
		return
	}

	command := checkCommand(slug, part)
	var notice string
	if agent := s.hostedAgent(); agent != nil {
		run, err := agent.startCheck(tut, tutDir, ws.Path, part, command)
		switch {
		case err == nil:
			writeAgentRun(w, run.id)
			return
		case errors.Is(err, errCheckBusy):
			http.Error(w, "conflict: "+err.Error(), http.StatusConflict)
			return
		default:
			notice = "The agent (" + agent.command + ") could not start: " + err.Error()
		}
	}
	if s.queue.WorkerConnected() {
		id := s.queue.Enqueue(queue.Job{Type: queue.JobCheck, Slug: slug, Part: part, Path: ws.Path})
		writeQueued(w, id)
		return
	}
	if notice != "" {
		writeHandoffNotice(w, command, notice)
		return
	}
	writeHandoff(w, command)
}

// handleCheckGet returns the newest check of a part, rendered: the page swaps
// it in when a check finishes. checkedAt lets a poller tell a new result from
// the one it already shows.
func (s *Server) handleCheckGet(w http.ResponseWriter, r *http.Request) {
	slug, part := r.PathValue("slug"), r.PathValue("part")
	tutDir, ok := s.safeTutorialPath(slug)
	if !ok {
		http.NotFound(w, r)
		return
	}
	tut, err := store.ReadMetadata(tutDir)
	if err != nil || !isKnownPart(tut, part) {
		http.NotFound(w, r)
		return
	}
	view := s.checkViewFor(tut, tutDir, part)
	resp := map[string]any{"checkedAt": "", "html": s.renderPartial("checkResult", view)}
	if view.Result != nil {
		resp["checkedAt"] = view.Result.CheckedAt
		resp["status"] = view.Result.Status
		resp["findings"] = len(view.Findings)
	}
	writeJSON(w, resp)
}

// startCheck copies the workspace to a fresh scratch dir, opens a session
// there under CheckPolicy and starts the check.
func (h *agentHost) startCheck(tut *store.Tutorial, tutDir, workspace, part, command string) (*agentRun, error) {
	slug := filepath.Base(tutDir)
	key := checkKey(slug)
	h.mu.Lock()
	_, busy := h.active[key]
	h.mu.Unlock()
	if busy {
		return nil, errCheckBusy
	}

	c, err := h.ensure()
	if err != nil {
		return nil, err
	}
	abs, err := realAbs(tutDir)
	if err != nil {
		return nil, err
	}
	ws, err := realAbs(workspace)
	if err != nil {
		return nil, err
	}
	scratch, err := os.MkdirTemp("", "lathe-check-")
	if err != nil {
		return nil, fmt.Errorf("make a scratch dir: %w", err)
	}
	if real, rerr := filepath.EvalSymlinks(scratch); rerr == nil {
		scratch = real
	}
	fail := func(err error) (*agentRun, error) {
		_ = os.RemoveAll(scratch)
		return nil, err
	}
	if err := store.CopyWorkspace(ws, scratch); err != nil {
		return fail(fmt.Errorf("copy your workspace: %w", err))
	}

	ctx, cancel := context.WithTimeout(context.Background(), agentStartTimeout)
	sid, err := c.NewSession(ctx, scratch, h.mcpServers(slug)...)
	cancel()
	if err != nil {
		return fail(err)
	}
	c.SetPolicy(sid, acp.CheckPolicy(abs, ws, scratch))

	started := time.Now()
	prompt := checkPrompt(tut, abs, ws, scratch, part, command)
	run, err := h.launch(c, key, jobCheck, jobTarget{tutDir: tutDir, part: part}, sid, prompt, command, checkRunTimeout, func() string {
		c.SetPolicy(sid, nil)
		_ = os.RemoveAll(scratch)
		if res := store.LatestCheck(tutDir, part); res == nil || res.Time().Before(started) {
			return "It finished without recording a result, so nothing was saved."
		}
		return ""
	})
	if err != nil {
		c.SetPolicy(sid, nil)
		return fail(err)
	}
	return run, nil
}

func realAbs(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	return abs, nil
}

// checkPrompt frames a hosted check: where everything is, what Lathe allows,
// the part's checkpoint and the reference diff, then the /lathe-check skill
// and the invocation.
func checkPrompt(tut *store.Tutorial, tutDir, workspace, scratch, part, command string) string {
	var b strings.Builder
	b.WriteString("You are checking a learner's own code for the \"Check my work\" button in the Lathe web reader, which hosts you. ")
	b.WriteString("Follow the /lathe-check skill below, start to finish, without stopping to ask questions: nobody is at a terminal, only the learner watching your progress.\n\n")
	fmt.Fprintf(&b, "- The tutorial is stored in %s; the part being checked is %s (read it there). Never edit it.\n", tutDir, part)
	fmt.Fprintf(&b, "- The learner's workspace is %s. Never change it: Lathe copied it to %s (your working directory). Build and run the checkpoint only in that copy.\n", workspace, scratch)
	b.WriteString("- Lathe lets this session read the tutorial, the workspace and the copy, and write files and run commands only in the copy; requests outside that are refused.\n")
	b.WriteString("- Record the result with the lathe_check_result tool (it defaults to this tutorial), or `lathe check-result <slug> --file -` if you don't have the tool. The page shows only the recorded result, and Lathe deletes the copy after the run, so record it before you finish.\n")
	b.WriteString("- The learner sees your plan live: keep a short plan (read the part, run the checkpoint, compare, record).\n")
	b.WriteString("- Finish with one or two sentences for the learner (markdown) that give nothing away: the hints are in the recorded result.\n")

	parts := tut.Parts
	if len(parts) == 0 {
		parts = []string{"index.md"}
	}
	tags := store.CodeTags(filepath.Join(tutDir, store.CodeDirName))
	if diff, ok := store.PartDiff(tutDir, parts, part); ok {
		fmt.Fprintf(&b, "- The verified code snapshot (%s, tags: %s) has this part's reference change, below. Compare by meaning, guided by it, and record confidence \"reference\". If the learner's code looks like another part, compare against that part's tag (git -C %s diff <tag>) and set on_part.\n",
			filepath.Join(tutDir, store.CodeDirName), strings.Join(tags, ", "), filepath.Join(tutDir, store.CodeDirName))
		writeTagged(&b, "reference-diff", diff)
	} else {
		b.WriteString("- No verified code snapshot covers this part, so check against the part's text only, record confidence \"text\", and say in the summary that running Verify first would make the check sharper.\n")
		if len(tags) > 0 {
			fmt.Fprintf(&b, "  (The snapshot does have tags %s, which can still tell you which part the learner is on.)\n", strings.Join(tags, ", "))
		}
	}
	b.WriteString("\n")
	if src, err := os.ReadFile(filepath.Join(tutDir, part)); err == nil {
		if cp := store.CheckpointSection(src); cp != "" {
			writeTagged(&b, "checkpoint", cp)
		}
	}
	writeSkill(&b, "lathe-check")
	b.WriteString(command)
	return b.String()
}

func writeTagged(b *strings.Builder, tag, body string) {
	fmt.Fprintf(b, "<%s>\n%s\n</%s>\n\n", tag, strings.TrimRight(body, "\n"), tag)
}

// runningCheck reports the hosted check in flight for slug, if any.
func (s *Server) runningCheck(slug string) *jobRunInfo {
	agent := s.hostedAgent()
	if agent == nil {
		return nil
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	id, ok := agent.active[checkKey(slug)]
	if !ok {
		return nil
	}
	info := &jobRunInfo{ID: id, Job: jobCheck}
	if run, ok := agent.runs[id]; ok {
		info.Started = run.started.UnixMilli()
	}
	return info
}

// checkView is what the checkSection / checkResult partials render.
type checkView struct {
	Slug, Part string
	Result     *store.CheckResult
	When       string
	// OnPartNumber/OnPartTitle name the part the learner's code matches when
	// the checker found them ahead or behind.
	OnPartNumber int
	OnPartTitle  string
	Findings     []findingView
	Divergences  []divergenceView
	HasWorkspace bool
	Workspace    string
	// RunID is the hosted check in flight, for a reload to reattach, and
	// RunStarted when it began (Unix milliseconds).
	RunID      string
	RunStarted int64
}

type findingView struct {
	File                            string
	Nudge, Pointer, Where, Reveal   template.HTML
	HasPointer, HasWhere, HasReveal bool
}

type divergenceView struct {
	File      string
	Note      template.HTML
	LaterPart string
	LaterNum  int
}

func (s *Server) checkViewFor(tut *store.Tutorial, tutDir, part string) checkView {
	v := checkView{Slug: tut.Slug, Part: part}
	if v.Slug == "" {
		v.Slug = filepath.Base(tutDir)
	}
	full, short := workspaceDisplay(tutDir)
	v.HasWorkspace, v.Workspace = full != "", short
	if run := s.runningCheck(v.Slug); run != nil {
		v.RunID, v.RunStarted = run.ID, run.Started
	}
	res := store.LatestCheck(tutDir, part)
	if res == nil {
		return v
	}
	v.Result = res
	if t := res.Time(); !t.IsZero() {
		v.When = t.Local().Format("Jan 2, 3:04 PM")
	}
	if res.OnPart != "" {
		if i := partIndex(tut, res.OnPart); i >= 0 {
			v.OnPartNumber = i + 1
			v.OnPartTitle = partTitle(tutDir, res.OnPart)
		}
	}
	for _, f := range res.Findings {
		fv := findingView{File: f.File, Nudge: jigBlock(f.Nudge)}
		if f.Pointer != "" {
			fv.Pointer, fv.HasPointer = jigBlock(f.Pointer), true
		}
		if f.Where != "" {
			fv.Where, fv.HasWhere = jigBlock(f.Where), true
		}
		if f.Reveal != "" {
			fv.Reveal, fv.HasReveal = jigBlock(f.Reveal), true
		}
		v.Findings = append(v.Findings, fv)
	}
	for _, d := range res.Divergences {
		dv := divergenceView{File: d.File, Note: jigBlock(d.Note), LaterPart: d.LaterPart}
		if d.LaterPart != "" {
			dv.LaterNum = partIndex(tut, d.LaterPart) + 1
		}
		v.Divergences = append(v.Divergences, dv)
	}
	return v
}

// withCheckSection puts the "Check my work" block at the end of the part's
// Checkpoint section (before the next h2), or returns content unchanged when
// the part has no checkpoint. It works on the rendered HTML: the h2s it looks
// for are the renderer's own top-level headings (auto ids), and "<h2" can't
// appear in rendered text or code, which goldmark escapes.
func withCheckSection(content []byte, section string) []byte {
	if section == "" {
		return content
	}
	start := bytes.Index(content, []byte(`<h2 id="checkpoint`))
	if start < 0 {
		return content
	}
	end := len(content)
	if next := bytes.Index(content[start+1:], []byte("<h2")); next >= 0 {
		end = start + 1 + next
	}
	out := make([]byte, 0, len(content)+len(section))
	out = append(out, content[:end]...)
	out = append(out, section...)
	out = append(out, content[end:]...)
	return out
}
