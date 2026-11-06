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
	"github.com/devenjarvis/lathe/internal/frontmatter"
	"github.com/devenjarvis/lathe/internal/skills"
)

// The agent host lets `lathe serve --agent <cmd>` run the user's own coding
// agent as a child process and drive it over the Agent Client Protocol. The
// binary still never calls a model API: the agent does all model work, under
// the user's own login, and Lathe only relays the conversation and the
// agent's permission prompts to the browser.
//
// MVP scope: Ask only. Verify/Extend keep using the /lathe-work queue or the
// copy-paste handoff.

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
)

// agentStarter launches and initializes an agent. Tests swap in a fake.
type agentStarter func(ctx context.Context) (*acp.Client, error)

// agentHost owns the agent process, its sessions, and the in-flight asks.
type agentHost struct {
	command string
	start   agentStarter

	startMu sync.Mutex // serializes (re)starts

	mu       sync.Mutex
	client   *acp.Client
	starting bool
	lastErr  string
	sessions map[string]string // "slug/part" → sessionId on the current client
	primed   map[string]bool   // sessionId → already received the skill text
	runs     map[string]*askRun
	active   map[string]string // "slug/part" → run id of the running ask
	counter  int64
}

func newAgentHost(command string, start agentStarter) *agentHost {
	return &agentHost{
		command:  command,
		start:    start,
		sessions: map[string]string{},
		primed:   map[string]bool{},
		runs:     map[string]*askRun{},
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
	s.setAgentHost(newAgentHost(command, processStarter(command, s.tutorialsDir, os.Stderr)))
	go func() { _, _ = s.agent.ensure() }()
}

func (s *Server) setAgentHost(h *agentHost) { s.agent = h }

// Close stops the hosted agent, if any.
func (s *Server) Close() {
	if s.agent != nil {
		s.agent.close()
	}
}

func (h *agentHost) close() {
	h.mu.Lock()
	c := h.client
	h.client = nil
	runs := make([]*askRun, 0, len(h.runs))
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
	// State is "starting", "ready" or "down".
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

func (h *agentHost) status() agentStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := agentStatus{Command: h.command, Error: h.lastErr}
	switch {
	case h.starting:
		st.State = "starting"
	case alive(h.client):
		st.State = "ready"
	default:
		st.State = "down"
	}
	return st
}

// askPrompt builds the first prompt of a session: the /lathe-ask skill text,
// framed for the drawer, then the invocation exactly as the handoff would
// paste it. Follow-ups in the same session send only the question.
func askPrompt(tutDir, slug, part, question string) string {
	var skill string
	if all, err := skills.All(); err == nil {
		for _, sk := range all {
			if sk.Slug == "lathe-ask" {
				skill = frontmatter.Strip(string(sk.Raw))
			}
		}
	}
	var b strings.Builder
	b.WriteString("You are answering a reader's question from the Ask drawer of the Lathe web reader. ")
	b.WriteString("Follow the /lathe-ask skill below. Your reply is shown to the reader as markdown, so reply with the answer itself — no preamble about the protocol.\n\n")
	fmt.Fprintf(&b, "The tutorial is stored in %s (your working directory); the reader is on %s.\n\n", tutDir, part)
	b.WriteString("<skill name=\"lathe-ask\">\n")
	b.WriteString(strings.TrimSpace(skill))
	b.WriteString("\n</skill>\n\n")
	fmt.Fprintf(&b, "/lathe-ask %s %s\n%s", slug, part, question)
	return b.String()
}

func followUpPrompt(part, question string) string {
	return fmt.Sprintf("Follow-up question from the reader (still on %s):\n%s", part, question)
}

var errAskBusy = errors.New("an answer to the last question is still streaming")

// startAsk opens (or reuses) the session for this part and starts streaming
// an answer. It returns an error — and the caller falls back to the handoff —
// when the agent can't be started or the session can't be opened.
func (h *agentHost) startAsk(tutDir, slug, part, question string) (*askRun, error) {
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
		sid, err = c.NewSession(ctx, abs)
		cancel()
		if err != nil {
			return nil, err
		}
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
	run := &askRun{
		id:       strconv.FormatInt(h.counter, 10),
		ctx:      ctx,
		cancel:   cancel,
		notify:   make(chan struct{}),
		perms:    map[string]chan string{},
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

func (h *agentHost) run(id string) (*askRun, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.runs[id]
	return r, ok
}

// drive runs one prompt to completion and turns the outcome into the run's
// final event.
func (h *agentHost) drive(c *acp.Client, key, sid, prompt string, run *askRun) {
	defer run.cancel()
	stop, err := c.Prompt(run.ctx, sid, prompt, run)

	h.mu.Lock()
	delete(h.active, key)
	h.mu.Unlock()

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
		run.finish(runEvent{Type: "error", Message: "The agent exited before it finished answering (" + reason + ").", Command: run.fallback})
	case err != nil && run.ctx.Err() == nil:
		// The agent refused the prompt (not logged in, bad request, …). The
		// session may be unusable, so drop it.
		h.mu.Lock()
		delete(h.sessions, key)
		h.mu.Unlock()
		run.finish(runEvent{Type: "error", Message: "The agent could not answer: " + err.Error(), Command: run.fallback})
	default:
		if stop == "" {
			stop = "cancelled"
		}
		text := run.answer()
		ev := runEvent{Type: "done", StopReason: stop}
		if text != "" {
			if html, rerr := RenderMarkdown([]byte(text)); rerr == nil {
				ev.HTML = string(html)
			}
		}
		run.finish(ev)
	}
}

// runEvent is one entry in an ask run's event stream (sent to the browser as
// server-sent events).
type runEvent struct {
	Type string `json:"type"` // chunk, tool, permission, permission_resolved, done, error
	Text string `json:"text,omitempty"`
	// permission / permission_resolved
	ID      string                 `json:"id,omitempty"`
	Title   string                 `json:"title,omitempty"`
	Options []acp.PermissionOption `json:"options,omitempty"`
	Choice  string                 `json:"choice,omitempty"`
	// done
	StopReason string `json:"stopReason,omitempty"`
	HTML       string `json:"html,omitempty"`
	// error
	Message string `json:"message,omitempty"`
	Command string `json:"command,omitempty"`
}

// askRun is one streamed answer. It implements acp.SessionHandler.
type askRun struct {
	id       string
	ctx      context.Context
	cancel   context.CancelFunc
	started  time.Time
	fallback string // the handoff command, offered on failure

	mu       sync.Mutex
	events   []runEvent
	notify   chan struct{} // closed and replaced on every append
	finished time.Time
	text     strings.Builder
	perms    map[string]chan string
	permSeq  int
}

func (r *askRun) appendLocked(ev runEvent) {
	r.events = append(r.events, ev)
	close(r.notify)
	r.notify = make(chan struct{})
}

func (r *askRun) push(ev runEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.finished.IsZero() {
		return
	}
	r.appendLocked(ev)
}

func (r *askRun) finish(ev runEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.finished.IsZero() {
		return
	}
	r.appendLocked(ev)
	r.finished = time.Now()
}

func (r *askRun) finishedBefore(t time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.finished.IsZero() && r.finished.Before(t)
}

func (r *askRun) answer() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.text.String()
}

// since returns the events from index i on, whether the run is finished, and
// a channel closed on the next append.
func (r *askRun) since(i int) ([]runEvent, bool, <-chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []runEvent
	if i < len(r.events) {
		out = append(out, r.events[i:]...)
	}
	return out, !r.finished.IsZero(), r.notify
}

// Update relays the parts of the agent's stream the drawer shows: answer text,
// and a one-line note when the agent starts a tool call.
func (r *askRun) Update(u acp.SessionUpdate) {
	switch u.Kind {
	case "agent_message_chunk":
		t := u.Text()
		if t == "" {
			return
		}
		r.mu.Lock()
		r.text.WriteString(t)
		r.mu.Unlock()
		r.push(runEvent{Type: "chunk", Text: t})
	case "tool_call":
		if u.Title != "" {
			r.push(runEvent{Type: "tool", Title: u.Title})
		}
	}
}

// RequestPermission shows the agent's prompt in the drawer and waits for the
// reader's choice.
func (r *askRun) RequestPermission(ctx context.Context, req acp.PermissionRequest) (string, bool) {
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
	r.appendLocked(runEvent{Type: "permission", ID: id, Title: title, Options: req.Options})
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
func (r *askRun) answerPermission(id, optionID string) bool {
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
