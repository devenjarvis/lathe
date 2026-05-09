package serve

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/devenjarvis/lathe/internal/store"
)

// maxQuestionBytes caps the JSON request body for /-/ask. Questions are
// expected to be short prose; anything bigger is almost certainly abuse.
const maxQuestionBytes = 8 << 10 // 8 KiB

// stderrCap bounds how much subprocess stderr we retain for the failure
// path. Large enough to capture a useful diagnostic, small enough to keep
// memory pressure bounded if `claude` becomes chatty.
const stderrCap = 4 << 10 // 4 KiB

// handleAsk streams an answer to a question about the part the user is
// currently reading. It spawns a tightly-scoped read-only `claude` subprocess
// (via exec.CommandContext bound to r.Context() so disconnect kills it) and
// re-streams the assistant text deltas to the browser as Server-Sent Events.
//
// SSE wire format:
//
//	data: <chunk>\n\n             — answer text chunks
//	event: done\ndata: {}\n\n     — clean completion
//	event: error\ndata: <msg>\n\n — subprocess or stream error
func (s *Server) handleAsk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	slug := r.PathValue("slug")
	part := r.PathValue("part")

	// Defense in depth: only .md files are valid parts.
	if !strings.HasSuffix(part, ".md") {
		http.NotFound(w, r)
		return
	}

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

	partPath, ok := s.safeTutorialPath(slug, part)
	if !ok {
		http.NotFound(w, r)
		return
	}
	articleBody, err := os.ReadFile(partPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Cap the request body. http.MaxBytesReader returns an error from Read once
	// the limit is exceeded; the json decoder surfaces that as a decode error.
	r.Body = http.MaxBytesReader(w, r.Body, maxQuestionBytes)
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
		Question string `json:"question"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	question := strings.TrimSpace(payload.Question)
	if question == "" {
		http.Error(w, "question is required", http.StatusBadRequest)
		return
	}

	system, user := buildAskPrompt(tut, part, string(articleBody), question)

	// Build the subprocess. Bind to the request context so client disconnect
	// kills the subprocess for free — no goroutines needed.
	cmd := exec.CommandContext(r.Context(), "claude",
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--add-dir", tutDir,
		"--allowedTools", "Read,Glob,Grep",
		"--dangerously-skip-permissions",
		"--system-prompt", system,
		user,
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		http.Error(w, "stream init failed", http.StatusInternalServerError)
		return
	}
	// Capture stderr into a bounded sink. A plain bytes.Buffer drains
	// continuously (so the subprocess can't deadlock on a full OS pipe) but
	// we cap total retained bytes so a chatty subprocess can't pressure
	// memory. On terminal failure we surface the tail in the SSE error frame.
	stderrBuf := &cappedBuffer{cap: stderrCap}
	cmd.Stderr = stderrBuf

	// SSE headers must go out before any frame is written. After the first
	// write, we cannot change status — errors after this must be SSE-framed.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	if err := cmd.Start(); err != nil {
		writeSSEError(w, flusher, "subprocess start failed")
		return
	}

	scanner := bufio.NewScanner(stdout)
	// Partial-message JSON events from claude can exceed the default 64KB
	// scanner buffer when they contain large code blocks; bump to 1MB.
	scanner.Buffer(make([]byte, 1<<20), 1<<20)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var obj any
		if err := json.Unmarshal(line, &obj); err != nil {
			// Non-JSON output (logs, banners) is harmless to skip.
			continue
		}
		text := extractTextDelta(obj)
		if text == "" {
			continue
		}
		writeSSEData(w, flusher, text)
	}

	// Drain stdout fully before Wait returns; reading the err afterward.
	waitErr := cmd.Wait()
	if waitErr != nil {
		msg := sanitizeStderr(stderrBuf.String())
		if msg == "" {
			msg = "subprocess error"
		}
		writeSSEError(w, flusher, msg)
		return
	}
	fmt.Fprint(w, "event: done\ndata: {}\n\n")
	flusher.Flush()
}

// writeSSEData emits a `data:` SSE frame. The SSE spec requires that newlines
// in the payload be sent as separate `data:` lines; we honor that so that
// multi-line model output renders correctly on the client.
func writeSSEData(w io.Writer, flusher http.Flusher, text string) {
	for _, line := range strings.Split(text, "\n") {
		fmt.Fprintf(w, "data: %s\n", line)
	}
	fmt.Fprint(w, "\n")
	flusher.Flush()
}

// writeSSEError emits an `error` SSE event. The message is intentionally short
// — we don't pipe stderr here, since it could be large and may leak details.
func writeSSEError(w io.Writer, flusher http.Flusher, msg string) {
	fmt.Fprintf(w, "event: error\ndata: %s\n\n", msg)
	flusher.Flush()
}

// cappedBuffer is an io.Writer that accumulates up to `cap` bytes and then
// silently drops the rest. It always reports the full input as written so
// the subprocess never sees backpressure (which would risk a deadlock on a
// full OS pipe).
type cappedBuffer struct {
	buf bytes.Buffer
	cap int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	remaining := c.cap - c.buf.Len()
	if remaining <= 0 {
		return len(p), nil
	}
	if len(p) > remaining {
		c.buf.Write(p[:remaining])
		return len(p), nil
	}
	return c.buf.Write(p)
}

func (c *cappedBuffer) String() string { return c.buf.String() }

// sanitizeStderr extracts the most useful single-line diagnostic from a
// captured stderr blob. We prefer the last non-empty line (errors typically
// land at the end), strip CR/LF so the SSE `data:` framing stays valid, and
// truncate to keep the wire frame bounded.
func sanitizeStderr(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	var last string
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			last = t
		}
	}
	if last == "" {
		return ""
	}
	if len(last) > 256 {
		last = last[:256]
	}
	return last
}

// extractTextDelta walks the JSON envelope of a claude --output-format
// stream-json line and returns any assistant text it carries. It only handles
// content_block_delta partials:
//
//	{"type":"content_block_delta","delta":{"type":"text_delta","text":"..."}}
//	(optionally wrapped in {"type":"stream_event","event":{...}})
//
// We deliberately ignore the final {"type":"assistant",...} envelope: with
// --include-partial-messages, that frame contains the *complete* assistant
// text, which is the concatenation of every partial we already streamed. If
// we returned text from it, the client would see the answer twice. The
// assistant frame is informational only.
//
// Anything else (tool_use, tool_result, message_start/stop, ping, system
// banners) returns "" and is skipped by the caller.
func extractTextDelta(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}

	// Unwrap stream_event envelope.
	if t, _ := m["type"].(string); t == "stream_event" {
		if inner, ok := m["event"].(map[string]any); ok {
			return extractTextDelta(inner)
		}
	}

	// content_block_delta with text_delta.
	if t, _ := m["type"].(string); t == "content_block_delta" {
		if delta, ok := m["delta"].(map[string]any); ok {
			if dt, _ := delta["type"].(string); dt == "text_delta" {
				if s, ok := delta["text"].(string); ok {
					return s
				}
			}
		}
		return ""
	}

	return ""
}

// buildAskPrompt produces the (system, user) prompt pair sent to the claude
// subprocess. The system prompt embeds the full text of the part the user is
// currently reading, plus — for series — a list of sibling parts the model
// can consult via Read/Glob/Grep. The user prompt is the question verbatim.
func buildAskPrompt(tut *store.Tutorial, part, articleBody, question string) (system, user string) {
	var b strings.Builder

	title := ""
	if tut != nil {
		title = tut.Title
	}

	b.WriteString("You are a helpful assistant answering questions about a hands-on technical tutorial.\n\n")
	fmt.Fprintf(&b, "The tutorial is titled %q.\n", title)
	fmt.Fprintf(&b, "The user is currently reading the part %q.\n\n", part)
	fmt.Fprintf(&b, "The full text of %q is included below.\n\n", part)

	if tut != nil && tut.Series && len(tut.Parts) > 1 {
		// Build the list of *other* parts so the model knows what it can
		// consult via Read/Glob/Grep.
		var siblings []string
		for _, p := range tut.Parts {
			if p == part {
				continue
			}
			siblings = append(siblings, p)
		}
		if len(siblings) > 0 {
			b.WriteString("This tutorial is a series. Other parts are also available in the same directory and you have read-only Read/Glob/Grep access if you need to consult them:\n")
			for _, p := range siblings {
				fmt.Fprintf(&b, "  - %s\n", p)
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("Answer the user's question concisely and accurately, citing specific parts of the tutorial when relevant. Do not write or modify any files.\n\n")
	fmt.Fprintf(&b, "--- BEGIN %s ---\n", part)
	b.WriteString(articleBody)
	if !strings.HasSuffix(articleBody, "\n") {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "--- END %s ---\n", part)

	return b.String(), question
}
