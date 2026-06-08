package serve

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/devenjarvis/lathe/internal/store"
)

const maxCheckpointBytes = 1024

func (s *Server) handleCheckpoint(w http.ResponseWriter, r *http.Request) {
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

	r.Body = http.MaxBytesReader(w, r.Body, maxCheckpointBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "request body too large", http.StatusBadRequest)
		return
	}
	if len(raw) == 0 {
		http.Error(w, "empty request body", http.StatusBadRequest)
		return
	}

	var payload struct {
		Progress  float64 `json:"progress"`
		HeadingID string  `json:"heading_id"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	progress := payload.Progress
	if progress < 0 {
		progress = 0
	}
	if progress > 1 {
		progress = 1
	}

	tut.Checkpoint = &store.Checkpoint{
		Part:      part,
		Progress:  progress,
		HeadingID: strings.TrimSpace(payload.HeadingID),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		http.Error(w, "write metadata", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Checkpoint *store.Checkpoint `json:"checkpoint"`
	}{Checkpoint: tut.Checkpoint})
}
