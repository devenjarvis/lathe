package serve

import (
	"encoding/json"
	"io"
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

	r.Body = http.MaxBytesReader(w, r.Body, maxProgressBytes)
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
		Ratio     *float64 `json:"ratio"`
		Progress  *float64 `json:"progress"`
		HeadingID string   `json:"heading_id"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if payload.Ratio == nil && payload.Progress == nil {
		http.Error(w, "ratio is required", http.StatusBadRequest)
		return
	}

	var ratio float64
	if payload.Ratio != nil {
		ratio = *payload.Ratio
	} else if payload.Progress != nil {
		ratio = *payload.Progress
	}
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}

	progress := &store.Progress{
		Part:      part,
		Ratio:     ratio,
		HeadingID: strings.TrimSpace(payload.HeadingID),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.SaveProgress(tutDir, progress); err != nil {
		http.Error(w, "write progress", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Progress *store.Progress `json:"progress"`
	}{Progress: progress})
}
