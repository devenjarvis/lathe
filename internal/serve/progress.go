package serve

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/devenjarvis/lathe/internal/store"
)

const maxProgressBytes = 1024

func (s *Server) handleProgress(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	slug := r.PathValue("slug")
	part := r.PathValue("part")
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
	if !isKnownPart(tut, part) {
		http.NotFound(w, r)
		return
	}

	var payload struct {
		Ratio     *float64 `json:"ratio"`
		HeadingID string   `json:"heading_id"`
	}
	if !readJSONBody(w, r, maxProgressBytes, &payload) {
		return
	}
	if payload.Ratio == nil {
		http.Error(w, "ratio is required", http.StatusBadRequest)
		return
	}

	progress := &store.Progress{
		Part:      part,
		Ratio:     clampRatio(*payload.Ratio),
		HeadingID: strings.TrimSpace(payload.HeadingID),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.WriteProgress(tutDir, progress); err != nil {
		http.Error(w, "write progress", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Progress *store.Progress `json:"progress"`
	}{Progress: progress})
}
