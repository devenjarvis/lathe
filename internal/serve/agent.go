package serve

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/devenjarvis/lathe/internal/acp"
	"github.com/devenjarvis/lathe/internal/agents"
	"github.com/devenjarvis/lathe/internal/frontmatter"
	"github.com/devenjarvis/lathe/internal/skills"
)

// The agent host lets `lathe serve --agent <cmd>` run the user's own coding
// agent as a child process and drive it over the Agent Client Protocol. The
// binary still never calls a model API: the agent does all model work, under
// the user's own login, and Lathe only relays the conversation and the
// agent's permission prompts to the browser.
//
// Ask, Verify and Extend all run here when an agent is configured (Verify and
// Extend are in agent_jobs.go); the /lathe-work queue and the copy-paste
// handoff stay as the fallback for agents without ACP support.
//
// Each session gets a file/terminal policy (acp.AskPolicy: read the tutorial
// dir, no writes, no commands; VerifyPolicy and ExtendPolicy for the jobs)
// that Lathe enforces on the agent's fs/* and terminal/* requests. Agents that
// use their own tools instead are reported as "soft" isolation.

const (
	// agentStartTimeout bounds initialize; adapters launched through npx can
	// take a while on first run.
	agentStartTimeout = 90 * time.Second
	// permissionTimeout answers a permission prompt "cancelled" if nobody
	// responds (the reader closed the tab).
	permissionTimeout = 10 * time.Minute
	// askRunTimeout caps one answer.
	askRunTimeout = 15 * time.Minute
	// runRetention is how long a finished run's events stay readable.
	runRetention = 30 * time.Minute
	// liveRenderEvery is how often a streaming answer is re-rendered to HTML
	// for the drawer.
	liveRenderEvery = 200 * time.Millisecond
)

// agentStarter launches and initializes an agent. Tests swap in a fake.
type agentStarter func(ctx context.Context) (*acp.Client, error)

// agentHost owns the agent process, its sessions, and the in-flight asks.
type agentHost struct {
	command string
	// id is the agent's registry id (internal/agents) when the command is a
	// known one, and name its display name ("Claude Code"); both are shown in
	// the agent panel.
	id    string
	name  string
	start agentStarter
	// mcp lists the MCP servers to hand the agent in each session/new. nil
	// sends none.
	mcp func(slug string) []acp.MCPServer

	startMu sync.Mutex // serializes (re)starts

	mu       sync.Mutex
	client   *acp.Client
	starting bool
	lastErr  string
	sessions map[string]string // "slug/part" → sessionId on the current client
	primed   map[string]bool   // sessionId → already received the skill text
	runs     map[string]*agentRun
	active   map[string]string // "slug/part" → run id of the running ask
	counter  int64
	// finished, when set, is told about every run once its final event is
	// out (the server's notify point; see Server.setAgentHost).
	finished func(run *agentRun, final runEvent, cancelled bool)
}

// jobTarget is what a verify, extend or check run works on.
type jobTarget struct {
	tutDir string
	part   string // the checked part (check only)
	parts  int    // how many parts the tutorial had when the job started (extend: did it add one?)
}

func newAgentHost(command string, start agentStarter) *agentHost {
	return &agentHost{
		command:  command,
		name:     agents.DisplayName(command),
		start:    start,
		sessions: map[string]string{},
		primed:   map[string]bool{},
		runs:     map[string]*agentRun{},
		active:   map[string]string{},
	}
}

// processStarter runs command through the shell from dir, with the agent's
// stderr passed through to the server's.
func processStarter(command, dir string, stderr io.Writer) agentStarter {
	return func(ctx context.Context) (*acp.Client, error) {
		return acp.Start(ctx, command, dir, stderr)
	}
}

// SetAgent makes the server host an ACP agent for Ask. The agent is started
// in the background now and restarted on demand if it exits. An empty command
// leaves the server as before (worker queue, then copy-paste handoff).
func (s *Server) SetAgent(command string) {
	command = strings.TrimSpace(command)
	if command == "" {
		return
	}
	h := s.makeHost(command)
	if a, ok := agents.ByCommand(command); ok {
		h.id = a.ID
	}
	s.setAgentHost(h)
	go func() { _, _ = h.ensure() }()
}

