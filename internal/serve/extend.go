package serve

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/devenjarvis/lathe/internal/queue"
	"github.com/devenjarvis/lathe/internal/store"
)

const maxGuidanceBytes = 2 * 1024

// handleExtend never spawns a generator — the binary never drives a model
// itself. When a /lathe-work worker is connected it enqueues an extend job
// (folding in any guidance the reader typed) for that interactive session to
// claim; otherwise it falls back to handing back the exact skill command. Either
// way the /lathe-extend protocol runs in the interactive session, which reserves
// the part (`lathe extend-start`), writes it, and records it
// (`lathe extend-commit`).
func (s *Server) handleExtend(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	slug := r.PathValue("slug")
	tutDir, ok := s.safeTutorialPath(slug)
	if !ok {
		http.NotFound(w, r)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxGuidanceBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "request body too large", http.StatusBadRequest)
		return
	}

	var payload struct {
		Guidance string `json:"guidance"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &payload); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
	}

	tut, err := store.ReadMetadata(tutDir)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if tut.Status == store.StatusExtending || tut.Status == store.StatusVerifying {
		http.Error(w, "conflict: tutorial is already extending or verifying", http.StatusConflict)
		return
	}

	guidance := strings.Join(strings.Fields(strings.TrimSpace(payload.Guidance)), " ")

	if s.queue.WorkerConnected() {
		id := s.queue.Enqueue(queue.Job{Type: queue.JobExtend, Slug: slug, Guidance: guidance})
		writeQueued(w, id)
		return
	}

	command := "/lathe-extend " + slug
	if guidance != "" {
		// Collapse newlines so the handoff stays a single pasteable line.
		command += " " + guidance
	}
	writeHandoff(w, command)
}
