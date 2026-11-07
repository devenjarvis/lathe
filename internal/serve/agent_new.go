package serve

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/devenjarvis/lathe/internal/acp"
	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/queue"
	"github.com/devenjarvis/lathe/internal/store"
	"github.com/devenjarvis/lathe/internal/voice"
)

// New tutorial on the hosted agent: POST /-/new runs the /lathe skill as a
// job, like Verify and Extend, with the reader's answers to the skill's usual
// questions (level, voice, workspace, versions, repo) filled in up front.
//
// Reserve + scoped store. The server reserves the slug before the agent
// starts (store.ReserveNew: a placeholder with status "writing", so the
// library shows it under "Being written") and hands the session a Lathe MCP
// server scoped to that slug in new mode (`lathe mcp --slug <slug> --new`),
// whose lathe_store stores only into the reservation. Like every other
// hosted job, the session can write only its own tutorial. The run's after
// hook settles it: a run that ended without storing loses its reservation
// (store.CancelNew), so it never leaves an empty tutorial behind; a stored one
// is put on the requested shelf.
//
// Questions. The skill still asks when something is genuinely unclear (say,
// confirming the versions it detected). It ends its turn on the question,
// which streams to the panel as an agent message; while the tutorial is not
// stored the run then waits (an "input" event) for POST /-/acp/{id}/reply,
// whose text goes back as the next prompt in the same session.

const (
	jobNew = "new"

	// maxNewBytes caps the POST /-/new body; maxTopicLen and maxFieldLen cap
	// its fields (a topic is a sentence, the rest are words or paths).
	maxNewBytes = 8 << 10
	maxTopicLen = 500
	maxFieldLen = 300
	maxNewTools = 12

	// replyTimeout is how long a new-tutorial run waits for the reader to
	// answer the agent's question before it gives up.
	replyTimeout = 30 * time.Minute
)

// newRequest is the POST /-/new body. Topic is required; everything else is
// a pre-answered input the skill would otherwise ask for, or filing.
type newRequest struct {
	Topic string `json:"topic"`
	// Level is the reader's experience level ("beginner", "some
	// familiarity", "experienced in adjacent areas", or their own words).
	Level string `json:"level"`
	// Voice is a voice name (lathe voice list); empty means the default.
	Voice string `json:"voice"`
	// Workspace is the local folder the tutorial is for (absolute or ~);
	// the agent may read it, and Lathe records it as the tutorial's
	// workspace.
	Workspace string `json:"workspace"`
	// WorkspaceAuto says Workspace is the New tutorial sheet's own
	// suggestion, not something the reader typed or picked: the server
	// recomputes it from the topic (<code root>/<slug>), so a submit that
	// beat the sheet's live update still gets the right folder.
	WorkspaceAuto bool `json:"workspaceAuto"`
	// Tools are pinned versions; when given the skill doesn't re-confirm
	// them.
	Tools []store.Tool `json:"tools"`
	// Versions is the New tutorial sheet's free-text form of Tools ("zig
	// 0.13.0, llvm 18"), used when Tools is empty.
	Versions string `json:"versions"`
	// Repo / RepoBranch pin the git remote the tutorial is for.
	Repo       string `json:"repo"`
	RepoBranch string `json:"repoBranch"`
	// Shelf is a shelf id to file the tutorial on once it is stored
	// ("unsorted" keeps it off every shelf). Empty lets the agent pick one
	// (the /lathe skill's "Pick a shelf").
	Shelf string `json:"shelf"`
	// Slug, when set, is used exactly (409 if taken); else it is derived
	// from the topic and made unique.
	Slug string `json:"slug"`
}

// newInputs is a validated newRequest.
type newInputs struct {
	newRequest
	voice     voice.Voice
	voiceFile string // the custom voice's spec file, "" for a built-in
	workspace string // expanded, absolute; "" when none
}

