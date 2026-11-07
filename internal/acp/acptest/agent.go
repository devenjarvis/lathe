// Package acptest is a scripted fake ACP agent for tests and local demos. It
// speaks the agent half of the protocol over any reader/writer pair and
// decides what to do from markers in the prompt text:
//
//	[permission]  ask for permission first, then report the outcome
//	[crash]       stream one chunk, then stop as if the process died
//	[slow]        stream slowly, honouring session/cancel
//	[tool]        send a short message and a tool call before the reply, the
//	              way real agents split one turn into several messages
//	[read:PATH]   read PATH through the client's fs/read_text_file
//	[write:PATH]  write PATH through the client's fs/write_text_file
//	[run:CMD]     run CMD through the client's terminal/* methods
//	[owntool]     report a completed read tool call without asking the
//	              client, the way an agent using its own tools does
//
// Setting Agent.Plan makes every prompt also report a plan and work through
// it, one tool call per step, the way an agent following a skill does.
//
// A relative PATH is joined to the session's cwd. The outcome of each client
// call is streamed before the reply, e.g. "(read ok: 12 bytes)" or
// "(write refused: …)".
// Otherwise it streams Reply(prompt) in a few chunks and ends the turn.
package acptest

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrCrash is returned by Serve when a prompt asked the agent to crash.
var ErrCrash = errors.New("acptest: crashed on request")

// Agent is a fake ACP agent.
type Agent struct {
	// Reply returns the answer text for a prompt. Default: a short echo.
	Reply func(prompt string) string
	// ChunkDelay is the pause between streamed chunks.
	ChunkDelay time.Duration
	// Plan, when set, is reported as a plan before the reply; each step then
	// runs as a tool call (tool_call, tool_call_update completed) and the
	// plan is re-sent with that step completed.
	Plan []string
	// StepDelay is the pause while each plan step's tool call runs.
	StepDelay time.Duration

	mu        sync.Mutex
	w         io.Writer
	nextID    int
	waiting   map[string]chan frame
	cancelled map[string]bool
	sessions  int
	cwds      map[string]string // sessionId → cwd
	// Prompts records every prompt text received, in order.
	Prompts []string
	// Cwds records the cwd of every session/new.
	Cwds []string
	// MCPServers records the mcpServers of every session/new, as sent.
	MCPServers []json.RawMessage
}

type frame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

// Serve runs the agent until r ends. It returns ErrCrash if a prompt asked it
// to crash, nil on a clean EOF.
func (a *Agent) Serve(r io.Reader, w io.Writer) error {
	a.mu.Lock()
	a.w = w
	a.waiting = map[string]chan frame{}
	a.cwds = map[string]string{}
	a.cancelled = map[string]bool{}
	a.mu.Unlock()

	crash := make(chan struct{})
	var crashOnce sync.Once
	lines := make(chan []byte)
	go func() {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
		for sc.Scan() {
			lines <- append([]byte(nil), sc.Bytes()...)
		}
		close(lines)
	}()
	for {
		select {
		case <-crash:
			return ErrCrash
		case line, ok := <-lines:
			if !ok {
				return nil
			}
			var f frame
			if json.Unmarshal(line, &f) != nil {
				continue
			}
			if f.Method == "" {
				a.mu.Lock()
				ch := a.waiting[string(f.ID)]
				delete(a.waiting, string(f.ID))
				a.mu.Unlock()
				if ch != nil {
					ch <- f
				}
				continue
			}
			go a.handle(f, func() { crashOnce.Do(func() { close(crash) }) })
		}
	}
}

func (a *Agent) send(f frame) {
	f.JSONRPC = "2.0"
	raw, _ := json.Marshal(f)
	a.mu.Lock()
	defer a.mu.Unlock()
	_, _ = a.w.Write(append(raw, '\n'))
}

func (a *Agent) respond(id json.RawMessage, v any) {
	raw, _ := json.Marshal(v)
	a.send(frame{ID: id, Result: raw})
}

func (a *Agent) notify(method string, v any) {
	raw, _ := json.Marshal(v)
	a.send(frame{Method: method, Params: raw})
}

func (a *Agent) call(method string, v any) json.RawMessage {
	res, _ := a.callErr(method, v)
	return res
}

// callErr is call, returning the error message too ("" on success).
func (a *Agent) callErr(method string, v any) (json.RawMessage, string) {
	raw, _ := json.Marshal(v)
	ch := make(chan frame, 1)
	a.mu.Lock()
	a.nextID++
	id := json.RawMessage(strconv.Itoa(1000 + a.nextID))
	a.waiting[string(id)] = ch
	a.mu.Unlock()
	a.send(frame{ID: id, Method: method, Params: raw})
	return a.await(ch)
}

