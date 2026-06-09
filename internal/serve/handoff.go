package serve

import (
	"encoding/json"
	"net/http"
)

// writeHandoff replies with the slash-command the user should paste into their
// interactive coding-agent session. All of Lathe's model work (verify, extend,
// ask) runs there — the binary never drives a model itself — so the web UI's
// job is to hand off the exact command rather than to spawn anything.
func writeHandoff(w http.ResponseWriter, command string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"command": command}) //nolint:errcheck
}