// newSlugRe is an explicit slug's shape: kebab-case, as SlugFromTopic makes.
var newSlugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// validate cleans the request and checks what can be checked before
// anything is reserved. The error is the 400 body.
func (s *Server) validateNew(req newRequest) (*newInputs, error) {
	in := &newInputs{newRequest: req}
	in.Topic = oneLine(req.Topic)
	in.Level = oneLine(req.Level)
	in.Voice = strings.ToLower(oneLine(req.Voice))
	in.Repo = strings.TrimSpace(req.Repo)
	in.RepoBranch = strings.TrimSpace(req.RepoBranch)
	in.Shelf = strings.TrimSpace(req.Shelf)
	in.Slug = strings.TrimSpace(req.Slug)
	switch {
	case in.Topic == "":
		return nil, errors.New("topic is required")
	case len(in.Topic) > maxTopicLen:
		return nil, fmt.Errorf("topic is too long (at most %d bytes)", maxTopicLen)
	case len(in.Level) > maxFieldLen, len(in.Repo) > maxFieldLen, len(in.RepoBranch) > maxFieldLen, len(req.Workspace) > maxFieldLen:
		return nil, errors.New("a field is too long")
	case strings.ContainsAny(in.Repo+in.RepoBranch, " \t\r\n\""):
		return nil, errors.New("repo and repoBranch must not contain spaces or quotes")
	case len(req.Versions) > maxFieldLen:
		return nil, errors.New("a field is too long")
	case len(req.Tools) > maxNewTools:
		return nil, fmt.Errorf("at most %d pinned versions", maxNewTools)
	}
	if in.Slug != "" {
		if !newSlugRe.MatchString(in.Slug) || len(in.Slug) > maxFieldLen {
			return nil, fmt.Errorf("invalid slug %q (lowercase letters, digits and hyphens)", in.Slug)
		}
	}
	in.Tools = store.NormalizeTools(req.Tools)
	if len(in.Tools) == 0 {
		in.Tools = parseVersions(req.Versions)
		if len(in.Tools) > maxNewTools {
			return nil, fmt.Errorf("at most %d pinned versions", maxNewTools)
		}
	}
	for _, t := range in.Tools {
		if strings.ContainsAny(t.Name+t.Version, " \t\r\n\":") {
			return nil, fmt.Errorf("invalid pinned version %q: names and versions are single words", t.Name+":"+t.Version)
		}
	}

	if in.Voice == "" {
		in.Voice = config.DefaultVoice()
	}
	v, err := voice.Resolve(in.Voice)
	if err != nil {
		return nil, err
	}
	in.voice, in.Voice = v, v.Name
	if !v.Builtin {
		if dir, derr := config.VoicesDir(); derr == nil {
			in.voiceFile = filepath.Join(dir, v.Name+".md")
		}
	}

	if ws := strings.TrimSpace(req.Workspace); ws != "" {
		p, err := store.ExpandPath(ws)
		if err != nil {
			return nil, err
		}
		if !filepath.IsAbs(p) {
			return nil, fmt.Errorf("workspace must be an absolute path (or start with ~): %q", ws)
		}
		if pathWithin(p, s.tutorialsDir) {
			return nil, errors.New("pick a workspace outside Lathe's tutorial store")
		}
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return nil, fmt.Errorf("workspace %s is a file, not a folder", p)
		}
		in.workspace = p
	}

	if in.Shelf != "" {
		if in.Shelf == store.UnsortedShelfID {
			// The reader's choice: settleNew takes it off any shelf.
		} else if sh, err := store.ReadShelves(s.shelvesPath()); err != nil {
			return nil, err
		} else if _, ok := sh.Get(in.Shelf); !ok {
			return nil, fmt.Errorf("no shelf %q", in.Shelf)
		}
	}
	return in, nil
}

