package serve

import (
	"net/http"
	"os"
	"strings"

	"github.com/devenjarvis/lathe/internal/queue"
	"github.com/devenjarvis/lathe/internal/store"
)

// maxQuestionBytes caps the JSON request body for /-/ask. Questions are
// expected to be short prose; anything bigger is almost certainly abuse.
const maxQuestionBytes = 8 << 10 // 8 KiB

// handleAsk routes a reader's question about the part they're reading to the
// /lathe-ask skill running in the user's own agent: the agent `lathe serve
// --agent` hosts over ACP when there is one, else a /lathe-work worker, else a
// command to paste into their session. The binary never calls a model API
// itself. The handler validates the slug/part/question first so the drawer can
// surface a clean error before anything runs or is copied.
func (s *Server) handleAsk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	slug := r.PathValue("slug")
	part := r.PathValue("part")

	// Defense in depth: only .md files are valid parts.
	if !strings.HasSuffix(part, ".md") {
		http.NotFound(w, r)
		return
	}

	tutDir, ok := s.safeTutorialPath(slug)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if _, err := store.ReadMetadata(tutDir); err != nil {
		http.NotFound(w, r)
		return
	}

	partPath, ok := s.safeTutorialPath(slug, part)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if _, err := os.Stat(partPath); err != nil {
		http.NotFound(w, r)
		return
	}

	var payload struct {
		Question string `json:"question"`
	}
	if !readJSONBody(w, r, maxQuestionBytes, &payload) {
		return
	}
	question := strings.TrimSpace(payload.Question)
	if question == "" {
		http.Error(w, "question is required", http.StatusBadRequest)
		return
	}

	// Order of preference: the agent lathe serve hosts (streams into the
	// drawer), then a connected /lathe-work worker, then the paste-able handoff.
	if s.askViaAgent(w, tutDir, slug, part, question) {
		return
	}
	if s.queue.WorkerConnected() {
		id := s.queue.Enqueue(queue.Job{Type: queue.JobAsk, Slug: slug, Part: part, Question: question})
		writeQueued(w, id)
		return
	}

	// A single pasteable block: the skill invocation on the first line, then the
	// reader's question so it carries over into the session verbatim.
	writeHandoff(w, "/lathe-ask "+slug+" "+part+"\n"+question)
}
