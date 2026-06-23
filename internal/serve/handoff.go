package serve

import (
	"encoding/json"
	"net/http"
)

// writeHandoff replies with the slash-command the user should paste into their
// interactive coding-agent session. It is the fallback branch: when no worker is
// connected, the ask/verify/extend buttons hand off the exact command rather
// than enqueue a job. mode="handoff" lets the browser tell the two branches
// apart. Model work still runs only in the interactive session — the binary
// never drives a model itself.
func writeHandoff(w http.ResponseWriter, command string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"mode": "handoff", "command": command}) //nolint:errcheck
}

// writeQueued replies that the request was enqueued for a connected worker. The
// browser branches on mode: verify/extend just start the existing status poll;
// ask polls GET /-/work/{jobId} for the answer.
func writeQueued(w http.ResponseWriter, jobID string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"mode": "queued", "jobId": jobID}) //nolint:errcheck
}
