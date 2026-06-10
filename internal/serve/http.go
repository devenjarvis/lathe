package serve

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// readJSONBody caps the request body at max bytes, reads it, requires it to be
// non-empty, and unmarshals it into dst. It writes an appropriate error
// response and returns false on any failure (so callers can `if
// !readJSONBody(...) { return }`); it returns true only when dst is populated.
//
// A genuinely oversize body (the MaxBytesReader limit was hit) gets a 413; any
// other read failure (e.g. a client disconnect) gets a generic 400 — so a
// network blip no longer surfaces as a misleading "request body too large".
func readJSONBody(w http.ResponseWriter, r *http.Request, max int64, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, max)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "could not read request", http.StatusBadRequest)
		}
		return false
	}
	if len(raw) == 0 {
		http.Error(w, "empty request body", http.StatusBadRequest)
		return false
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return false
	}
	return true
}

// clampRatio constrains a scroll ratio to the inclusive 0..1 range.
func clampRatio(r float64) float64 {
	if r < 0 {
		return 0
	}
	if r > 1 {
		return 1
	}
	return r
}
