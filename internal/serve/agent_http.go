package serve

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// writeAgentRun replies that the ask is streaming from the hosted agent; the
// drawer opens GET /-/acp/{runId}/events.
func writeAgentRun(w http.ResponseWriter, runID string) {
	writeJSON(w, map[string]string{"mode": "agent", "runId": runID})
}

// writeHandoffNotice is writeHandoff plus a line explaining why the hosted
// agent could not take the question.
func writeHandoffNotice(w http.ResponseWriter, command, notice string) {
	writeJSON(w, map[string]string{"mode": "handoff", "command": command, "notice": notice})
}

// handleAgentEvents streams a run's events as server-sent events. Each event
// carries its index as the SSE id, so a reconnecting EventSource resumes from
// Last-Event-ID instead of replaying the answer. Once the run has finished and
// everything is sent, the stream ends; a reconnect after that gets 204, which
// tells EventSource to stop.
func (s *Server) handleAgentEvents(w http.ResponseWriter, r *http.Request) {
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
	next := 0
	if last := r.Header.Get("Last-Event-ID"); last != "" {
		if n, err := strconv.Atoi(last); err == nil && n >= 0 {
			next = n + 1
		}
	}
	if evs, finished, _ := run.since(next); finished && len(evs) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	flusher, _ := w.(http.Flusher)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	keepAlive := time.NewTicker(20 * time.Second)
	defer keepAlive.Stop()
	for {
		evs, finished, changed := run.since(next)
		for _, ev := range evs {
			raw, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", next, raw); err != nil {
				return
			}
			next++
		}
		if flusher != nil {
			flusher.Flush()
		}
		if finished {
			return
		}
		select {
		case <-changed:
		case <-keepAlive.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

// handleAgentPermission answers a permission prompt from the drawer.
func (s *Server) handleAgentPermission(w http.ResponseWriter, r *http.Request) {
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
		PermissionID string `json:"permissionId"`
		OptionID     string `json:"optionId"`
	}
	if !readJSONBody(w, r, 4<<10, &payload) {
		return
	}
	if payload.PermissionID == "" || payload.OptionID == "" {
		http.Error(w, "permissionId and optionId are required", http.StatusBadRequest)
		return
	}
	if !run.answerPermission(payload.PermissionID, payload.OptionID) {
		http.Error(w, "that permission request is no longer waiting", http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAgentCancel stops a streaming answer (sends session/cancel).
func (s *Server) handleAgentCancel(w http.ResponseWriter, r *http.Request) {
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
	run.cancel()
	w.WriteHeader(http.StatusNoContent)
}

// askViaAgent routes a question to the hosted agent. It returns false (having
// written nothing) only when no agent is configured; every agent failure ends
// in the copy-paste handoff with a notice, so the reader is never stuck.
func (s *Server) askViaAgent(w http.ResponseWriter, tutDir, slug, part, question string) bool {
	agent := s.hostedAgent()
	if agent == nil {
		return false
	}
	run, err := agent.startAsk(tutDir, slug, part, question)
	if err != nil {
		if errors.Is(err, errAskBusy) {
			http.Error(w, "Wait for the current answer to finish (or stop it) before asking again.", http.StatusConflict)
			return true
		}
		writeHandoffNotice(w, "/lathe-ask "+slug+" "+part+"\n"+question,
			"The agent ("+agent.command+") could not take this question: "+err.Error())
		return true
	}
	writeAgentRun(w, run.id)
	return true
}
