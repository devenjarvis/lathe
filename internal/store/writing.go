package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// While the hosted agent writes a new tutorial into a reservation (status
// "writing", see new.go), the /lathe skill reports where it is through
// `lathe writing-step` or the lathe_progress MCP tool: which of the four
// steps it is on, the sources it has read so far, and — just before it ends
// its turn on a question — a short label for that question plus a few quick
// replies. RecordWriting keeps that in the reservation's writing.json, which
// lathe serve reads for the "Being written" cover and the progress page.
// Store drops the file when the tutorial is stored (it describes the run, not
// the tutorial), and CancelNew removes it with the reservation.

// WritingFile is the reservation's progress sidecar.
const WritingFile = "writing.json"

// The steps of writing a new tutorial, in order.
const (
	WritingVersions = "versions"
	WritingResearch = "research"
	WritingWrite    = "write"
	WritingShelve   = "shelve"
)

// WritingStep is one step as the reader sees it.
type WritingStep struct {
	ID    string
	Label string
}

// WritingSteps lists the steps in order.
var WritingSteps = []WritingStep{
	{WritingVersions, "Pin versions"},
	{WritingResearch, "Research"},
	{WritingWrite, "Write Part 1"},
	{WritingShelve, "Put it on your shelf"},
}

// WritingStepIndex is step's 1-based position in WritingSteps, or 0 for an
// unknown step.
func WritingStepIndex(step string) int {
	for i, s := range WritingSteps {
		if s.ID == step {
			return i + 1
		}
	}
	return 0
}

// Limits on what a run may record, so a chatty agent can't grow the file
// without bound or flood the cover.
const (
	maxWritingSources = 60
	maxAskLen         = 60
	maxReplies        = 4
	maxReplyLen       = 40
)

// WritingAsk is what the agent is asking the reader: a short label for the
// cover ("confirm versions") and quick replies for the panel.
type WritingAsk struct {
	Label   string    `json:"label"`
	Replies []string  `json:"replies,omitempty"`
	At      time.Time `json:"at"`
}

// Writing is a reservation's progress.
type Writing struct {
	Step      string      `json:"step,omitempty"`
	Sources   []Source    `json:"sources,omitempty"`
	Ask       *WritingAsk `json:"ask,omitempty"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// WritingUpdate is one progress report. Step, when set, replaces the current
// step; Sources are added (deduplicated by URL); Ask, when set, records the
// question the agent is about to ask, and a report without one clears it.
type WritingUpdate struct {
	Step    string
	Sources []Source
	Ask     string
	Replies []string
}

// ReadWriting returns a reservation's progress, or nil (no error) when none
// was recorded yet.
func ReadWriting(tutDir string) (*Writing, error) {
	var w Writing
	if err := readJSONFile(filepath.Join(tutDir, WritingFile), &w); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return &w, nil
}

// RecordWriting applies a progress report to a reservation. It refuses a
// tutorial that is not being written (ErrNotInFlight): the run was stopped,
// or the tutorial is already stored.
func RecordWriting(tutDir string, u WritingUpdate) (*Writing, error) {
	slug := filepath.Base(tutDir)
	tut, err := ReadMetadata(tutDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %q is gone (the run was stopped)", ErrNotInFlight, slug)
		}
		return nil, err
	}
	if tut.Status != StatusWriting {
		return nil, fmt.Errorf("%w: %q is not being written (status: %s)", ErrNotInFlight, slug, tut.Status)
	}
	step := strings.ToLower(strings.TrimSpace(u.Step))
	if step != "" && WritingStepIndex(step) == 0 {
		return nil, fmt.Errorf("unknown step %q (versions, research, write or shelve)", u.Step)
	}
	ask := strings.Join(strings.Fields(u.Ask), " ")
	if utf8.RuneCountInString(ask) > maxAskLen {
		return nil, fmt.Errorf("ask is a short label for the question (at most %d characters), e.g. \"confirm versions\"", maxAskLen)
	}
	var replies []string
	for _, r := range u.Replies {
		r = strings.Join(strings.Fields(r), " ")
		if r == "" {
			continue
		}
		if utf8.RuneCountInString(r) > maxReplyLen {
			return nil, fmt.Errorf("quick reply %q is too long (at most %d characters)", r, maxReplyLen)
		}
		replies = append(replies, r)
	}
	if len(replies) > maxReplies {
		return nil, fmt.Errorf("at most %d quick replies", maxReplies)
	}
	if len(replies) > 0 && ask == "" {
		return nil, errors.New("replies need an ask: the label of the question they answer")
	}

	w, err := ReadWriting(tutDir)
	if err != nil || w == nil {
		w = &Writing{} // an unreadable file is replaced, not fatal
	}
	if step != "" {
		w.Step = step
	}
	if srcs := NormalizeSources(u.Sources); len(srcs) > 0 {
		w.Sources = NormalizeSources(append(w.Sources, srcs...))
		if len(w.Sources) > maxWritingSources {
			w.Sources = w.Sources[:maxWritingSources]
		}
	}
	w.Ask = nil
	now := time.Now().UTC()
	if ask != "" {
		w.Ask = &WritingAsk{Label: ask, Replies: replies, At: now}
	}
	w.UpdatedAt = now
	if err := writeJSONFile(filepath.Join(tutDir, WritingFile), w); err != nil {
		return nil, err
	}
	return w, nil
}