// newProcessHost builds the host for a real agent process.
func (s *Server) newProcessHost(command string) *agentHost {
	h := newAgentHost(command, processStarter(command, s.tutorialsDir, os.Stderr))
	if exe, err := os.Executable(); err == nil {
		h.mcp = latheMCP(exe)
	} else {
		fmt.Fprintf(os.Stderr, "warning: cannot locate the lathe binary, so the agent gets no Lathe MCP tools: %v\n", err)
	}
	return h
}

// makeHost builds a host for command; tests swap s.hostFactory for a fake.
func (s *Server) makeHost(command string) *agentHost {
	if s.hostFactory != nil {
		return s.hostFactory(command)
	}
	return s.newProcessHost(command)
}

func (s *Server) setAgentHost(h *agentHost) {
	if h != nil {
		h.mu.Lock()
		h.finished = s.runFinished
		h.mu.Unlock()
	}
	s.agentMu.Lock()
	s.agent = h
	s.agentMu.Unlock()
}

// hostedAgent returns the current agent host (nil when none is configured).
// The picker can swap it while the server runs, so handlers read it once.
func (s *Server) hostedAgent() *agentHost {
	s.agentMu.RLock()
	defer s.agentMu.RUnlock()
	return s.agent
}

// latheMCP serves Lathe's write commands to each agent session as MCP tools:
// `<exe> mcp --slug <slug>`, a stdio server scoped to the session's tutorial.
// The agent records results through it instead of shelling out to `lathe`.
// HOME is passed through so the tools write to the same ~/.lathe this server
// reads, whatever environment the agent gives its MCP servers.
func latheMCP(exe string) func(slug string) []acp.MCPServer {
	return func(slug string) []acp.MCPServer {
		var env []acp.EnvVariable
		if home, err := os.UserHomeDir(); err == nil {
			env = append(env, acp.EnvVariable{Name: "HOME", Value: home})
		}
		return []acp.MCPServer{{
			Name:    "lathe",
			Command: exe,
			Args:    []string{"mcp", "--slug", slug},
			Env:     env,
		}}
	}
}

func (h *agentHost) mcpServers(slug string) []acp.MCPServer {
	if h.mcp == nil {
		return nil
	}
	return h.mcp(slug)
}

// Close stops the hosted agent, if any.
func (s *Server) Close() {
	if h := s.hostedAgent(); h != nil {
		h.close()
	}
}

func (h *agentHost) close() {
	h.mu.Lock()
	c := h.client
	h.client = nil
	runs := make([]*agentRun, 0, len(h.runs))
	for _, r := range h.runs {
		runs = append(runs, r)
	}
	h.mu.Unlock()
	for _, r := range runs {
		r.cancel()
	}
	if c != nil {
		_ = c.Close()
	}
	// Let cancelled verify/extend runs restore the tutorial's status and
	// remove their scratch dirs before the server exits.
	deadline := time.After(10 * time.Second)
	for _, r := range runs {
		select {
		case <-r.done:
		case <-deadline:
			return
		}
	}
}

func alive(c *acp.Client) bool {
	if c == nil {
		return false
	}
	select {
	case <-c.Done():
		return false
	default:
		return true
	}
}

// ensure returns a live client, starting the agent if it isn't running.
func (h *agentHost) ensure() (*acp.Client, error) {
	h.startMu.Lock()
	defer h.startMu.Unlock()

	h.mu.Lock()
	if alive(h.client) {
		c := h.client
		h.mu.Unlock()
		return c, nil
	}
	old := h.client
	h.client = nil
	h.starting = true
	// Sessions belong to the old process.
	h.sessions = map[string]string{}
	h.primed = map[string]bool{}
	h.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), agentStartTimeout)
	defer cancel()
	c, err := h.start(ctx)

	h.mu.Lock()
	defer h.mu.Unlock()
	h.starting = false
	if err != nil {
		h.lastErr = err.Error()
		return nil, err
	}
	h.client = c
	h.lastErr = ""
	go h.watch(c)
	return c, nil
}

// watch records the agent's exit so the UI can say so.
func (h *agentHost) watch(c *acp.Client) {
	<-c.Done()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.client == c {
		h.lastErr = "the agent exited: " + exitReason(c.Err())
	}
}

func exitReason(err error) string {
	if err == nil {
		return "unknown reason"
	}
	msg := strings.TrimPrefix(err.Error(), acp.ErrClosed.Error()+": ")
	if msg == "EOF" {
		return "its output closed"
	}
	return msg
}

