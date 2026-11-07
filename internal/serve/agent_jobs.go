package serve

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/devenjarvis/lathe/internal/acp"
	"github.com/devenjarvis/lathe/internal/store"
)

// Verify and Extend on the hosted agent. Each job gets a fresh ACP session
// and a scratch dir Lathe makes for it, under the job's policy
// (acp.VerifyPolicy / acp.ExtendPolicy). The prompt carries the skill text, so
// the agent needs no skills installed, and the agent records results through
// the Lathe MCP tools (or the CLI) exactly as in an interactive session — the
// skill still sets the status, never the server. Progress (plan, tool calls,
// the files and commands Lathe runs for it) streams to the reader over the
// same SSE endpoint as Ask.
//
// When the run ends without a result (cancelled, crashed, refused), Lathe
// undoes the in-flight state the skill left behind, the same as
// `lathe verify-cancel`: a stuck "verifying" goes back to the last result, and
// a stuck "extending" loses its unfinished part.

const (
	jobAsk    = "ask"
	jobVerify = "verify"
	jobExtend = "extend"

	// jobRunTimeout caps one verify or extend run. Following a long tutorial
	// end to end (installs, builds) takes a while.
	jobRunTimeout = 60 * time.Minute
)

var errJobBusy = errors.New("the agent is already working on this tutorial")

// jobKey is the active-run key for a tutorial's verify/extend job: one at a
// time per tutorial. Ask keys are "slug/part", and a slug never holds a "/",
// so the two can't collide.
func jobKey(slug string) string { return slug + "#job" }

// activeJob returns the verify/extend run in flight for slug, if any.
func (h *agentHost) activeJob(slug string) (*agentRun, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	id, ok := h.active[jobKey(slug)]
	if !ok {
		return nil, false
	}
	r, ok := h.runs[id]
	return r, ok
}

// jobPrompt is the whole prompt for a verify or extend run: framing for the
// hosted session, the skill text, then the invocation the handoff would paste.
func jobPrompt(job, tutDir, scratch, slug, command string) string {
	var b strings.Builder
	switch job {
	case jobVerify:
		b.WriteString("You are verifying a Lathe tutorial for the \"Verify this tutorial\" button in the Lathe web reader, which hosts you. ")
		b.WriteString("Follow the /lathe-verify skill below, start to finish, without stopping to ask questions: nobody is at a terminal, only a reader watching your progress.\n\n")
		fmt.Fprintf(&b, "- The tutorial is stored in %s; read its parts there (the skill's ~/.lathe/tutorials/%s/). Never edit it.\n", tutDir, slug)
		fmt.Fprintf(&b, "- Lathe made a fresh scratch dir for this run: %s (your working directory). Use it as $SCRATCH instead of running mktemp -d, and run every command with it as the working directory.\n", scratch)
		b.WriteString("- Lathe lets this session read the tutorial and the scratch dir, and write files and run commands only in the scratch dir; requests outside that are refused.\n")
		fmt.Fprintf(&b, "- Record each status with the lathe_verify_result tool (it defaults to this tutorial), or `lathe verify-result` if you don't have the tool. Pass the scratch dir as code (%s) so the code snapshot is saved.\n", scratch)
		b.WriteString("- Lathe deletes the scratch dir after the run ends, so record the result before you finish.\n")
		b.WriteString("- The reader sees your plan live: keep a plan with one entry per part and update it as each part passes or fails.\n")
		b.WriteString("- Finish with a short report for the reader (markdown): the result and, if something failed, where and why.\n\n")
		writeSkill(&b, "lathe-verify")
	case jobExtend:
		b.WriteString("You are writing the next part of a Lathe tutorial for the \"Add a part\" button in the Lathe web reader, which hosts you. ")
		b.WriteString("Follow the /lathe-extend skill below, which defers to the lathe skill (also below) for shape, research and callouts. Work start to finish without stopping to ask questions: nobody is at a terminal, only a reader watching your progress.\n\n")
		fmt.Fprintf(&b, "- The tutorial is stored in %s (your working directory; the skill's ~/.lathe/tutorials/%s/). Write the new part only at the path lathe_extend_start returns, and touch nothing else there.\n", tutDir, slug)
		fmt.Fprintf(&b, "- Lathe made a scratch dir for this run: %s. Try code there, and run every command with it as the working directory.\n", scratch)
		b.WriteString("- Lathe lets this session read the tutorial and the scratch dir, write the tutorial dir and the scratch dir, and run commands only in the scratch dir; requests outside that are refused.\n")
		b.WriteString("- Use the lathe_extend_start and lathe_extend_commit tools (they default to this tutorial), or the `lathe extend-start` / `lathe extend-commit` commands if you don't have the tools. A part you don't commit is discarded when the run ends.\n")
		b.WriteString("- The reader sees your plan live: keep a short plan (read, research, reserve, write, commit) and update it as you go.\n")
		b.WriteString("- Finish with a one-paragraph summary for the reader (markdown) of what the new part covers.\n\n")
		writeSkill(&b, "lathe-extend")
		writeSkill(&b, "lathe")
	}
	b.WriteString(command)
	return b.String()
}

