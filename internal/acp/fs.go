package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// maxReadBytes caps one fs/read_text_file answer; one JSON-RPC frame carries
// the whole file.
const maxReadBytes = 10 << 20

// codeInvalidParams is the JSON-RPC "invalid params" code. Policy refusals use
// it too, with a message that says what is allowed, so the agent can correct
// its request rather than retry it.
const codeInvalidParams = -32602

// Activity is one thing Lathe did (or refused to do) for the agent through
// the client fs/terminal methods. Handlers that implement ActivityHandler get
// each one, so the UI can show the user what is being read, written and run.
type Activity struct {
	// Kind is read, write, terminal (a command started) or terminal_exit.
	Kind    string `json:"kind"`
	Path    string `json:"path,omitempty"`    // file, or a command's cwd
	Command string `json:"command,omitempty"` // terminal, terminal_exit
	// Denied is set when the policy refused the request; Error says why.
	Denied bool   `json:"denied,omitempty"`
	Error  string `json:"error,omitempty"`
	// Output is the tail of a finished command's output.
	Output   string `json:"output,omitempty"`
	ExitCode *int   `json:"exitCode,omitempty"`
}

// ActivityHandler is an optional SessionHandler extension.
type ActivityHandler interface {
	Activity(Activity)
}

// SetPolicy sets the file/terminal policy for a session. Until a session has
// one, every fs/* and terminal/* request in it is refused. A nil p forgets
// the session's policy, so a finished job's session is refused everything.
func (c *Client) SetPolicy(sessionID string, p *Policy) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p == nil {
		delete(c.policies, sessionID)
		return
	}
	c.policies[sessionID] = p
}

func (c *Client) policyFor(sessionID string) *Policy {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.policies[sessionID]
}

func (c *Client) report(sessionID string, a Activity) {
	if h, ok := c.handlerFor(sessionID).(ActivityHandler); ok {
		h.Activity(a)
	}
}

func invalidParams(err error) error {
	return &RPCError{Code: codeInvalidParams, Message: err.Error()}
}

func policyError(err error) error {
	return &RPCError{Code: codeInvalidParams, Message: "lathe: " + err.Error()}
}

func (c *Client) readTextFile(params json.RawMessage) (any, error) {
	var p struct {
		SessionID string `json:"sessionId"`
		Path      string `json:"path"`
		Line      *int   `json:"line"`
		Limit     *int   `json:"limit"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalidParams(err)
	}
	c.countClientCall(false)
	resolved, err := c.policyFor(p.SessionID).CheckRead(p.Path)
	if err != nil {
		c.report(p.SessionID, Activity{Kind: "read", Path: p.Path, Denied: true, Error: err.Error()})
		return nil, policyError(err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, &RPCError{Code: codeInternal, Message: err.Error()}
	}
	if info.IsDir() {
		return nil, invalidParams(fmt.Errorf("%s is a directory", p.Path))
	}
	if info.Size() > maxReadBytes {
		return nil, invalidParams(fmt.Errorf("%s is %d bytes; lathe reads at most %d", p.Path, info.Size(), maxReadBytes))
	}
	raw, err := os.ReadFile(resolved)
	if err != nil {
		return nil, &RPCError{Code: codeInternal, Message: err.Error()}
	}
	content := sliceLines(string(raw), p.Line, p.Limit)
	c.report(p.SessionID, Activity{Kind: "read", Path: p.Path})
	return map[string]string{"content": content}, nil
}

// sliceLines returns limit lines of s starting at 1-based line.
func sliceLines(s string, line, limit *int) string {
	if line == nil && limit == nil {
		return s
	}
	lines := strings.SplitAfter(s, "\n")
	start := 0
	if line != nil && *line > 1 {
		start = *line - 1
	}
	if start >= len(lines) {
		return ""
	}
	end := len(lines)
	if limit != nil && *limit >= 0 && start+*limit < end {
		end = start + *limit
	}
	return strings.Join(lines[start:end], "")
}

func (c *Client) writeTextFile(params json.RawMessage) (any, error) {
	var p struct {
		SessionID string `json:"sessionId"`
		Path      string `json:"path"`
		Content   string `json:"content"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalidParams(err)
	}
	c.countClientCall(false)
	resolved, err := c.policyFor(p.SessionID).CheckWrite(p.Path)
	if err != nil {
		c.report(p.SessionID, Activity{Kind: "write", Path: p.Path, Denied: true, Error: err.Error()})
		return nil, policyError(err)
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return nil, &RPCError{Code: codeInternal, Message: err.Error()}
	}
	if err := os.WriteFile(resolved, []byte(p.Content), 0o644); err != nil {
		return nil, &RPCError{Code: codeInternal, Message: err.Error()}
	}
	c.report(p.SessionID, Activity{Kind: "write", Path: p.Path})
	return map[string]any{}, nil
}

// handleClientMethod answers fs/* and terminal/*. ok is false for any other
// method.
func (c *Client) handleClientMethod(ctx context.Context, method string, params json.RawMessage) (result any, ok bool, err error) {
	switch method {
	case "fs/read_text_file":
		result, err = c.readTextFile(params)
	case "fs/write_text_file":
		result, err = c.writeTextFile(params)
	case "terminal/create":
		result, err = c.terminalCreate(params)
	case "terminal/output":
		result, err = c.terminalOutput(params)
	case "terminal/wait_for_exit":
		result, err = c.terminalWait(ctx, params)
	case "terminal/kill":
		result, err = c.terminalKill(params)
	case "terminal/release":
		result, err = c.terminalRelease(params)
	default:
		return nil, false, nil
	}
	return result, true, err
}