// agentStatus is what /-/worker reports about the hosted agent.
type agentStatus struct {
	Command string `json:"command"`
	// ID is the registry id when the command is a known agent; Name is what
	// the panel calls it.
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	// Model is the model the agent reported for its last session, if any.
	Model string `json:"model,omitempty"`
	// Working names the job running now ("ask", "verify", "extend" or
	// "check"), or is
	// empty while the agent waits.
	Working string `json:"working,omitempty"`
	// State is "starting", "ready" or "down".
	State string `json:"state"`
	Error string `json:"error,omitempty"`
	// Isolation says whether the agent's file access goes through Lathe's
	// per-job policy (only while the agent is running).
	Isolation *acp.Isolation `json:"isolation,omitempty"`
}

func (h *agentHost) status() agentStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := agentStatus{Command: h.command, ID: h.id, Name: h.name, Error: h.lastErr}
	// The newest active run says what the agent is doing.
	var newest *agentRun
	for _, id := range h.active {
		if r, ok := h.runs[id]; ok && (newest == nil || r.started.After(newest.started)) {
			newest = r
		}
	}
	if newest != nil {
		st.Working = newest.job
	}
	if h.client != nil {
		st.Model = h.client.Model()
	}
	switch {
	case h.starting:
		st.State = "starting"
	case alive(h.client):
		st.State = "ready"
		iso := h.client.Isolation()
		st.Isolation = &iso
	default:
		st.State = "down"
	}
	return st
}

// askPrompt builds the first prompt of a session: the /lathe-ask skill text,
// framed for the drawer, then the invocation exactly as the handoff would
// paste it. Follow-ups in the same session send only the question.
func askPrompt(tutDir, slug, part, question string) string {
	var b strings.Builder
	b.WriteString("You are answering a reader's question from the Ask drawer of the Lathe web reader. ")
	b.WriteString("Follow the /lathe-ask skill below. Your reply is shown to the reader as markdown, so reply with the answer itself — no preamble about the protocol.\n\n")
	fmt.Fprintf(&b, "The tutorial is stored in %s (your working directory); the reader is on %s.\n", tutDir, part)
	b.WriteString("Lathe lets this session read files under that directory only; it may not write files or run commands, and requests outside that are refused.\n\n")
	writeSkill(&b, "lathe-ask")
	fmt.Fprintf(&b, "/lathe-ask %s %s\n%s", slug, part, question)
	return b.String()
}

// skillText returns an embedded skill's body (frontmatter stripped), or ""
// if it isn't found.
func skillText(slug string) string {
	all, err := skills.All()
	if err != nil {
		return ""
	}
	for _, sk := range all {
		if sk.Slug == slug {
			return strings.TrimSpace(frontmatter.Strip(string(sk.Raw)))
		}
	}
	return ""
}

// writeSkill writes a skill's text inside a <skill> tag, so the agent needs
// no skills installed.
func writeSkill(b *strings.Builder, slug string) {
	fmt.Fprintf(b, "<skill name=%q>\n%s\n</skill>\n\n", slug, skillText(slug))
}

func followUpPrompt(part, question string) string {
	return fmt.Sprintf("Follow-up question from the reader (still on %s):\n%s", part, question)
}

var errAskBusy = errors.New("an answer to the last question is still streaming")

