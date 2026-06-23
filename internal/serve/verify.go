package serve

import (
	"net/http"

	"github.com/devenjarvis/lathe/internal/queue"
	"github.com/devenjarvis/lathe/internal/store"
)

// handleVerify never spawns a verifier — the binary never drives a model itself.
// When a /lathe-work worker is connected it enqueues a verify job for that
// interactive session to claim; otherwise it falls back to handing the user the
// exact skill command to paste. Either way verification runs in the interactive
// session, which sets status=verifying and reports the result via
// `lathe verify-result`.
func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
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

	tut, err := store.ReadMetadata(tutDir)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if tut.Status == store.StatusExtending || tut.Status == store.StatusVerifying {
		http.Error(w, "conflict: tutorial is already extending or verifying", http.StatusConflict)
		return
	}

	if s.queue.WorkerConnected() {
		id := s.queue.Enqueue(queue.Job{Type: queue.JobVerify, Slug: slug})
		writeQueued(w, id)
		return
	}
	writeHandoff(w, "/lathe-verify "+slug)
}
