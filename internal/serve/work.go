package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/devenjarvis/lathe/internal/queue"
)

// workLongPoll is how long GET /-/work blocks waiting for a job before returning
// "no task". The worker CLI uses a slightly longer client timeout so the server
// always wins the race and answers with a clean 204 rather than the client
// timing out.
const workLongPoll = 50 * time.Second

// handleWorkNext is the worker's long-poll claim endpoint. It records worker
// presence (so the enqueue-vs-handoff branch knows a worker is live), then blocks
// up to workLongPoll for a queued job. It returns the claimed job as JSON, or 204
// No Content when the window lapses with nothing queued.
func (s *Server) handleWorkNext(w http.ResponseWriter, r *http.Request) {
	s.queue.MarkWorkerSeen()

	ctx, cancel := context.WithTimeout(r.Context(), workLongPoll)
	defer cancel()

	job, ok := s.queue.Claim(ctx)
	if !ok {
		// Nothing to do within the window — the worker re-polls immediately.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, job)
}

// handleWorkGet lets the browser poll an ask job for its answer. verify/extend
// jobs don't use this — they keep polling GET /-/status as before — but the
// endpoint serves any job id uniformly.
func (s *Server) handleWorkGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	job, ok := s.queue.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	resp := map[string]any{
		"id":     job.ID,
		"type":   job.Type,
		"state":  job.State,
		"answer": job.Answer,
	}
	// For an ask answer, also hand back rendered HTML so the drawer can show the
	// markdown the same way parts render — consistent with how the rest of the app
	// injects model-authored markdown.
	if job.Type == queue.JobAsk && job.Answer != "" {
		if html, err := RenderMarkdown([]byte(job.Answer)); err == nil {
			resp["answerHTML"] = string(html)
		}
	}
	writeJSON(w, resp)
}

// handleWorkAnswer records an ask job's answer (worker → browser) and closes the
// job. The worker is the CLI, which sends no Origin header, so sameOrigin allows
// it while still rejecting a cross-site POST.
func (s *Server) handleWorkAnswer(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id := r.PathValue("id")
	if _, ok := s.queue.Get(id); !ok {
		http.NotFound(w, r)
		return
	}
	var payload struct {
		Answer string `json:"answer"`
	}
	if !readJSONBody(w, r, maxAnswerBytes, &payload) {
		return
	}
	s.queue.SetAnswer(id, payload.Answer)
	s.queue.Done(id)
	w.WriteHeader(http.StatusNoContent)
}

// maxAnswerBytes caps the worker's answer body. Answers are prose; a generous
// cap still rules out abuse.
const maxAnswerBytes = 256 << 10 // 256 KiB

// handleWorkDone closes a verify/extend job once the worker has finished it (the
// real state was already written to disk by `lathe verify-result` /
// `lathe extend-commit`). The browser learns the outcome from GET /-/status.
func (s *Server) handleWorkDone(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id := r.PathValue("id")
	if _, ok := s.queue.Get(id); !ok {
		http.NotFound(w, r)
		return
	}
	s.queue.Done(id)
	w.WriteHeader(http.StatusNoContent)
}

// handleWorker reports whether a worker is currently connected, for the UI's
// "agent connected" indicator and to tune button copy.
func (s *Server) handleWorker(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]bool{"connected": s.queue.WorkerConnected()})
}

// writeJSON encodes v as a JSON response with the right content type.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}