// startAsk opens (or reuses) the session for this part and starts streaming
// an answer. It returns an error — and the caller falls back to the handoff —
// when the agent can't be started or the session can't be opened.
func (h *agentHost) startAsk(tutDir, slug, part, question string) (*agentRun, error) {
	key := slug + "/" + part
	h.mu.Lock()
	if id, ok := h.active[key]; ok {
		h.mu.Unlock()
		return nil, fmt.Errorf("%w (run %s)", errAskBusy, id)
	}
	h.mu.Unlock()

	c, err := h.ensure()
	if err != nil {
		return nil, err
	}

	h.mu.Lock()
	sid, ok := h.sessions[key]
	h.mu.Unlock()
	if !ok {
		abs, aerr := filepath.Abs(tutDir)
		if aerr != nil {
			abs = tutDir
		}
		ctx, cancel := context.WithTimeout(context.Background(), agentStartTimeout)
		sid, err = c.NewSession(ctx, abs, h.mcpServers(slug)...)
		cancel()
		if err != nil {
			return nil, err
		}
		c.SetPolicy(sid, acp.AskPolicy(abs))
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if _, busy := h.active[key]; busy {
		return nil, errAskBusy
	}
	h.sessions[key] = sid
	prompt := followUpPrompt(part, question)
	if !h.primed[sid] {
		prompt = askPrompt(tutDir, slug, part, question)
		h.primed[sid] = true
	}
	h.pruneLocked()
	h.counter++
	ctx, cancel := context.WithTimeout(context.Background(), askRunTimeout)
	run := &agentRun{
		id:       strconv.FormatInt(h.counter, 10),
		job:      jobAsk,
		ctx:      ctx,
		cancel:   cancel,
		notify:   make(chan struct{}),
		perms:    map[string]chan string{},
		done:     make(chan struct{}),
		started:  time.Now(),
		fallback: "/lathe-ask " + slug + " " + part + "\n" + question,
	}
	h.runs[run.id] = run
	h.active[key] = run.id
	go h.drive(c, key, sid, prompt, run)
	return run, nil
}

func (h *agentHost) pruneLocked() {
	for id, r := range h.runs {
		if r.finishedBefore(time.Now().Add(-runRetention)) {
			delete(h.runs, id)
		}
	}
}

func (h *agentHost) run(id string) (*agentRun, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.runs[id]
	return r, ok
}

// drive runs one prompt to completion and turns the outcome into the run's
// final event.
func (h *agentHost) drive(c *acp.Client, key, sid, prompt string, run *agentRun) {
	defer close(run.done)
	defer run.cancel()
	stopRender := make(chan struct{})
	rendered := make(chan struct{})
	go run.renderLive(stopRender, rendered)
	stop, err := c.Prompt(run.ctx, sid, prompt, run)
	// A run that takes replies (a new tutorial) whose turn ended on a
	// question waits for the reader's answer and goes on in the same session.
	for err == nil && stop == "end_turn" && run.wantsReply() {
		reply, ok := run.waitReply(c.Done())
		if !ok {
			switch {
			case !alive(c):
				err = errors.New("the agent exited while waiting for a reply")
			case run.ctx.Err() != nil:
				stop = "cancelled"
			}
			break
		}
		stop, err = c.Prompt(run.ctx, sid, reply, run)
	}
	close(stopRender)
	<-rendered

	h.mu.Lock()
	delete(h.active, key)
	h.mu.Unlock()

	// A job's cleanup (restore a status the agent left in flight, remove the
	// scratch dir) runs before the final event, so a browser that polls the
	// status when it sees "done" already finds it settled.
	var note string
	if run.after != nil {
		note = run.after()
	}

	switch {
	case err != nil && !alive(c):
		// The process died mid-answer. Forget its sessions so the next ask
		// starts a fresh agent, and hand the reader the paste-able command.
		reason := exitReason(c.Err())
		h.mu.Lock()
		if h.client == c {
			h.lastErr = "the agent exited: " + reason
		}
		h.mu.Unlock()
		run.finish(runEvent{Type: "error", Message: withNote("The agent exited before it finished "+run.doing()+" ("+reason+").", note), Command: run.fallback})
	case err != nil && run.ctx.Err() == nil:
		// The agent refused the prompt (not logged in, bad request, …). The
		// session may be unusable, so drop it.
		h.mu.Lock()
		delete(h.sessions, key)
		h.mu.Unlock()
		run.finish(runEvent{Type: "error", Message: withNote("The agent could not "+run.verb()+": "+err.Error(), note), Command: run.fallback})
	default:
		if stop == "" {
			stop = "cancelled"
		}
		text := run.answer()
		ev := runEvent{Type: "done", StopReason: stop, Message: note}
		if text != "" {
			if html, rerr := RenderMarkdown([]byte(text)); rerr == nil {
				ev.HTML = string(html)
			}
		}
		run.finish(ev)
	}

	// Canceled (not DeadlineExceeded) means the reader stopped the run, or
	// the server is shutting down; drive's deferred cancel hasn't run yet.
	h.mu.Lock()
	finished := h.finished
	h.mu.Unlock()
	if finished != nil {
		finished(run, run.last(), errors.Is(run.ctx.Err(), context.Canceled))
	}
}

// last returns the run's final event (zero before it has one).
func (r *agentRun) last() runEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.events) == 0 {
		return runEvent{}
	}
	return r.events[len(r.events)-1]
}

func withNote(msg, note string) string {
	if note == "" {
		return msg
	}
	return msg + " " + note
}

