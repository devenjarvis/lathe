// Package drift answers one question about an onboarding guide: does it still
// describe the code?
//
// It diffs the commit the guide was pinned to against HEAD and classifies every
// anchored code excerpt (see internal/anchor) as ok, moved, changed, renamed, or
// broken. This is a pure git computation — no model is involved anywhere — which
// is why it can live in the Go binary without crossing the "skills generate
// content, the CLI owns durable state" boundary.
//
// The design bias throughout is against false positives: a guide wrongly marked
// stale destroys trust faster than no drift check at all. Anything genuinely
// unknowable (a pinned commit that is not in the repo, an orphaned history)
// returns an error so callers can report "unknown" and leave status alone,
// rather than guessing.
package drift

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/devenjarvis/lathe/internal/anchor"
	"github.com/devenjarvis/lathe/internal/gitrepo"
)

// Verdict is the classification of a single anchor.
type Verdict string

const (
	// VerdictOK — the anchored range is byte-identical and in the same place.
	VerdictOK Verdict = "ok"
	// VerdictMoved — the range is intact but sits at a different line number.
	VerdictMoved Verdict = "moved"
	// VerdictChanged — a diff hunk overlaps the anchored range.
	VerdictChanged Verdict = "changed"
	// VerdictRenamed — the file moved; the anchored range itself is intact.
	VerdictRenamed Verdict = "renamed"
	// VerdictBroken — the file is gone at HEAD, or was never at the pin.
	VerdictBroken Verdict = "broken"
)

var (
	// ErrUnknownPin means the pinned commit is not in this repository — a
	// shallow clone, a rebased branch, or a GC'd commit. Drift is unknowable.
	ErrUnknownPin = errors.New("pinned commit is not present in this repository")
	// ErrNoCommonHistory means the pinned commit exists but shares no ancestor
	// with HEAD, so a diff between them says nothing about drift.
	ErrNoCommonHistory = errors.New("pinned commit shares no history with HEAD")
)

// AnchorResult is the verdict for one anchored excerpt. Path/Start/End are as
// the guide wrote them; NewPath and CurrentStart are where that code lives at
// HEAD. CurrentStart is 0 for a path-only anchor (nothing to map) and for a
// broken one (nowhere to map to).
type AnchorResult struct {
	Part         string  `json:"part,omitempty"`
	Path         string  `json:"path"`
	NewPath      string  `json:"new_path,omitempty"`
	Start        int     `json:"start,omitempty"`
	End          int     `json:"end,omitempty"`
	CurrentStart int     `json:"current_start,omitempty"`
	Verdict      Verdict `json:"verdict"`
	// Detail carries the reason a broken anchor is broken, for the CLI and the
	// warning panel. Empty for every other verdict.
	Detail string `json:"detail,omitempty"`
}

// Part pairs a part filename with the anchors parsed out of it, so results can
// name the part a reader needs to look at.
type Part struct {
	Name    string
	Anchors []anchor.Anchor
}

// Result is a whole drift run, persisted verbatim as the drift.json sidecar.
type Result struct {
	PinnedCommit string          `json:"pinned_commit"`
	HeadCommit   string          `json:"head_commit"`
	CheckedAt    string          `json:"checked_at"`
	Dirty        bool            `json:"dirty,omitempty"`
	Anchors      []AnchorResult  `json:"anchors"`
	Summary      map[Verdict]int `json:"summary"`
}

// Stale reports whether this result should flip the tutorial to stale. Only
// changed and broken count: a moved or renamed anchor still describes the code
// correctly, it just lives somewhere else.
func (r *Result) Stale() bool {
	return r.Summary[VerdictChanged] > 0 || r.Summary[VerdictBroken] > 0
}

// Problems returns just the changed and broken anchors, in document order —
// what the reading page's warning panel lists and what /lathe-verify judges.
// Moved and renamed anchors are excluded: they are not problems, only relocations.
func (r *Result) Problems() []AnchorResult {
	var out []AnchorResult
	for _, a := range r.Anchors {
		if a.Verdict == VerdictChanged || a.Verdict == VerdictBroken {
			out = append(out, a)
		}
	}
	return out
}

// StalePartsList returns the parts containing at least one changed or broken
// anchor, in first-seen order — what the reading page's warning panel names.
func (r *Result) StalePartsList() []string {
	var out []string
	seen := map[string]bool{}
	for _, a := range r.Anchors {
		if a.Verdict != VerdictChanged && a.Verdict != VerdictBroken {
			continue
		}
		if a.Part == "" || seen[a.Part] {
			continue
		}
		seen[a.Part] = true
		out = append(out, a.Part)
	}
	return out
}

// Check classifies a flat list of anchors with no part attribution. It is the
// convenience form of CheckParts.
func Check(repo *gitrepo.Repo, pinned string, anchors []anchor.Anchor) (Result, error) {
	return CheckParts(repo, pinned, []Part{{Anchors: anchors}})
}

