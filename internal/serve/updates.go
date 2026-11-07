package serve

import (
	"net/http"
	"sync"

	"github.com/devenjarvis/lathe/internal/update"
)

// The update notice (#149): a quiet note in the title bar when a newer Lathe
// is out ("Lathe 0.9 is available · Download", or the Homebrew command), or
// when the app on disk was already upgraded ("Lathe 0.9 is ready · Restart").
// The desktop app installs its update checker with SetUpdates; plain
// `lathe serve` has none, so its pages never show the notice.
//
//	POST /-/native/update {action: "dismiss"|"restart", version}  → 204 / 202
//
// The notice is read when a page renders (the updateNotice template func), so
// it never waits on the network: the checker answers from its cache.

// Updates is what the desktop shell's update checker offers the pages.
type Updates interface {
	// Notice is the title-bar notice, nil when there is nothing to say. It
	// must not touch the network (pages call it while rendering).
	Notice() *update.Notice
	// Dismiss hides the notice for version until a newer one.
	Dismiss(version string) error
	// Restart quits the app and opens the upgraded one (a "ready" notice).
	Restart() error
}

// updateSlot holds the installed Updates. It exists before the Server so the
// template func map (built first) can reach it.
type updateSlot struct {
	mu sync.RWMutex
	u  Updates
}

func (s *updateSlot) get() Updates {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.u
}

// notice is the updateNotice template func.
func (s *updateSlot) notice() *update.Notice {
	if u := s.get(); u != nil {
		return u.Notice()
	}
	return nil
}

// SetUpdates installs the desktop app's update checker; nil removes it.
func (s *Server) SetUpdates(u Updates) {
	s.updates.mu.Lock()
	s.updates.u = u
	s.updates.mu.Unlock()
}

// handleNativeUpdate dismisses the notice or restarts into the upgraded app.
func (s *Server) handleNativeUpdate(w http.ResponseWriter, r *http.Request) {
	u := s.updates.get()
	if s.nativeShell() == nil || u == nil {
		http.NotFound(w, r)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var payload struct {
		Action  string `json:"action"`
		Version string `json:"version"`
	}
	if !readJSONBody(w, r, maxNativeBody, &payload) {
		return
	}
	switch payload.Action {
	case "dismiss":
		if err := u.Dismiss(payload.Version); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case "restart":
		// Only when the upgraded app is really there: a page can't make the
		// app quit otherwise.
		if n := u.Notice(); n == nil || n.Kind != update.KindRestart {
			http.Error(w, "there is no upgraded Lathe to restart into", http.StatusConflict)
			return
		}
		if err := u.Restart(); err != nil {
			http.Error(w, "could not restart: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	default:
		http.Error(w, "action must be dismiss or restart", http.StatusBadRequest)
	}
}