// pathWithin reports whether p is root or lies under it.
func pathWithin(p, root string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// quoteArg quotes a handoff argument that holds spaces or quotes.
func quoteArg(s string) string {
	if !strings.ContainsAny(s, " \t\"'\\") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// command is the paste-able invocation: `/lathe <topic>` followed by the
// pre-answered inputs as flags the /lathe skill reads (see its "Pre-answered
// inputs" section). It is what a /lathe-work worker runs and what the
// handoff offers.
func (in *newInputs) command() string {
	var b strings.Builder
	b.WriteString("/lathe ")
	b.WriteString(in.Topic)
	flag := func(name, v string) {
		if v != "" {
			fmt.Fprintf(&b, " --%s %s", name, quoteArg(v))
		}
	}
	flag("level", in.Level)
	flag("voice", in.Voice)
	if in.workspace != "" {
		flag("workspace", in.workspace)
	}
	for _, t := range in.Tools {
		tool := t.Name
		if t.Version != "" {
			tool += ":" + t.Version
		}
		flag("tool", tool)
	}
	flag("repo", in.Repo)
	if in.Repo != "" {
		flag("repo-branch", in.RepoBranch)
	}
	flag("shelf", in.Shelf)
	return b.String()
}

// handleNew starts a new tutorial: on the hosted agent (a reservation and a
// streamed run, {"mode":"agent","runId","slug"}), else as a job for a
// connected /lathe-work worker ({"mode":"queued","jobId"}), else as the
// `/lathe …` command to paste ({"mode":"handoff","command"[,"notice"]}).
// The binary never drives a model: each path runs the /lathe skill in the
// user's own agent.
func (s *Server) handleNew(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	// The New tutorial sheet posts JSON; with scripts off it is a plain
	// form, answered with a redirect or the /-/new page (new_sheet.go).
	form := isFormPost(r)
	var req newRequest
	if form {
		var ok bool
		if req, ok = newFormRequest(w, r); !ok {
			http.Error(w, "could not read the form", http.StatusBadRequest)
			return
		}
	} else if !readJSONBody(w, r, maxNewBytes, &req) {
		return
	}
	// reply answers a start: id is the run (agent) or the job (queued).
	reply := func(mode, slug, id, command, notice string) {
		if form {
			s.newFormReply(w, r, req, mode, slug, command, notice, "", http.StatusOK)
			return
		}
		switch mode {
		case "agent":
			writeJSON(w, map[string]string{"mode": "agent", "runId": id, "slug": slug})
		case "queued":
			writeQueued(w, id)
		default:
			if notice != "" {
				writeHandoffNotice(w, command, notice)
			} else {
				writeHandoff(w, command)
			}
		}
	}
	fail := func(msg string, code int) {
		if form {
			s.newFormReply(w, r, req, "", "", "", "", msg, code)
			return
		}
		http.Error(w, msg, code)
	}
	if req.WorkspaceAuto && strings.TrimSpace(req.Topic) != "" {
		req.Workspace = newWorkspaceFor(req.Topic)
	}
	in, err := s.validateNew(req)
	if err != nil {
		fail(err.Error(), http.StatusBadRequest)
		return
	}
	command := in.command()

	var notice string
	if agent := s.hostedAgent(); agent != nil {
		run, slug, err := agent.startNew(s.tutorialsDir, s.shelvesPath(), in, command)
		switch {
		case err == nil:
			reply("agent", slug, run.id, command, "")
			return
		case errors.Is(err, store.ErrSlugTaken), errors.Is(err, errJobBusy):
			fail("conflict: "+err.Error(), http.StatusConflict)
			return
		}
		notice = "The agent (" + agent.command + ") could not start: " + err.Error()
	}
	if s.queue.WorkerConnected() {
		reply("queued", "", s.queue.Enqueue(queue.Job{Type: queue.JobNew, Command: command}), command, "")
		return
	}
	reply("handoff", "", "", command, notice)
}

// startNew reserves the tutorial's slug and starts the /lathe run for it in a
// fresh session. Any failure undoes the reservation; the caller then falls
// back to the worker or the handoff (or answers 409 for a taken slug).
func (h *agentHost) startNew(tutorialsDir, shelvesPath string, in *newInputs, command string) (*agentRun, string, error) {
	if in.Slug != "" {
		if _, busy := h.activeJob(in.Slug); busy {
			return nil, "", errJobBusy
		}
	}
	// Start the agent first, so an agent that can't start never leaves a
	// reservation behind, not even briefly.
	c, err := h.ensure()
	if err != nil {
		return nil, "", err
	}

	tut, tutDir, err := store.ReserveNew(tutorialsDir, store.NewReservation{
		Topic: in.Topic, Slug: in.Slug, Voice: in.Voice,
		Repo: in.Repo, RepoBranch: in.RepoBranch, Tools: in.Tools,
	})
	if err != nil {
		return nil, "", err
	}
	slug := tut.Slug
	scratch, err := os.MkdirTemp("", "lathe-new-")
	if err != nil {
		_ = store.CancelNew(tutDir)
		return nil, "", fmt.Errorf("make a scratch dir: %w", err)
	}
	if real, rerr := filepath.EvalSymlinks(scratch); rerr == nil {
		scratch = real
	}
	fail := func(err error) (*agentRun, string, error) {
		_ = os.RemoveAll(scratch)
		_ = store.CancelNew(tutDir)
		return nil, "", err
	}

	workspace := in.workspace
	if workspace != "" {
		// Recorded now (local reader state that survives the store), so
		// Check my work knows it from the start; this also creates an empty
		// folder (and git-inits it) for a fresh project.
		if _, err := store.SetWorkspace(tutDir, workspace); err != nil {
			return fail(fmt.Errorf("set the workspace: %w", err))
		}
		if real, rerr := filepath.EvalSymlinks(workspace); rerr == nil {
			workspace = real
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), agentStartTimeout)
	sid, err := c.NewSession(ctx, scratch, h.mcpServersNew(slug)...)
	cancel()
	if err != nil {
		return fail(err)
	}
	c.SetPolicy(sid, acp.NewPolicy(scratch, workspace, in.voiceFile))

	shelves, _ := store.ListShelves(tutorialsDir)
	prompt := newPrompt(slug, scratch, workspace, in, command, shelves)
	run, err := h.launch(c, jobKey(slug), jobNew, jobTarget{tutDir: tutDir}, sid, prompt, command, jobRunTimeout, func() string {
		c.SetPolicy(sid, nil)
		note := settleNew(tutDir, shelvesPath, in.Shelf)
		_ = os.RemoveAll(scratch)
		return note
	}, func(r *agentRun) {
		r.replies = make(chan string, 1)
		r.awaiting = func() bool {
			t, err := store.ReadMetadata(tutDir)
			return err == nil && t.Status == store.StatusWriting
		}
	})
	if err != nil {
		c.SetPolicy(sid, nil)
		return fail(err)
	}
	return run, slug, nil
}

// mcpServersNew is the session's Lathe MCP server in new mode: scoped to the
// reservation, with lathe_store storing only into it.
func (h *agentHost) mcpServersNew(slug string) []acp.MCPServer {
	servers := h.mcpServers(slug)
	for i := range servers {
		if servers[i].Name == "lathe" {
			servers[i].Args = append(append([]string{}, servers[i].Args...), "--new")
		}
	}
	return servers
}

// settleNew runs once a new-tutorial run has ended. A reservation the agent
// never stored into is removed; a stored tutorial goes on the requested
// shelf. It returns a sentence for the reader, or "".
func settleNew(tutDir, shelvesPath, shelf string) string {
	slug := filepath.Base(tutDir)
	err := store.CancelNew(tutDir)
	switch {
	case err == nil:
		return "It stopped before storing the tutorial, so Lathe removed the reservation; nothing was saved."
	case !errors.Is(err, store.ErrNotInFlight):
		return "Lathe could not remove the unfinished tutorial (" + err.Error() + "); delete it with `lathe rm " + slug + "`."
	}
	if _, rerr := store.ReadMetadata(tutDir); rerr != nil || shelf == "" {
		return "" // deleted while it ran, or nothing to file
	}
	if _, err := store.UpdateShelves(shelvesPath, func(sh *store.Shelves) error { return sh.Assign(slug, shelf) }); err != nil {
		return "It is stored, but Lathe could not put it on the shelf: " + err.Error() + "."
	}
	return ""
}

// wantsReply reports whether an ended turn should wait for the reader: the
// run takes replies and the tutorial is not stored yet.
func (r *agentRun) wantsReply() bool {
	return r.replies != nil && r.awaiting != nil && r.awaiting()
}

// waitReply waits for the reader's answer to the agent's question. It
// returns false when the run was cancelled, the agent exited (agentDone), or
// nobody answered in time.
func (r *agentRun) waitReply(agentDone <-chan struct{}) (string, bool) {
	r.mu.Lock()
	r.waiting = true
	r.appendLocked(runEvent{Type: "input", Message: "The agent is waiting for your reply."})
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.waiting = false
		r.mu.Unlock()
	}()
	t := time.NewTimer(replyTimeout)
	defer t.Stop()
	select {
	case text := <-r.replies:
		r.markNewBlock()
		return text, true
	case <-r.ctx.Done():
		return "", false
	case <-agentDone:
		return "", false
	case <-t.C:
		return "", false
	}
}

// reply hands the reader's answer to a waiting run. It reports false when
// the run is not waiting for one.
func (r *agentRun) reply(text string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.replies == nil || !r.waiting || !r.finished.IsZero() {
		return false
	}
	select {
	case r.replies <- text:
	default:
		return false // an answer is already on its way
	}
	r.waiting = false
	r.turnStart = r.text.Len()
	r.appendLocked(runEvent{Type: "reply", Text: text})
	return true
}

// handleAgentReply answers the question a new-tutorial run is waiting on:
// POST /-/acp/{id}/reply {"text"}. 409 when the run is not waiting.
func (s *Server) handleAgentReply(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	agent := s.hostedAgent()
	if agent == nil {
		http.NotFound(w, r)
		return
	}
	run, ok := agent.run(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	var payload struct {
		Text string `json:"text"`
	}
	if !readJSONBody(w, r, maxNewBytes, &payload) {
		return
	}
	text := strings.TrimSpace(payload.Text)
	if text == "" {
		http.Error(w, "text is required", http.StatusBadRequest)
		return
	}
	if !run.reply(text) {
		http.Error(w, "the agent is not waiting for a reply", http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// newPrompt is the whole prompt for a new-tutorial run: framing for the
// hosted session, the pre-answered inputs, the voice, the /lathe skill text,
// then the invocation the handoff would paste.
func newPrompt(slug, scratch, workspace string, in *newInputs, command string, shelves []store.ShelfListing) string {
	var b strings.Builder
	out := filepath.Join(scratch, slug)
	b.WriteString("You are writing a new Lathe tutorial for the \"New tutorial\" button in the Lathe web reader, which hosts you. ")
	b.WriteString("Follow the /lathe skill below, start to finish. Nobody is at a terminal: the reader is watching your progress in the Lathe panel, and answered the skill's usual questions up front (the pre-answered inputs below), so don't ask them again. ")
	b.WriteString("Ask only when something is genuinely unclear — for example to confirm the versions you detected when none were pinned, or a topic that could mean two different things. Then end your turn with one short question; the reader's reply comes back as your next prompt. Otherwise work start to finish without stopping.\n\n")
	fmt.Fprintf(&b, "- Lathe reserved the slug %s for this tutorial; it already shows in the library as \"Being written\". Use it as the skill's <slug>.\n", slug)
	fmt.Fprintf(&b, "- Lathe made a fresh scratch dir for this run: %s (your working directory). Write the tutorial to %s/part-01.md instead of /tmp/lathe-%s/, try code in the scratch dir, and run every command with it as the working directory.\n", scratch, out, slug)
	if workspace != "" {
		fmt.Fprintf(&b, "- The tutorial is for the workspace %s: read it to detect the repo and toolchain (e.g. `git -C %s remote get-url origin`) instead of $PWD. Never change files there.\n", workspace, quoteArg(workspace))
		b.WriteString("- Lathe lets this session read the scratch dir and the workspace, and write files and run commands only in the scratch dir; requests outside that are refused.\n")
	} else {
		b.WriteString("- Lathe lets this session read, write and run commands only in the scratch dir; requests outside that are refused.\n")
	}
	fmt.Fprintf(&b, "- Store it with the lathe_store tool, path %s; it stores only into %s. Use `lathe store %s` if you don't have the tool. A run that ends without storing loses the reservation, and Lathe deletes the scratch dir after the run.\n", out, slug, quoteArg(out))
	b.WriteString("- Lathe records the workspace itself; don't run `lathe start`.\n")
	if in.Shelf != "" {
		b.WriteString("- The reader chose the tutorial's shelf, and Lathe files it there when the run ends: skip the skill's \"Pick a shelf\" step and don't call lathe_shelf_set or `lathe shelf`.\n")
	} else {
		b.WriteString("- Pick its shelf as the skill's \"Pick a shelf\" step says, after storing: an existing shelf from the list below when one fits, else a new one. Record it with the lathe_shelf_set tool (it defaults to this tutorial; pass create: true for a new shelf).\n")
	}
	b.WriteString("- The reader sees your plan live: keep a short plan (pin versions, research, write, check jigs, store) and update it as you go.\n")
	b.WriteString("- Finish with a short summary for the reader (markdown): what Part 1 builds and the tutorial's slug.\n\n")

	b.WriteString("Pre-answered inputs:\n")
	fmt.Fprintf(&b, "- Topic: %s\n", in.Topic)
	if in.Level != "" {
		fmt.Fprintf(&b, "- Experience level: %s\n", in.Level)
	} else {
		b.WriteString("- Experience level: not given — ask the reader (the skill's first question).\n")
	}
	fmt.Fprintf(&b, "- Voice: %s (its spec is below; `lathe voice show` isn't needed). Pass it as voice when you store.\n", in.Voice)
	if workspace != "" {
		fmt.Fprintf(&b, "- Workspace: %s\n", workspace)
	} else {
		b.WriteString("- Workspace: none.\n")
	}
	if len(in.Tools) > 0 {
		var pins []string
		for _, t := range in.Tools {
			pins = append(pins, strings.TrimSpace(t.Name+" "+t.Version))
		}
		fmt.Fprintf(&b, "- Versions: %s — pinned by the reader; write against them without re-confirming.\n", strings.Join(pins, ", "))
	} else {
		b.WriteString("- Versions: not pinned — detect them and confirm the target with the reader before you write.\n")
	}
	switch {
	case in.Repo != "":
		fmt.Fprintf(&b, "- Repo: %s", in.Repo)
		if in.RepoBranch != "" {
			fmt.Fprintf(&b, " (branch %s)", in.RepoBranch)
		}
		b.WriteString(" — pinned by the reader.\n")
	case workspace != "":
		b.WriteString("- Repo: not given — use the workspace's origin remote and branch if it has one (no need to confirm), else it is standalone.\n")
	default:
		b.WriteString("- Repo: none — a standalone tutorial.\n")
	}
	b.WriteString("\n")
	if in.Shelf == "" && len(shelves) > 0 {
		b.WriteString("The reader's shelves (pick from these first):\n\n```json\n")
		b.WriteString(jsonBlock(shelves))
		b.WriteString("\n```\n\n")
	}
	fmt.Fprintf(&b, "<voice name=%q>\n%s\n</voice>\n\n", in.Voice, strings.TrimSpace(in.voice.Wrapped()))
	writeSkill(&b, "lathe")
	b.WriteString(command)
	return b.String()
}