// CheckParts classifies every anchor in every part against the diff from pinned
// to HEAD.
func CheckParts(repo *gitrepo.Repo, pinned string, parts []Part) (Result, error) {
	if !repo.HasCommit(pinned) {
		return Result{}, fmt.Errorf("%w: %s", ErrUnknownPin, pinned)
	}
	head, err := repo.HeadSHA()
	if err != nil {
		return Result{}, err
	}
	if _, err := repo.MergeBase(pinned, head); err != nil {
		return Result{}, fmt.Errorf("%w: %s", ErrNoCommonHistory, pinned)
	}
	dirty, err := repo.IsDirty()
	if err != nil {
		return Result{}, err
	}

	res := Result{
		PinnedCommit: pinned,
		HeadCommit:   head,
		CheckedAt:    time.Now().UTC().Format(time.RFC3339),
		Dirty:        dirty,
		Summary:      map[Verdict]int{},
	}

	// One FileDiff per distinct path, not per anchor — a part typically quotes
	// the same file several times.
	diffs := map[string]gitrepo.FileDiff{}
	diffErrs := map[string]error{}

	for _, part := range parts {
		for _, a := range part.Anchors {
			if _, done := diffs[a.Path]; !done {
				if _, failed := diffErrs[a.Path]; !failed {
					fd, err := repo.FileDiff(pinned, a.Path)
					if err != nil {
						if !errors.Is(err, gitrepo.ErrPathNotAtCommit) {
							return Result{}, err
						}
						diffErrs[a.Path] = err
					} else {
						diffs[a.Path] = fd
					}
				}
			}
			ar := AnchorResult{Part: part.Name, Path: a.Path, Start: a.Start, End: a.End}
			if err, failed := diffErrs[a.Path]; failed {
				ar.Verdict = VerdictBroken
				ar.Detail = fmt.Sprintf("no such file at the pinned commit (%v)", err)
			} else {
				classify(&ar, a, diffs[a.Path])
			}
			res.Anchors = append(res.Anchors, ar)
			res.Summary[ar.Verdict]++
		}
	}
	return res, nil
}

// classify turns one file's diff into a verdict for one anchor.
func classify(ar *AnchorResult, a anchor.Anchor, fd gitrepo.FileDiff) {
	if fd.Status == gitrepo.FileDeleted {
		ar.Verdict = VerdictBroken
		ar.Detail = "file deleted at HEAD"
		return
	}
	if fd.NewPath != "" && fd.NewPath != a.Path {
		ar.NewPath = fd.NewPath
	}

	// A path-only anchor names a file, not a range. It can only be ok, renamed,
	// or broken — there is nothing to move or overlap.
	if !a.HasRange() {
		if fd.Status == gitrepo.FileRenamed {
			ar.Verdict = VerdictRenamed
		} else {
			ar.Verdict = VerdictOK
		}
		return
	}

	if rangeTouched(a.Start, a.End, fd.Hunks) {
		ar.Verdict = VerdictChanged
		ar.CurrentStart, _ = MapLine(a.Start, fd.Hunks)
		return
	}

	current, _ := MapLine(a.Start, fd.Hunks)
	ar.CurrentStart = current
	switch {
	case fd.Status == gitrepo.FileRenamed:
		ar.Verdict = VerdictRenamed
	case current != a.Start:
		ar.Verdict = VerdictMoved
	default:
		ar.Verdict = VerdictOK
	}
}

// MapLine maps a 1-indexed line number from the pinned commit to its line number
// at HEAD, and reports whether the line itself falls inside a changed hunk.
//
// The arithmetic that matters: a hunk with OldCount == 0 is a pure *insertion*
// after old line OldStart (git writes OldStart == 0 for an insertion before the
// first line). It has no width on the old side, so it can never contain a line —
// treating it as a zero-width range that intersects would misreport every nearby
// insertion as a change. It only shifts lines strictly below the insertion
// point.
func MapLine(old int, hunks []gitrepo.Hunk) (newLine int, touched bool) {
	offset := 0
	for _, h := range hunks {
		if h.OldCount == 0 {
			// Pure insertion after old line h.OldStart.
			if old > h.OldStart {
				offset += h.NewCount
			}
			continue
		}
		oldEnd := h.OldStart + h.OldCount - 1
		switch {
		case old > oldEnd:
			offset += h.NewCount - h.OldCount
		case old >= h.OldStart:
			touched = true
		}
	}
	return old + offset, touched
}

// rangeTouched reports whether any hunk overlaps the inclusive old-line range
// [start, end]. Pure insertions (OldCount == 0) never overlap — see MapLine.
func rangeTouched(start, end int, hunks []gitrepo.Hunk) bool {
	for _, h := range hunks {
		if h.OldCount == 0 {
			continue
		}
		oldEnd := h.OldStart + h.OldCount - 1
		if h.OldStart <= end && oldEnd >= start {
			return true
		}
	}
	return false
}

// Verdicts is the display order for summary output — worst first, so a reader
// scanning a terminal or a warning panel sees the problems before the noise.
var Verdicts = []Verdict{VerdictBroken, VerdictChanged, VerdictRenamed, VerdictMoved, VerdictOK}

// SummaryLine renders the verdict counts in Verdicts order, e.g.
// "12 ok, 2 moved, 1 changed". Verdicts with a zero count are omitted.
func (r *Result) SummaryLine() string {
	var parts []string
	for _, v := range Verdicts {
		if n := r.Summary[v]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, v))
		}
	}
	if len(parts) == 0 {
		return "no anchors"
	}
	return strings.Join(parts, ", ")
}
