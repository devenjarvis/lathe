package serve

import (
	"net/http"

	"github.com/devenjarvis/lathe/internal/queue"
	"github.com/devenjarvis/lathe/internal/store"
)

// handleVerify never runs a verifier itself — the binary never drives a model.
// With a hosted agent (lathe serve --agent) it starts the /lathe-verify skill
// there over ACP; else, when a /lathe-work worker is connected, it enqueues a
// verify job for that interactive session to claim; otherwise it hands the
// user the exact skill command to paste. Either way the skill sets
// status=verifying and reports the result via `lathe verify-result` (or its
// MCP tool).
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

	// Order of preference: the agent lathe serve hosts (progress streams into
	// the page), then a connected /lathe-work worker, then the handoff.
	command := "/lathe-verify " + slug
	handled, notice := s.jobViaAgent(w, jobVerify, tutDir, slug, command)
	if handled {
		return
	}
	if s.queue.WorkerConnected() {
		id := s.queue.Enqueue(queue.Job{Type: queue.JobVerify, Slug: slug})
		writeQueued(w, id)
		return
	}
	if notice != "" {
		writeHandoffNotice(w, command, notice)
		return
	}
	writeHandoff(w, command)
}