// settleJob undoes the in-flight status a job left behind when it ended
// without a result. It returns a sentence for the reader, or "" when the
// skill recorded a result (nothing to undo).
func settleJob(job, tutDir string) string {
	var (
		st  store.Status
		err error
	)
	switch job {
	case jobVerify:
		st, err = store.CancelVerify(tutDir)
		if err == nil {
			return fmt.Sprintf("It stopped before recording a result, so Lathe restored the previous status (%s).", st)
		}
	case jobExtend:
		st, err = store.CancelExtend(tutDir)
		if err == nil {
			return fmt.Sprintf("It stopped before committing the part, so Lathe discarded the unfinished part and restored the previous status (%s).", st)
		}
	default:
		return ""
	}
	if errors.Is(err, store.ErrNotInFlight) {
		return ""
	}
	return "Lathe could not restore the tutorial's status (" + err.Error() + "); run `lathe verify-cancel` or re-run the job."
}

// startJob opens a fresh session for a verify or extend run and starts it.
// command is the handoff invocation ("/lathe-verify <slug>", …), offered if
// the run fails. It returns an error — and the caller falls back to the
// worker queue or the handoff — when the agent can't be started or the
// session can't be opened.
func (h *agentHost) startJob(job, tutDir, slug, command string) (*agentRun, error) {
	key := jobKey(slug)
	h.mu.Lock()
	_, busy := h.active[key]
	h.mu.Unlock()
	if busy {
		return nil, errJobBusy
	}

	c, err := h.ensure()
	if err != nil {
		return nil, err
	}

	abs, err := filepath.Abs(tutDir)
	if err != nil {
		return nil, err
	}
	if real, rerr := filepath.EvalSymlinks(abs); rerr == nil {
		abs = real
	}
	scratch, err := os.MkdirTemp("", "lathe-"+job+"-")
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

	cwd, policy := scratch, acp.VerifyPolicy(abs, scratch)
	if job == jobExtend {
		cwd, policy = abs, acp.ExtendPolicy(abs, scratch)
	}
	ctx, cancel := context.WithTimeout(context.Background(), agentStartTimeout)
	sid, err := c.NewSession(ctx, cwd, h.mcpServers(slug)...)
	cancel()
	if err != nil {
		return fail(err)
	}
	c.SetPolicy(sid, policy)

	h.mu.Lock()
	defer h.mu.Unlock()
	if _, busy := h.active[key]; busy {
		c.SetPolicy(sid, nil)
		return fail(errJobBusy)
	}
	h.pruneLocked()
	h.counter++
	runCtx, runCancel := context.WithTimeout(context.Background(), jobRunTimeout)
	run := &agentRun{
		id:       strconv.FormatInt(h.counter, 10),
		job:      job,
		ctx:      runCtx,
		cancel:   runCancel,
		notify:   make(chan struct{}),
		perms:    map[string]chan string{},
		done:     make(chan struct{}),
		started:  time.Now(),
		fallback: command,
		after: func() string {
			c.SetPolicy(sid, nil)
			note := settleJob(job, tutDir)
			_ = os.RemoveAll(scratch)
			return note
		},
	}
	h.runs[run.id] = run
	h.active[key] = run.id
	go h.drive(c, key, sid, jobPrompt(job, abs, scratch, slug, command), run)
	return run, nil
}

// jobViaAgent routes a verify or extend to the hosted agent. It returns false
// (having written nothing) when no agent is configured, or when the agent
// can't take the job — then the caller falls back to a /lathe-work worker or
// the copy-paste handoff, with notice saying why.
func (s *Server) jobViaAgent(w http.ResponseWriter, job, tutDir, slug, command string) (handled bool, notice string) {
	if s.agent == nil {
		return false, ""
	}
	run, err := s.agent.startJob(job, tutDir, slug, command)
	if err != nil {
		if errors.Is(err, errJobBusy) {
			http.Error(w, "conflict: the agent is already working on this tutorial", http.StatusConflict)
			return true, ""
		}
		return false, "The agent (" + s.agent.command + ") could not start: " + err.Error()
	}
	writeAgentRun(w, run.id)
	return true, ""
}

// handleJobCancel stops the hosted agent's verify or extend run for a
// tutorial (session/cancel) and waits for it to wind down, so the caller
// finds the status already restored. `lathe verify-cancel` calls it; the
// reader's Stop button uses POST /-/acp/{id}/cancel. 404 when no such run.
func (s *Server) handleJobCancel(job string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if s.agent == nil {
			http.NotFound(w, r)
			return
		}
		run, ok := s.agent.activeJob(r.PathValue("slug"))
		if !ok || run.job != job {
			http.NotFound(w, r)
			return
		}
		run.cancel()
		select {
		case <-run.done:
		case <-time.After(15 * time.Second):
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// jobRunInfo is what the reading page needs to reattach to a running job.
type jobRunInfo struct {
	ID  string `json:"id"`
	Job string `json:"job"`
}

// runningJob reports the hosted verify/extend run for slug, if any.
func (s *Server) runningJob(slug string) *jobRunInfo {
	if s.agent == nil {
		return nil
	}
	run, ok := s.agent.activeJob(slug)
	if !ok {
		return nil
	}
	return &jobRunInfo{ID: run.id, Job: run.job}
}
