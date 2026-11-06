package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// defaultOutputLimit caps a terminal's retained output when the agent sets no
// outputByteLimit.
const defaultOutputLimit = 1 << 20

// activityTail is how much of a finished command's output goes into its
// terminal_exit activity.
const activityTail = 4 << 10

// terminal is one command started by terminal/create.
type terminal struct {
	id      string
	session string
	command string
	cwd     string
	cmd     *exec.Cmd

	mu        sync.Mutex
	buf       []byte
	limit     int
	truncated bool
	exited    bool
	exitCode  *int
	signal    *string

	done chan struct{}
}

// Write appends output, dropping from the front past the limit (on a UTF-8
// boundary), as ACP specifies.
func (t *terminal) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.limit; over > 0 {
		for over < len(t.buf) && !utf8.RuneStart(t.buf[over]) {
			over++
		}
		t.buf = append(t.buf[:0:0], t.buf[over:]...)
		t.truncated = true
	}
	return len(p), nil
}

type exitStatus struct {
	ExitCode *int    `json:"exitCode"`
	Signal   *string `json:"signal"`
}

func (t *terminal) status() (string, bool, *exitStatus) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var st *exitStatus
	if t.exited {
		st = &exitStatus{ExitCode: t.exitCode, Signal: t.signal}
	}
	return string(t.buf), t.truncated, st
}

func (t *terminal) kill() {
	select {
	case <-t.done:
		return
	default:
	}
	if t.cmd.Process != nil {
		killProcess(t.cmd)
	}
}

func (c *Client) terminalCreate(params json.RawMessage) (any, error) {
	var p struct {
		SessionID string        `json:"sessionId"`
		Command   string        `json:"command"`
		Args      []string      `json:"args"`
		Env       []EnvVariable `json:"env"`
		Cwd       string        `json:"cwd"`
		Limit     *int          `json:"outputByteLimit"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalidParams(err)
	}
	if strings.TrimSpace(p.Command) == "" {
		return nil, invalidParams(errors.New("command is required"))
	}
	c.countClientCall(true)
	display := strings.TrimSpace(strings.Join(append([]string{p.Command}, p.Args...), " "))
	cwd, err := c.policyFor(p.SessionID).CheckExec(p.Cwd)
	if err != nil {
		c.report(p.SessionID, Activity{Kind: "terminal", Command: display, Path: p.Cwd, Denied: true, Error: err.Error()})
		return nil, policyError(err)
	}

	var cmd *exec.Cmd
	if len(p.Args) == 0 && strings.ContainsAny(p.Command, " \t\n|&;<>()$`'\"*?") {
		// A whole command line: run it the way the agent would in a shell.
		cmd = exec.Command("/bin/sh", "-c", p.Command)
	} else {
		cmd = exec.Command(p.Command, p.Args...)
	}
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	for _, e := range p.Env {
		cmd.Env = append(cmd.Env, e.Name+"="+e.Value)
	}
	setProcessGroup(cmd)

	limit := defaultOutputLimit
	if p.Limit != nil && *p.Limit >= 0 {
		limit = *p.Limit
	}
	t := &terminal{session: p.SessionID, command: display, cwd: cwd, cmd: cmd, limit: limit, done: make(chan struct{})}
	cmd.Stdout = t
	cmd.Stderr = t

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, &RPCError{Code: codeInternal, Message: "lathe is shutting the agent down"}
	}
	c.termSeq++
	t.id = "term-" + strconv.FormatInt(c.termSeq, 10)
	c.terminals[t.id] = t
	c.mu.Unlock()

	if err := cmd.Start(); err != nil {
		c.mu.Lock()
		delete(c.terminals, t.id)
		c.mu.Unlock()
		c.report(p.SessionID, Activity{Kind: "terminal", Command: display, Path: cwd, Error: err.Error()})
		return nil, &RPCError{Code: codeInternal, Message: fmt.Sprintf("start %q: %v", display, err)}
	}
	c.report(p.SessionID, Activity{Kind: "terminal", Command: display, Path: cwd})
	go func() {
		_ = cmd.Wait()
		code, sig := exitInfo(cmd.ProcessState)
		t.mu.Lock()
		t.exited = true
		t.exitCode, t.signal = code, sig
		t.mu.Unlock()
		close(t.done)
		out, _, _ := t.status()
		if len(out) > activityTail {
			cut := len(out) - activityTail
			for cut < len(out) && !utf8.RuneStart(out[cut]) {
				cut++
			}
			out = "…" + out[cut:]
		}
		c.report(t.session, Activity{Kind: "terminal_exit", Command: display, Path: cwd, Output: out, ExitCode: code})
	}()
	return map[string]string{"terminalId": t.id}, nil
}

func (c *Client) terminalFor(params json.RawMessage) (*terminal, error) {
	var p struct {
		SessionID  string `json:"sessionId"`
		TerminalID string `json:"terminalId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalidParams(err)
	}
	c.mu.Lock()
	t, ok := c.terminals[p.TerminalID]
	c.mu.Unlock()
	if !ok || t.session != p.SessionID {
		return nil, invalidParams(fmt.Errorf("no terminal %q in this session", p.TerminalID))
	}
	return t, nil
}

func (c *Client) terminalOutput(params json.RawMessage) (any, error) {
	t, err := c.terminalFor(params)
	if err != nil {
		return nil, err
	}
	out, truncated, st := t.status()
	res := map[string]any{"output": out, "truncated": truncated}
	if st != nil {
		res["exitStatus"] = st
	}
	return res, nil
}

func (c *Client) terminalWait(ctx context.Context, params json.RawMessage) (any, error) {
	t, err := c.terminalFor(params)
	if err != nil {
		return nil, err
	}
	select {
	case <-t.done:
	case <-ctx.Done():
		return nil, &RPCError{Code: codeInternal, Message: "lathe stopped waiting: " + ctx.Err().Error()}
	}
	_, _, st := t.status()
	return st, nil
}

func (c *Client) terminalKill(params json.RawMessage) (any, error) {
	t, err := c.terminalFor(params)
	if err != nil {
		return nil, err
	}
	t.kill()
	return map[string]any{}, nil
}

func (c *Client) terminalRelease(params json.RawMessage) (any, error) {
	t, err := c.terminalFor(params)
	if err != nil {
		return nil, err
	}
	t.kill()
	c.mu.Lock()
	delete(c.terminals, t.id)
	c.mu.Unlock()
	return map[string]any{}, nil
}

// killTerminals stops every command the agent started; Close calls it so no
// process outlives the agent.
func (c *Client) killTerminals() {
	c.mu.Lock()
	c.closed = true
	ts := make([]*terminal, 0, len(c.terminals))
	for _, t := range c.terminals {
		ts = append(ts, t)
	}
	c.terminals = map[string]*terminal{}
	c.mu.Unlock()
	for _, t := range ts {
		t.kill()
	}
}