func (a *Agent) await(ch chan frame) (json.RawMessage, string) {
	f := <-ch
	if len(f.Error) > 0 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(f.Error, &e)
		if e.Message == "" {
			e.Message = "error"
		}
		return nil, e.Message
	}
	return f.Result, ""
}

var clientCallRe = regexp.MustCompile(`\[(read|write|run):([^\]]+)\]`)

// clientCalls runs the [read:…], [write:…] and [run:…] markers in prompt
// through the client and streams each outcome.
func (a *Agent) clientCalls(session, prompt string) {
	a.mu.Lock()
	cwd := a.cwds[session]
	a.mu.Unlock()
	abs := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(cwd, p)
	}
	for _, m := range clientCallRe.FindAllStringSubmatch(prompt, -1) {
		op, arg := m[1], strings.TrimSpace(m[2])
		switch op {
		case "read":
			res, errMsg := a.callErr("fs/read_text_file", map[string]any{"sessionId": session, "path": abs(arg)})
			if errMsg != "" {
				a.chunk(session, "(read refused: "+errMsg+") ")
				continue
			}
			var out struct {
				Content string `json:"content"`
			}
			_ = json.Unmarshal(res, &out)
			a.chunk(session, fmt.Sprintf("(read ok: %d bytes) ", len(out.Content)))
		case "write":
			_, errMsg := a.callErr("fs/write_text_file", map[string]any{"sessionId": session, "path": abs(arg), "content": "written by acptest\n"})
			if errMsg != "" {
				a.chunk(session, "(write refused: "+errMsg+") ")
				continue
			}
			a.chunk(session, "(write ok) ")
		case "run":
			res, errMsg := a.callErr("terminal/create", map[string]any{"sessionId": session, "command": arg})
			if errMsg != "" {
				a.chunk(session, "(run refused: "+errMsg+") ")
				continue
			}
			var t struct {
				TerminalID string `json:"terminalId"`
			}
			_ = json.Unmarshal(res, &t)
			ref := map[string]string{"sessionId": session, "terminalId": t.TerminalID}
			res, _ = a.callErr("terminal/wait_for_exit", ref)
			var st struct {
				ExitCode *int `json:"exitCode"`
			}
			_ = json.Unmarshal(res, &st)
			res, _ = a.callErr("terminal/output", ref)
			var out struct {
				Output string `json:"output"`
			}
			_ = json.Unmarshal(res, &out)
			_, _ = a.callErr("terminal/release", ref)
			code := -1
			if st.ExitCode != nil {
				code = *st.ExitCode
			}
			a.chunk(session, fmt.Sprintf("(run exit %d: %s) ", code, strings.TrimSpace(out.Output)))
		}
	}
}

func (a *Agent) chunk(session, text string) {
	a.notify("session/update", map[string]any{
		"sessionId": session,
		"update": map[string]any{
			"sessionUpdate": "agent_message_chunk",
			"content":       map[string]string{"type": "text", "text": text},
		},
	})
}

func (a *Agent) isCancelled(session string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cancelled[session]
}

func (a *Agent) handle(f frame, crash func()) {
	switch f.Method {
	case "initialize":
		a.respond(f.ID, map[string]any{
			"protocolVersion":   1,
			"agentCapabilities": map[string]any{"loadSession": false},
			"authMethods":       []any{},
		})
	case "session/new":
		var p struct {
			Cwd        string          `json:"cwd"`
			MCPServers json.RawMessage `json:"mcpServers"`
		}
		_ = json.Unmarshal(f.Params, &p)
		a.mu.Lock()
		a.sessions++
		id := "sess-" + strconv.Itoa(a.sessions)
		a.Cwds = append(a.Cwds, p.Cwd)
		a.cwds[id] = p.Cwd
		a.MCPServers = append(a.MCPServers, p.MCPServers)
		a.mu.Unlock()
		a.respond(f.ID, map[string]string{"sessionId": id})
	case "session/cancel":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(f.Params, &p)
		a.mu.Lock()
		a.cancelled[p.SessionID] = true
		a.mu.Unlock()
	case "session/prompt":
		a.prompt(f, crash)
	default:
		if len(f.ID) > 0 {
			raw, _ := json.Marshal(map[string]any{"code": -32601, "message": "method not found"})
			a.send(frame{ID: f.ID, Error: raw})
		}
	}
}

