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
		Auto      bool     `json:"auto"`
	}
	if !readJSONBody(w, r, maxProgressBytes, &payload) {
		return
	}
	if payload.Ratio == nil {
		http.Error(w, "ratio is required", http.StatusBadRequest)
		return
	}

	incoming := &store.Progress{
		Part:      part,
		Ratio:     clampRatio(*payload.Ratio),
		HeadingID: strings.TrimSpace(payload.HeadingID),
		UpdatedAt: time.Now().UTC(),
	}

	// Cross-part monotonic guard: only auto-saves are prevented from
	// regressing progress. Manual saves (payload.Auto == false) are an
	// explicit user action and always write through.
	if payload.Auto {
		if existing, err := store.ReadProgress(tutDir); err == nil && existing != nil {
			existingIdx := partIndex(tut, existing.Part)
			incomingIdx := partIndex(tut, incoming.Part)
			if existingIdx >= 0 && incomingIdx >= 0 {
				if incomingIdx < existingIdx {
					// Viewing an earlier part — don't regress.
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(struct {
						Progress *store.Progress `json:"progress"`
					}{Progress: existing})
					return
				}
				if incomingIdx == existingIdx && incoming.Ratio <= existing.Ratio {
					// Same part but not advancing — don't regress.
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(struct {
						Progress *store.Progress `json:"progress"`
					}{Progress: existing})
					return
				}
			}
		}
	}

	if err := store.WriteProgress(tutDir, incoming); err != nil {
		http.Error(w, "write progress", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Progress *store.Progress `json:"progress"`
	}{Progress: incoming})
}
