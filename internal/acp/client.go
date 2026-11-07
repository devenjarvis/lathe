package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// ContentBlock is an ACP content block. Lathe sends and reads text only; other
// block types decode with an empty Text.
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// SessionUpdate is one session/update notification's payload. Kind is the
// sessionUpdate discriminator (agent_message_chunk, agent_thought_chunk,
// tool_call, tool_call_update, plan, …); the fields Lathe reads are lifted out
// and Raw keeps the whole object.
type SessionUpdate struct {
	Kind       string          `json:"sessionUpdate"`
	Content    json.RawMessage `json:"content,omitempty"`
	ToolCallID string          `json:"toolCallId,omitempty"`
	Title      string          `json:"title,omitempty"`
	Status     string          `json:"status,omitempty"`
	// MessageID, when the agent sends one, changes between the separate
	// messages of one turn.
	MessageID string `json:"messageId,omitempty"`
	// ToolKind is a tool call's kind (read, edit, execute, …).
	ToolKind string `json:"kind,omitempty"`
	// Entries is a plan update's full list of steps; each plan update
	// replaces the last.
	Entries []PlanEntry     `json:"entries,omitempty"`
	Raw     json.RawMessage `json:"-"`
}

// PlanEntry is one step of an agent's plan. Status is pending, in_progress or
// completed; Priority is high, medium or low.
type PlanEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority,omitempty"`
	Status   string `json:"status,omitempty"`
}

// Text returns the update's text when its content is a single text block (as
// in agent_message_chunk), else "".
func (u SessionUpdate) Text() string {
	var b ContentBlock
	if json.Unmarshal(u.Content, &b) == nil && b.Type == "text" {
		return b.Text
	}
	return ""
}

// PermissionOption is one choice the agent offers in a permission request.
// Kind is allow_once, allow_always, reject_once or reject_always.
type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

// ToolCallRef is the part of a permission request's toolCall Lathe shows.
type ToolCallRef struct {
	ToolCallID string `json:"toolCallId"`
	Title      string `json:"title,omitempty"`
	Kind       string `json:"kind,omitempty"`
}