func (a *Agent) prompt(f frame, crash func()) {
	var p struct {
		SessionID string `json:"sessionId"`
		Prompt    []struct {
			Text string `json:"text"`
		} `json:"prompt"`
	}
	_ = json.Unmarshal(f.Params, &p)
	var text strings.Builder
	for _, b := range p.Prompt {
		text.WriteString(b.Text)
	}
	prompt := text.String()
	a.mu.Lock()
	a.Prompts = append(a.Prompts, prompt)
	a.cancelled[p.SessionID] = false
	a.mu.Unlock()

	if strings.Contains(prompt, "[crash]") {
		a.chunk(p.SessionID, "Starting to answer… ")
		crash()
		return
	}

	if strings.Contains(prompt, "[permission]") {
		res := a.call("session/request_permission", map[string]any{
			"sessionId": p.SessionID,
			"toolCall": map[string]any{
				"toolCallId": "call-1",
				"title":      "Read part-02.md",
				"kind":       "read",
			},
			"options": []map[string]string{
				{"optionId": "allow", "name": "Allow", "kind": "allow_once"},
				{"optionId": "deny", "name": "Deny", "kind": "reject_once"},
			},
		})
		var out struct {
			Outcome struct {
				Outcome  string `json:"outcome"`
				OptionID string `json:"optionId"`
			} `json:"outcome"`
		}
		_ = json.Unmarshal(res, &out)
		switch {
		case out.Outcome.Outcome == "cancelled":
			a.respond(f.ID, map[string]string{"stopReason": "cancelled"})
			return
		case out.Outcome.OptionID == "allow":
			a.chunk(p.SessionID, "(permission granted) ")
		default:
			a.chunk(p.SessionID, "(permission denied) ")
		}
	}

	a.clientCalls(p.SessionID, prompt)

	if strings.Contains(prompt, "[owntool]") {
		for _, status := range []string{"pending", "completed"} {
			upd := map[string]any{"toolCallId": "call-own", "status": status}
			if status == "pending" {
				upd["sessionUpdate"] = "tool_call"
				upd["title"] = "Read part-01.md"
				upd["kind"] = "read"
			} else {
				upd["sessionUpdate"] = "tool_call_update"
			}
			a.notify("session/update", map[string]any{"sessionId": p.SessionID, "update": upd})
		}
	}

	a.followPlan(p.SessionID)

	if strings.Contains(prompt, "[tool]") {
		a.chunk(p.SessionID, "Let me check part 2.")
		a.notify("session/update", map[string]any{
			"sessionId": p.SessionID,
			"update": map[string]any{
				"sessionUpdate": "tool_call",
				"toolCallId":    "call-read",
				"title":         "Read part-02.md",
				"kind":          "read",
				"status":        "pending",
			},
		})
	}

	reply := "You asked about this part."
	if a.Reply != nil {
		reply = a.Reply(prompt)
	}
	delay := a.ChunkDelay
	if strings.Contains(prompt, "[slow]") && delay < 50*time.Millisecond {
		delay = 50 * time.Millisecond
	}
	for _, c := range splitChunks(reply) {
		if a.isCancelled(p.SessionID) {
			a.respond(f.ID, map[string]string{"stopReason": "cancelled"})
			return
		}
		a.chunk(p.SessionID, c)
		if delay > 0 {
			time.Sleep(delay)
		}
	}
	a.respond(f.ID, map[string]string{"stopReason": "end_turn"})
}

func (a *Agent) followPlan(session string) {
	if len(a.Plan) == 0 {
		return
	}
	update := func(u map[string]any) {
		a.notify("session/update", map[string]any{"sessionId": session, "update": u})
	}
	sendPlan := func(done int) {
		entries := make([]map[string]string, len(a.Plan))
		for i, step := range a.Plan {
			status := "pending"
			switch {
			case i < done:
				status = "completed"
			case i == done:
				status = "in_progress"
			}
			entries[i] = map[string]string{"content": step, "priority": "medium", "status": status}
		}
		update(map[string]any{"sessionUpdate": "plan", "entries": entries})
	}
	for i, step := range a.Plan {
		sendPlan(i)
		id := "step-" + strconv.Itoa(i+1)
		update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": id, "title": step, "kind": "other", "status": "in_progress"})
		if a.StepDelay > 0 {
			time.Sleep(a.StepDelay)
		}
		if a.isCancelled(session) {
			return
		}
		update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": id, "status": "completed"})
	}
	sendPlan(len(a.Plan))
}

// splitChunks breaks s into word-ish pieces, the way agents stream tokens.
func splitChunks(s string) []string {
	var out []string
	for len(s) > 0 {
		i := strings.IndexAny(s[1:], " \n")
		if i < 0 {
			out = append(out, s)
			break
		}
		out = append(out, s[:i+1])
		s = s[i+1:]
	}
	return out
}
