package serve

import (
	"net/http"

	"github.com/devenjarvis/lathe/internal/store"
)

// handleVerify no longer spawns a verifier. Verification runs in the user's
// interactive coding-agent session — the binary never drives a model itself.
// The button hands back the exact skill command for the user to paste; the
// /lathe-verify skill sets status=verifying and reports the result via
// `lathe verify-result`.
func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
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

	writeHandoff(w, "/lathe-verify "+slug)
}