// PermissionRequest is a session/request_permission call from the agent.
type PermissionRequest struct {
	SessionID string             `json:"sessionId"`
	ToolCall  ToolCallRef        `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
}

// SessionHandler receives one session's traffic while a prompt runs.
type SessionHandler interface {
	// Update is called in order for each session/update. It must not block.
	Update(SessionUpdate)
	// RequestPermission blocks until the user picks an option. It returns the
	// chosen optionId, or ok=false to answer "cancelled" (the user went away,
	// or ctx ended).
	RequestPermission(ctx context.Context, req PermissionRequest) (optionID string, ok bool)
}

// AgentInfo is what the agent reported at initialize.
type AgentInfo struct {
	ProtocolVersion int             `json:"protocolVersion"`
	Capabilities    json.RawMessage `json:"agentCapabilities,omitempty"`
	AuthMethods     json.RawMessage `json:"authMethods,omitempty"`
}

// Client is a connection to one agent process (or, in tests, any stream).
type Client struct {
	conn   *Conn
	closer func() error

	mu        sync.Mutex
	sessions  map[string]SessionHandler
	policies  map[string]*Policy
	terminals map[string]*terminal
	termSeq   int64
	closed    bool
	iso       isolationCounts

	// CancelGrace is how long Prompt keeps waiting for the agent's
	// stopReason after sending session/cancel.
	CancelGrace time.Duration
}

// Start launches command through /bin/sh (so a configured agent command can
// carry flags and env), connects to its stdio and runs initialize. dir is the
// process's working directory; stderr receives the agent's own logging.
func Start(ctx context.Context, command, dir string, stderr io.Writer) (*Client, error) {
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Dir = dir
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("acp: start agent %q: %w", command, err)
	}
	waited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(waited) }()
	closer := func() error {
		_ = stdin.Close()
		select {
		case <-waited:
		case <-time.After(2 * time.Second):
			_ = cmd.Process.Kill()
			<-waited
		}
		return nil
	}
	c := NewClient(stdout, stdin, closer)
	if _, err := c.Initialize(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// NewClient wraps an already-connected stream. closer (optional) is run by
// Close. Initialize is not called.
func NewClient(r io.Reader, w io.Writer, closer func() error) *Client {
	c := &Client{
		closer:      closer,
		sessions:    map[string]SessionHandler{},
		policies:    map[string]*Policy{},
		terminals:   map[string]*terminal{},
		CancelGrace: 5 * time.Second,
	}
	c.conn = NewConn(r, w, c)
	return c
}

// Done is closed when the agent's stream ends (the process exited or crashed).
func (c *Client) Done() <-chan struct{} { return c.conn.Done() }

// Err reports why the stream ended, or nil while it is open.
func (c *Client) Err() error { return c.conn.Err() }

// Close stops the agent and any command it started.
func (c *Client) Close() error {
	c.killTerminals()
	if c.closer != nil {
		return c.closer()
	}
	return nil
}

// Initialize negotiates the protocol version. Lathe advertises the fs and
// terminal capabilities, and answers those requests within each session's
// Policy (see SetPolicy); an agent that still uses its own tools shows up in
// Isolation as "soft".
func (c *Client) Initialize(ctx context.Context) (*AgentInfo, error) {
	params := map[string]any{
		"protocolVersion": ProtocolVersion,
		"clientInfo":      map[string]string{"name": "lathe", "title": "Lathe"},
		"clientCapabilities": map[string]any{
			"fs":       map[string]bool{"readTextFile": true, "writeTextFile": true},
			"terminal": true,
		},
	}
	var info AgentInfo
	if err := c.conn.Call(ctx, "initialize", params, &info); err != nil {
		return nil, err
	}
	if info.ProtocolVersion != ProtocolVersion {
		return nil, fmt.Errorf("acp: agent speaks protocol version %d, lathe speaks %d", info.ProtocolVersion, ProtocolVersion)
	}
	return &info, nil
}

// EnvVariable is one environment variable for an MCP server process.
type EnvVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// MCPServer is a stdio MCP server the agent should connect to for the
// session. Every ACP agent supports the stdio transport.
type MCPServer struct {
	Name    string        `json:"name"`
	Command string        `json:"command"` // absolute path
	Args    []string      `json:"args"`
	Env     []EnvVariable `json:"env"`
}

// NewSession opens a session rooted at cwd (an absolute path), with the
// given MCP servers connected, and returns its id.
func (c *Client) NewSession(ctx context.Context, cwd string, servers ...MCPServer) (string, error) {
	var out struct {
		SessionID string `json:"sessionId"`
	}
	list := make([]MCPServer, 0, len(servers))
	for _, s := range servers {
		// The schema requires both arrays, so never send null.
		if s.Args == nil {
			s.Args = []string{}
		}
		if s.Env == nil {
			s.Env = []EnvVariable{}
		}
		list = append(list, s)
	}
	params := map[string]any{"cwd": cwd, "mcpServers": list}
	if err := c.conn.Call(ctx, "session/new", params, &out); err != nil {
		return "", err
	}
	if out.SessionID == "" {
		return "", errors.New("acp: session/new returned no sessionId")
	}
	return out.SessionID, nil
}

// Prompt sends text as one user turn and returns the agent's stopReason
// (end_turn, max_tokens, max_turn_requests, refusal, cancelled). Updates and
// permission requests for the session go to h while it runs. If ctx ends
// first, Prompt sends session/cancel and waits up to CancelGrace for the
// agent to wind down.
func (c *Client) Prompt(ctx context.Context, sessionID, text string, h SessionHandler) (string, error) {
	c.mu.Lock()
	if _, busy := c.sessions[sessionID]; busy {
		c.mu.Unlock()
		return "", fmt.Errorf("acp: session %s already has a prompt running", sessionID)
	}
	c.sessions[sessionID] = h
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.sessions, sessionID)
		c.mu.Unlock()
	}()

	params := map[string]any{
		"sessionId": sessionID,
		"prompt":    []ContentBlock{{Type: "text", Text: text}},
	}
	type result struct {
		stop string
		err  error
	}
	resCh := make(chan result, 1)
	callCtx, stopCall := context.WithCancel(context.Background())
	defer stopCall()
	go func() {
		var out struct {
			StopReason string `json:"stopReason"`
		}
		err := c.conn.Call(callCtx, "session/prompt", params, &out)
		resCh <- result{out.StopReason, err}
	}()

	select {
	case r := <-resCh:
		return r.stop, r.err
	case <-ctx.Done():
	}
	_ = c.conn.Notify("session/cancel", map[string]string{"sessionId": sessionID})
	select {
	case r := <-resCh:
		if r.err != nil {
			return "cancelled", ctx.Err()
		}
		return r.stop, nil
	case <-time.After(c.CancelGrace):
		return "cancelled", ctx.Err()
	}
}

func (c *Client) handlerFor(sessionID string) SessionHandler {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessions[sessionID]
}

// HandleNotification routes session/update to the running prompt's handler.
func (c *Client) HandleNotification(method string, params json.RawMessage) {
	if method != "session/update" {
		return
	}
	var p struct {
		SessionID string          `json:"sessionId"`
		Update    json.RawMessage `json:"update"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	var u SessionUpdate
	if json.Unmarshal(p.Update, &u) != nil {
		return
	}
	h := c.handlerFor(p.SessionID)
	if h == nil {
		if u.Kind == "tool_call" || u.Kind == "tool_call_update" {
			c.observeToolCall(u)
		}
		return
	}
	u.Raw = p.Update
	if u.Kind == "tool_call" || u.Kind == "tool_call_update" {
		c.observeToolCall(u)
	}
	h.Update(u)
}

// HandleRequest answers the agent's requests: permission prompts, and the
// fs/* and terminal/* methods within the session's Policy.
func (c *Client) HandleRequest(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if res, ok, err := c.handleClientMethod(ctx, method, params); ok {
		return res, err
	}
	if method != "session/request_permission" {
		return nil, &RPCError{Code: codeMethodNotFound, Message: "method not supported by lathe: " + method}
	}
	var req PermissionRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &RPCError{Code: -32602, Message: "invalid params"}
	}
	cancelled := map[string]any{"outcome": map[string]string{"outcome": "cancelled"}}
	h := c.handlerFor(req.SessionID)
	if h == nil {
		return cancelled, nil
	}
	optionID, ok := h.RequestPermission(ctx, req)
	if !ok {
		return cancelled, nil
	}
	return map[string]any{"outcome": map[string]string{"outcome": "selected", "optionId": optionID}}, nil
}
