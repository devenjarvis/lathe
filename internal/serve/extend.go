package serve

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/devenjarvis/lathe/internal/store"
)

const maxGuidanceBytes = 2 * 1024

// handleExtend no longer spawns a generator. Adding a part runs in the user's
// interactive coding-agent session via the /lathe-extend skill — the binary
// never drives a model itself. The button hands back the exact skill command,
// folding in any guidance the reader typed; the skill reserves the part
// (`lathe extend-start`), writes it, and records it (`lathe extend-commit`).
func (s *Server) handleExtend(w http.ResponseWriter, r *http.Request) {
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

	command := "/lathe-extend " + slug
	if g := strings.TrimSpace(payload.Guidance); g != "" {
		// Collapse newlines so the handoff stays a single pasteable line.
		command += " " + strings.Join(strings.Fields(g), " ")
	}
	writeHandoff(w, command)
}
