package acp

// Isolation says whether the agent's file and command access actually goes
// through Lathe's policy.
//
// Advertising fs/terminal capabilities lets an agent route reads, writes and
// commands through Lathe, but does not make it: an agent may keep using its
// own built-in tools, which Lathe never sees. So Lathe watches what happens.
// Tool calls the agent reports (kind read/edit/delete/move for files, execute
// for commands) are compared with the fs/* and terminal/* requests Lathe got.
type Isolation struct {
	// State is "enforced" (the agent routes through Lathe's policy), "soft"
	// (it has used its own file or command tools instead) or "unknown" (it
	// has not touched files or run commands yet).
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

type isolationCounts struct {
	fsCalls, termCalls int // client requests received (allowed or not)
	ownFs, ownExec     int // completed agent tool calls of those kinds
	toolKinds          map[string]string
}

func (c *Client) countClientCall(terminal bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if terminal {
		c.iso.termCalls++
	} else {
		c.iso.fsCalls++
	}
}

// observeToolCall counts tool calls the agent reports finishing, by kind.
func (c *Client) observeToolCall(u SessionUpdate) {
	if u.ToolCallID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.iso.toolKinds == nil {
		c.iso.toolKinds = map[string]string{}
	}
	if u.ToolKind != "" {
		c.iso.toolKinds[u.ToolCallID] = u.ToolKind
	}
	if u.Status != "completed" {
		return
	}
	kind := c.iso.toolKinds[u.ToolCallID]
	delete(c.iso.toolKinds, u.ToolCallID)
	switch kind {
	case "read", "edit", "delete", "move":
		c.iso.ownFs++
	case "execute":
		c.iso.ownExec++
	}
}

// Isolation reports what Lathe has seen of the agent's file/command access
// since it started.
func (c *Client) Isolation() Isolation {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := c.iso
	switch {
	case n.ownFs > 0 && n.fsCalls == 0 && n.ownExec > 0 && n.termCalls == 0:
		return Isolation{State: "soft", Detail: "The agent reads files and runs commands with its own tools, so Lathe's per-job policy is not enforced; its own permission prompts are the boundary."}
	case n.ownFs > 0 && n.fsCalls == 0:
		return Isolation{State: "soft", Detail: "The agent reads and writes files with its own tools, so Lathe's per-job file policy is not enforced; its own permission prompts are the boundary."}
	case n.ownExec > 0 && n.termCalls == 0:
		return Isolation{State: "soft", Detail: "The agent runs commands with its own tools, so Lathe's per-job command policy is not enforced; its own permission prompts are the boundary."}
	case n.fsCalls+n.termCalls > 0:
		return Isolation{State: "enforced", Detail: "The agent reads, writes and runs commands through Lathe, which limits each job to its own directories."}
	default:
		return Isolation{State: "unknown", Detail: "The agent has not touched any files yet."}
	}
}
