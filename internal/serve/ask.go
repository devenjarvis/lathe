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

// handleAsk hands the reader the command to paste into their interactive
// coding-agent session, where the /lathe-ask skill answers questions about the
// part they're reading. The binary never drives a model itself; routing
// through the interactive session keeps the work on the user's subscription and
// preserves the skill's read-only access to sibling parts. The handler still
// validates the slug/part/question so the drawer can surface a clean error
// before the user copies anything.
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

	if s.queue.WorkerConnected() {
		id := s.queue.Enqueue(queue.Job{Type: queue.JobAsk, Slug: slug, Part: part, Question: question})
		writeQueued(w, id)
		return
	}

	// A single pasteable block: the skill invocation on the first line, then the
	// reader's question so it carries over into the session verbatim.
	writeHandoff(w, "/lathe-ask "+slug+" "+part+"\n"+question)
}
