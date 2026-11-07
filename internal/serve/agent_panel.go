package serve

import (
	"net/http"
	"os"

	"github.com/devenjarvis/lathe/internal/agents"
	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
)

// The agent panel's setup endpoints: the "Bring your agent" picker lists the
// built-in agents it finds on PATH (GET /-/agents) and starts the one the
// reader picks (POST /-/agent), remembering it in ~/.lathe/config.json; the
// workspace field records where the reader types the tutorial's code
// (POST /-/workspace/{slug}), in that tutorial's local workspace.json.
//
// Only registry agents can be started from the browser. A custom command
// stays a `lathe serve --agent <cmd>` choice, so a page can never make the
// server run a command line it supplies.

// agentChoice is the picked agent as the panel shows it.
type agentChoice struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name"`
	Command string `json:"command"`
}

// handleAgents lists the registry with what was found on this machine, plus
// the agent being hosted now.
func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	home, _ := os.UserHomeDir()
	resp := map[string]any{
		"agents":          agents.Detect(s.lookPath, home),
		"workerConnected": s.queue.WorkerConnected(),
	}
	if h := s.hostedAgent(); h != nil {
		resp["current"] = agentChoice{ID: h.id, Name: h.name, Command: h.command}
	}
	writeJSON(w, resp)
}

// handleAgentPick starts the picked registry agent in place of the current
// one and remembers the choice.
func (s *Server) handleAgentPick(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var payload struct {
		ID string `json:"id"`
	}
	if !readJSONBody(w, r, 1<<10, &payload) {
		return
	}
	a, ok := agents.ByID(payload.ID)
	if !ok {
		http.Error(w, "unknown agent", http.StatusBadRequest)
		return
	}

	s.switchMu.Lock()
	defer s.switchMu.Unlock()
	old := s.hostedAgent()
	if old != nil && old.busy() {
		http.Error(w, "The agent is still working. Wait for it to finish (or stop it) before switching.", http.StatusConflict)
		return
	}
	if err := config.SetAgent(a.ID); err != nil {
		http.Error(w, "could not save the choice: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if old == nil || old.command != a.Command {
		h := s.makeHost(a.Command)
		h.id = a.ID
		h.name = a.Name
		if old != nil {
			// Keep run ids unique across the swap, so a page still holding an
			// old run id can never attach to a new run.
			h.counter = old.lastCounter()
		}
		s.setAgentHost(h)
		if old != nil {
			go old.close()
		}
		go func() { _, _ = h.ensure() }()
	} else {
		old.id, old.name = a.ID, a.Name
	}
	writeJSON(w, map[string]any{"agent": s.hostedAgent().status()})
}

// handleAgentRestart stops the hosted agent and starts the same command
// afresh (the panel's "Restart agent"), keeping its registry id, name and
// run-id counter. Like a switch, it is refused while a run is active.
func (s *Server) handleAgentRestart(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.switchMu.Lock()
	defer s.switchMu.Unlock()
	old := s.hostedAgent()
	if old == nil {
		http.Error(w, "No agent is running here to restart.", http.StatusConflict)
		return
	}
	if old.busy() {
		http.Error(w, "The agent is still working. Wait for it to finish (or stop it) before restarting.", http.StatusConflict)
		return
	}
	h := s.makeHost(old.command)
	h.id, h.name = old.id, old.name
	h.counter = old.lastCounter()
	s.setAgentHost(h)
	go old.close()
	go func() { _, _ = h.ensure() }()
	writeJSON(w, map[string]any{"agent": h.status()})
}

// panelSuggestions reports whether the agent panel offers its one context
// suggestion ("Want feedback on your code? Check my work"). It is the page's
// .PanelSuggestions: on by default; the Settings window's "suggestions in
// the panel" key (config panel_suggestions) turns it off.
func (s *Server) panelSuggestions() bool {
	return !s.noPanelSuggestions && config.LoadSettings().PanelSuggestions
}

// handleWorkspace records the reader's code folder for a tutorial, creating
// it (and running git init) when needed.
func (s *Server) handleWorkspace(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	tutDir, ok := s.safeTutorialPath(r.PathValue("slug"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	if _, err := store.ReadMetadata(tutDir); err != nil {
		http.NotFound(w, r)
		return
	}
	var payload struct {
		Path string `json:"path"`
	}
	if !readJSONBody(w, r, 4<<10, &payload) {
		return
	}
	setup, err := store.SetWorkspace(tutDir, payload.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{
		"path":    setup.Path,
		"display": displayPath(setup.Path),
		"created": setup.Created,
		"gitInit": setup.GitInit,
	})
}

// busy reports whether any ask or job is running on this host.
func (h *agentHost) busy() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.active) > 0
}

func (h *agentHost) lastCounter() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.counter
}

// workspaceDisplay is the tutorial's workspace for the panel footer: the
// full path and the ~-abbreviated one ("" when none is set).
func workspaceDisplay(tutDir string) (full, short string) {
	ws, err := store.ReadWorkspace(tutDir)
	if err != nil || ws == nil {
		return "", ""
	}
	return ws.Path, displayPath(ws.Path)
}