// runEvent is one entry in a run's event stream (sent to the browser as
// server-sent events).
type runEvent struct {
	Type string `json:"type"` // chunk, html, plan, tool, tool_update, activity, permission, permission_resolved, done, error
	Text string `json:"text,omitempty"`
	// html (the answer so far, rendered) and done
	// permission / permission_resolved
	ID      string                 `json:"id,omitempty"`
	Title   string                 `json:"title,omitempty"`
	Options []acp.PermissionOption `json:"options,omitempty"`
	Choice  string                 `json:"choice,omitempty"`
	// Input is the exact command (or path) a permission prompt is about,
	// from the tool call's rawInput, when the agent sent one.
	Input string `json:"input,omitempty"`
	HTML  string `json:"html,omitempty"`
	// tool / tool_update: the agent's tool call, keyed by ID
	Status string `json:"status,omitempty"`
	Kind   string `json:"kind,omitempty"`
	// plan: the agent's whole current plan (each plan event replaces the last)
	Entries []acp.PlanEntry `json:"entries,omitempty"`
	// done
	StopReason string `json:"stopReason,omitempty"`
	// activity: a file read/write or command Lathe ran (or refused) for the
	// agent under the session's policy
	Activity *acp.Activity `json:"activity,omitempty"`
	// error, and done when a job's cleanup has something to say
	Message string `json:"message,omitempty"`
	Command string `json:"command,omitempty"`
}

// agentRun is one streamed prompt: an Ask answer, or a Verify/Extend job. It
// implements acp.SessionHandler.
type agentRun struct {
	id       string
	job      string // "ask", "verify", "extend" or "check"
	ctx      context.Context
	cancel   context.CancelFunc
	started  time.Time
	fallback string // the handoff command, offered on failure
	// after, when set, runs once the prompt has ended, before the final
	// event; what it returns is added to that event's message.
	after func() string
	// target is the tutorial (and, for a check, the part) a verify, extend
	// or check job works on; zero for an ask.
	target jobTarget
	done   chan struct{} // closed when drive has returned
	// replies, when set (a new-tutorial run), carries the reader's answers
	// to a question the agent ended its turn with; awaiting says whether an
	// ended turn still needs one (see agent_new.go). nil for every other job.
	replies  chan string
	awaiting func() bool

	mu       sync.Mutex
	events   []runEvent
	notify   chan struct{} // closed and replaced on every append
	finished time.Time
	text     strings.Builder
	perms    map[string]chan string
	permSeq  int
	// newBlock is set when the agent ends a message (a tool call, a thought,
	// a new messageId): the next text starts its own markdown block.
	newBlock  bool
	messageID string
	// lastHTML is the index of the newest html event; older snapshots are
	// emptied when a new one lands, so memory and replays stay small.
	lastHTML int
	textRev  int // bumped on every text append
	// waiting is set while drive waits for a reply (POST /-/acp/{id}/reply).
	waiting bool
}

func (r *agentRun) appendLocked(ev runEvent) {
	if ev.Type == "html" {
		if r.lastHTML > 0 {
			r.events[r.lastHTML-1].HTML = ""
		}
		r.lastHTML = len(r.events) + 1
	}
	r.events = append(r.events, ev)
	close(r.notify)
	r.notify = make(chan struct{})
}

func (r *agentRun) push(ev runEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.finished.IsZero() {
		return
	}
	r.appendLocked(ev)
}

func (r *agentRun) finish(ev runEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.finished.IsZero() {
		return
	}
	r.appendLocked(ev)
	r.finished = time.Now()
}

func (r *agentRun) finishedBefore(t time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.finished.IsZero() && r.finished.Before(t)
}

func (r *agentRun) answer() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.text.String()
}

// since returns the events from index i on, whether the run is finished, and
// a channel closed on the next append.
func (r *agentRun) since(i int) ([]runEvent, bool, <-chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []runEvent
	for ; i < len(r.events); i++ {
		ev := r.events[i]
		if ev.Type == "html" && ev.HTML == "" {
			// A superseded snapshot: keep its slot (SSE ids are indices) but
			// send nothing heavy.
			ev = runEvent{Type: "html_stale"}
		}
		out = append(out, ev)
	}
	return out, !r.finished.IsZero(), r.notify
}

// renderLive re-renders the answer so far every liveRenderEvery while the
// prompt runs, so the drawer shows formatted markdown as it streams. It
// closes done when it has stopped.
func (r *agentRun) renderLive(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	t := time.NewTicker(liveRenderEvery)
	defer t.Stop()
	seen := 0
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		r.mu.Lock()
		rev, text := r.textRev, r.text.String()
		r.mu.Unlock()
		if rev == seen || text == "" {
			continue
		}
		seen = rev
		html, err := RenderMarkdown([]byte(text))
		if err != nil {
			continue
		}
		r.push(runEvent{Type: "html", HTML: string(html)})
	}
}

