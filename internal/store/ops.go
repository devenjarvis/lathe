package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/extend"
)

// The write operations the skills drive. Each one is a whole state mutation
// on one tutorial dir, shared by the CLI commands (`lathe verify-result`,
// `extend-start`, `extend-commit`, `tag`) and the MCP tools `lathe serve`
// hands a hosted agent, so both transports write exactly the same state.

// ValidateSlug guards against empty, dot, and path-separator slugs so a slug
// (or a part filename) can never escape ~/.lathe/tutorials/.
func ValidateSlug(slug string) error {
	if slug == "" || slug == "." || slug == ".." || strings.ContainsAny(slug, `/\`) {
		return fmt.Errorf("invalid slug: %q", slug)
	}
	return nil
}

// TutorialPath validates slug and returns its dir under ~/.lathe/tutorials/.
func TutorialPath(slug string) (string, error) {
	if err := ValidateSlug(slug); err != nil {
		return "", err
	}
	dir, err := config.TutorialsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, slug), nil
}

// VerifyRecord is one /lathe-verify outcome to record.
type VerifyRecord struct {
	Status     Status
	Part       string // filename of the part that failed
	FailedStep int    // 1-indexed step that failed
	Error      string
	CheckedAt  string // RFC3339; defaults to now
	// Code is the scratch git repo to keep as the code snapshot. RecordVerify
	// only validates it; the caller runs SaveCode after the status is
	// recorded, so a snapshot problem can never strand the badge.
	Code string
}

// RecordVerify records a verification status. "verifying" only flips the
// in-flight badge; a terminal status writes verify-result.json, then
// metadata.
func RecordVerify(tutDir string, rec VerifyRecord) error {
	slug := filepath.Base(tutDir)
	switch rec.Status {
	case StatusVerifying, StatusVerified, StatusFailed, StatusSkipped:
	default:
		return fmt.Errorf("invalid --status %q (want verifying, verified, failed, or skipped)", rec.Status)
	}
	// A code snapshot only exists once the skill has followed the parts: a
	// verified run covers every part, a failed one the parts before the
	// failure. A skipped run never built anything.
	if rec.Code != "" && rec.Status != StatusVerified && rec.Status != StatusFailed {
		return fmt.Errorf("--code is only valid with --status verified or failed")
	}
	tut, err := ReadMetadata(tutDir)
	if err != nil {
		return fmt.Errorf("read metadata for %q: %w", slug, err)
	}
	if rec.Status == StatusVerifying {
		// Don't start a verify on top of an in-flight extend.
		if tut.Status == StatusExtending {
			return fmt.Errorf("cannot verify %q while it is extending", slug)
		}
		if tut.Status == StatusWriting {
			return fmt.Errorf("cannot verify %q while it is still being written", slug)
		}
		tut.Status = StatusVerifying
		return WriteMetadata(tutDir, tut)
	}
	checkedAt := rec.CheckedAt
	if checkedAt == "" {
		checkedAt = time.Now().UTC().Format(time.RFC3339)
	}
	result := &VerifyResult{
		Status:     rec.Status,
		Part:       rec.Part,
		FailedStep: rec.FailedStep,
		Error:      rec.Error,
		CheckedAt:  checkedAt,
	}
	if err := WriteVerifyResult(tutDir, result); err != nil {
		return fmt.Errorf("write verify result: %w", err)
	}
	tut.Status = rec.Status
	if err := WriteMetadata(tutDir, tut); err != nil {
		return fmt.Errorf("write metadata: %w", err)
	}
	return nil
}

// ExtendStart reserves the next part, marks the tutorial extending, and
// returns the filename the skill should write.
func ExtendStart(tutDir string) (string, error) {
	slug := filepath.Base(tutDir)
	// Legacy single-part tutorials store index.md; promote it so the series
	// always lives in part-NN.md files before we append.
	if err := PromoteIndexToPart(tutDir); err != nil {
		return "", fmt.Errorf("promote legacy tutorial: %w", err)
	}
	tut, err := ReadMetadata(tutDir)
	if err != nil {
		return "", fmt.Errorf("read metadata for %q: %w", slug, err)
	}
	if tut.Status == StatusExtending || tut.Status == StatusVerifying || tut.Status == StatusWriting {
		return "", fmt.Errorf("already extending or verifying: status is %q", tut.Status)
	}
	pending := extend.NextPartFilename(tut.Parts)
	tut.Status = StatusExtending
	tut.PendingPart = pending
	if err := WriteMetadata(tutDir, tut); err != nil {
		return "", fmt.Errorf("write metadata: %w", err)
	}
	return pending, nil
}

// ExtendCommit records a newly written part: it appends the part (once),
// folds new sources into the research trail, refreshes the model label when
// one is given, clears the pending marker and resets status to unverified.
func ExtendCommit(tutDir, partFile string, sources []Source, model string) error {
	slug := filepath.Base(tutDir)
	if err := ValidateSlug(partFile); err != nil {
		return fmt.Errorf("invalid part file: %w", err)
	}
	if _, err := os.Stat(filepath.Join(tutDir, partFile)); err != nil {
		return fmt.Errorf("part file %q not found: %w", partFile, err)
	}
	tut, err := ReadMetadata(tutDir)
	if err != nil {
		return fmt.Errorf("read metadata for %q: %w", slug, err)
	}
	found := false
	for _, p := range tut.Parts {
		if p == partFile {
			found = true
			break
		}
	}
	if !found {
		tut.Parts = append(tut.Parts, partFile)
	}
	if len(sources) > 0 {
		tut.Sources = NormalizeSources(append(tut.Sources, sources...))
	}
	// Last writer wins, so re-extending in a newer model updates the byline;
	// an empty model leaves the existing value untouched.
	if m := NormalizeModel(model); m != "" {
		tut.Model = m
	}
	tut.PendingPart = ""
	tut.Status = StatusUnverified
	if err := WriteMetadata(tutDir, tut); err != nil {
		return fmt.Errorf("write metadata: %w", err)
	}
	return nil
}

// ErrNotInFlight is wrapped by CancelVerify and CancelExtend when the
// tutorial is not in the state they undo.
var ErrNotInFlight = errors.New("nothing to cancel")

// statusBeforeRun is the status to restore when an in-flight verify or extend
// is abandoned: the last completed verification's, else unverified.
// Cancellation is not a result, so verify-result.json is never written.
func statusBeforeRun(tutDir string) Status {
	if prior, err := ReadVerifyResult(tutDir); err == nil && prior.Status != "" {
		return prior.Status
	}
	return StatusUnverified
}

// CancelVerify recovers a tutorial whose verification stopped before it
// recorded a result (the session died, or the run was cancelled): it restores
// the status of the last completed run and returns it.
func CancelVerify(tutDir string) (Status, error) {
	slug := filepath.Base(tutDir)
	tut, err := ReadMetadata(tutDir)
	if err != nil {
		return "", fmt.Errorf("no stored tutorial %q: %w", slug, err)
	}
	if tut.Status != StatusVerifying {
		return tut.Status, fmt.Errorf("%w: %q is not verifying (status: %s)", ErrNotInFlight, slug, tut.Status)
	}
	tut.Status = statusBeforeRun(tutDir)
	if err := WriteMetadata(tutDir, tut); err != nil {
		return "", fmt.Errorf("write metadata: %w", err)
	}
	return tut.Status, nil
}

// CancelExtend recovers a tutorial whose extend stopped before
// ExtendCommit: it deletes the half-written reserved part (it was never
// registered, so nothing links to it), clears the reservation and restores
// the status of the last completed verification.
func CancelExtend(tutDir string) (Status, error) {
	slug := filepath.Base(tutDir)
	tut, err := ReadMetadata(tutDir)
	if err != nil {
		return "", fmt.Errorf("no stored tutorial %q: %w", slug, err)
	}
	if tut.Status != StatusExtending {
		return tut.Status, fmt.Errorf("%w: %q is not extending (status: %s)", ErrNotInFlight, slug, tut.Status)
	}
	if p := tut.PendingPart; p != "" && ValidateSlug(p) == nil && !slices.Contains(tut.Parts, p) {
		if err := os.Remove(filepath.Join(tutDir, p)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("remove unfinished part %s: %w", p, err)
		}
	}
	tut.PendingPart = ""
	tut.Status = statusBeforeRun(tutDir)
	if err := WriteMetadata(tutDir, tut); err != nil {
		return "", fmt.Errorf("write metadata: %w", err)
	}
	return tut.Status, nil
}

// SplitTags flattens tag values, also splitting comma-separated ones so
// "a,b" and ["a", "b"] mean the same. Normalization happens in NormalizeTags.
func SplitTags(raw []string) []string {
	var out []string
	for _, r := range raw {
		out = append(out, strings.Split(r, ",")...)
	}
	return out
}

// UpdateTags edits a tutorial's tags: set replaces the whole list, then add
// appends and remove drops. It returns the normalized result.
func UpdateTags(tutDir string, set, add, remove []string) ([]string, error) {
	slug := filepath.Base(tutDir)
	if len(set) == 0 && len(add) == 0 && len(remove) == 0 {
		return nil, fmt.Errorf("nothing to do: pass --set, --add, or --remove")
	}
	tut, err := ReadMetadata(tutDir)
	if err != nil {
		return nil, fmt.Errorf("read metadata for %q: %w", slug, err)
	}
	tags := tut.Tags
	if len(set) > 0 {
		tags = SplitTags(set)
	}
	tags = NormalizeTags(append(tags, SplitTags(add)...))
	if drop := NormalizeTags(SplitTags(remove)); len(drop) > 0 {
		gone := make(map[string]struct{}, len(drop))
		for _, r := range drop {
			gone[r] = struct{}{}
		}
		kept := tags[:0]
		for _, t := range tags {
			if _, ok := gone[t]; !ok {
				kept = append(kept, t)
			}
		}
		tags = kept
	}
	tut.Tags = tags
	if err := WriteMetadata(tutDir, tut); err != nil {
		return nil, fmt.Errorf("write metadata: %w", err)
	}
	return tags, nil
}
