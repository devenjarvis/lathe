package serve

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/devenjarvis/lathe/internal/drift"
	"github.com/devenjarvis/lathe/internal/store"
)

// handleDrift runs a drift check synchronously, in-process, and returns the
// result.
//
// It is the one POST endpoint that neither enqueues a job nor hands back a
// paste-able command, and that is the point: drift is a pure git computation
// with no model in it, so there is nothing for a /lathe-work session to do. It
// works with no worker connected — that property is what makes the button
// honest, and it is asserted in drift_test.go so nobody "fixes" it into the
// queue later.
//
// It is also the only endpoint that writes a status without a skill. That is
// safe for exactly the same reason: no model runs, the transition is a pure
// function of the git diff, and it never touches the verifying/extending
// in-flight markers the skills own.
func (s *Server) handleDrift(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	slug := r.PathValue("slug")
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
	if !tut.IsOnboarding() || tut.RepoCommit == "" {
		http.Error(w, "drift only applies to onboarding guides with a pinned commit", http.StatusBadRequest)
		return
	}

	result, transition, err := store.RunDriftCheck(tutDir, tut, "")
	if err != nil {
		// An unknowable answer is not a server error and must not read as drift:
		// say so plainly and leave status and drift.json untouched.
		if errors.Is(err, drift.ErrUnknownPin) || errors.Is(err, drift.ErrNoCommonHistory) {
			writeDriftJSON(w, http.StatusOK, map[string]any{
				"mode":    "unknown",
				"message": err.Error(),
			})
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeDriftJSON(w, http.StatusOK, map[string]any{
		"mode":       "drift",
		"status":     tut.Status,
		"stale":      result.Stale(),
		"summary":    result.SummaryLine(),
		"transition": transition,
		// The re-rendered region, so the click updates the page immediately
		// instead of waiting up to 5s for the status poller (which only reacts to
		// a *change* — a clean re-check would otherwise look like nothing
		// happened).
		"section": s.renderPartial("driftSection", driftSectionData(tut, tutDir)),
	})
}

func writeDriftJSON(w http.ResponseWriter, code int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

// driftSectionData assembles the data the driftSection partial needs. It reads
// drift.json best-effort, mirroring how verify-result.json is read: a guide that
// has never been checked simply renders the "never checked" form.
func driftSectionData(tut *store.Tutorial, tutDir string) map[string]any {
	result, checkedDate := driftMeta(tut, tutDir)
	verifyResult, verifiedDate := verifyMeta(tut, tutDir)
	return map[string]any{
		"Tutorial":         tut,
		"Drift":            result,
		"DriftCheckedDate": checkedDate,
		"VerifyResult":     verifyResult,
		"VerifiedDate":     verifiedDate,
	}
}

// driftMeta returns the last recorded drift check and its friendly date. Both
// are best-effort: a missing, corrupt, or unparseable drift.json yields a nil
// result and an empty date, and the section renders as never-checked.
func driftMeta(tut *store.Tutorial, tutDir string) (*drift.Result, string) {
	if !tut.IsOnboarding() {
		return nil, ""
	}
	result, err := store.ReadDrift(tutDir)
	if err != nil {
		return nil, ""
	}
	var checked string
	if ts, err := time.Parse(time.RFC3339, result.CheckedAt); err == nil {
		checked = ts.Format("Jan 2, 2006")
	}
	return result, checked
}

// anchorOpenTag matches the container anchor.Rewrite emits, capturing the path
// and (when present) the line range, so a rendered part can be cross-referenced
// against a drift result.
var anchorOpenTag = regexp.MustCompile(`<div class="anchor" data-path="([^"]*)"(?: data-lines="(\d+)-(\d+)")?>`)

// markDriftedAnchors annotates the rendered HTML of one part with the verdict
// each anchored block earned, as data-drift="<verdict>". Only non-ok verdicts
// are marked — an unchanged excerpt gets no attribute and no styling, so a clean
// guide reads exactly as it does today.
//
// Matching is by path plus line range because a part legitimately quotes the
// same file more than once; a path-only anchor matches only a path-only result.
func markDriftedAnchors(content []byte, result *drift.Result, part string) []byte {
	if result == nil {
		return content
	}
	verdicts := map[string]drift.Verdict{}
	for _, a := range result.Anchors {
		if a.Part != part || a.Verdict == drift.VerdictOK {
			continue
		}
		verdicts[anchorKey(a.Path, a.Start, a.End)] = a.Verdict
	}
	if len(verdicts) == 0 {
		return content
	}

	return anchorOpenTag.ReplaceAllFunc(content, func(match []byte) []byte {
		sub := anchorOpenTag.FindSubmatch(match)
		start, _ := strconv.Atoi(string(sub[2]))
		end, _ := strconv.Atoi(string(sub[3]))
		verdict, ok := verdicts[anchorKey(unescapeAttr(string(sub[1])), start, end)]
		if !ok {
			return match
		}
		// Insert the attribute before the closing ">" of the open tag.
		return append(
			append([]byte{}, bytes.TrimSuffix(match, []byte(">"))...),
			[]byte(fmt.Sprintf(` data-drift=%q>`, verdict))...,
		)
	})
}

func anchorKey(path string, start, end int) string {
	return fmt.Sprintf("%s:%d-%d", path, start, end)
}

// unescapeAttr reverses the html.EscapeString anchor.Rewrite applied to the path
// attribute, so it compares equal to the raw path in the drift result.
func unescapeAttr(s string) string {
	return html.UnescapeString(s)
}