// Update relays the parts of the agent's stream the drawer shows: answer text,
// and a one-line note when the agent starts a tool call.
func (r *agentRun) Update(u acp.SessionUpdate) {
	switch u.Kind {
	case "agent_message_chunk":
		t := u.Text()
		if t == "" {
			return
		}
		r.mu.Lock()
		if u.MessageID != "" {
			if r.messageID != "" && u.MessageID != r.messageID {
				r.newBlock = true
			}
			r.messageID = u.MessageID
		}
		if r.newBlock && r.text.Len() > 0 {
			// Separate this message from the last as its own paragraph.
			sep := "\n\n"
			cur := r.text.String()
			if strings.HasSuffix(cur, "\n\n") {
				sep = ""
			} else if strings.HasSuffix(cur, "\n") {
				sep = "\n"
			}
			t = sep + t
		}
		r.newBlock = false
		r.text.WriteString(t)
		r.textRev++
		r.mu.Unlock()
		r.push(runEvent{Type: "chunk", Text: t})
	case "tool_call":
		r.markNewBlock()
		if u.Title != "" {
			r.push(runEvent{Type: "tool", ID: u.ToolCallID, Title: u.Title, Status: u.Status, Kind: u.ToolKind})
		}
	case "tool_call_update":
		r.markNewBlock()
		if u.ToolCallID != "" && (u.Status != "" || u.Title != "") {
			r.push(runEvent{Type: "tool_update", ID: u.ToolCallID, Title: u.Title, Status: u.Status})
		}
	case "plan":
		r.markNewBlock()
		r.push(runEvent{Type: "plan", Entries: u.Entries})
	case "agent_thought_chunk":
		r.markNewBlock()
	}
}

// Activity shows a file read/write or command Lathe handled for the agent —
// including ones the policy refused — in the drawer.
func (r *agentRun) Activity(a acp.Activity) {
	r.push(runEvent{Type: "activity", Activity: &a})
}

// verb and doing name the run's work for error messages.
func (r *agentRun) verb() string {
	switch r.job {
	case jobVerify:
		return "verify the tutorial"
	case jobExtend:
		return "write the part"
	case jobCheck:
		return "check your work"
	case jobNew:
		return "write the tutorial"
	}
	return "answer"
}

func (r *agentRun) doing() string {
	switch r.job {
	case jobVerify:
		return "verifying"
	case jobExtend:
		return "writing the part"
	case jobCheck:
		return "checking your work"
	case jobNew:
		return "writing the tutorial"
	}
	return "answering"
}

func (r *agentRun) markNewBlock() {
	r.mu.Lock()
	r.newBlock = true
	r.mu.Unlock()
}

// RequestPermission shows the agent's prompt in the drawer and waits for the
// reader's choice.
func (r *agentRun) RequestPermission(ctx context.Context, req acp.PermissionRequest) (string, bool) {
	ch := make(chan string, 1)
	r.mu.Lock()
	if !r.finished.IsZero() {
		r.mu.Unlock()
		return "", false
	}
	r.permSeq++
	id := "p" + strconv.Itoa(r.permSeq)
	r.perms[id] = ch
	title := req.ToolCall.Title
	if title == "" {
		title = "The agent wants to use a tool"
	}
	r.appendLocked(runEvent{Type: "permission", ID: id, Title: title, Options: req.Options, Kind: req.ToolCall.Kind, Input: req.ToolCall.Input()})
	r.mu.Unlock()

	var choice string
	select {
	case choice = <-ch:
	case <-ctx.Done():
	case <-r.ctx.Done():
	case <-time.After(permissionTimeout):
	}
	r.mu.Lock()
	delete(r.perms, id)
	r.mu.Unlock()
	r.push(runEvent{Type: "permission_resolved", ID: id, Choice: choice})
	if choice == "" {
		return "", false
	}
	for _, o := range req.Options {
		if o.OptionID == choice {
			return choice, true
		}
	}
	return "", false
}

// answerPermission delivers the reader's choice. It reports false if the
// prompt is no longer waiting.
func (r *agentRun) answerPermission(id, optionID string) bool {
	r.mu.Lock()
	ch, ok := r.perms[id]
	delete(r.perms, id)
	r.mu.Unlock()
	if !ok {
		return false
	}
	ch <- optionID
	return true
}
