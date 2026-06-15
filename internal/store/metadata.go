package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Status string

const (
	StatusUnverified Status = "unverified"
	StatusVerifying  Status = "verifying"
	StatusVerified   Status = "verified"
	StatusFailed     Status = "failed"
	StatusSkipped    Status = "skipped"
	StatusExtending  Status = "extending"
)

type Tutorial struct {
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	Topic       string    `json:"topic"`
	Created     time.Time `json:"created"`
	Status      Status    `json:"status"`
	Tags        []string  `json:"tags,omitempty"`
	Parts       []string  `json:"parts,omitempty"`
	PendingPart string    `json:"pending_part,omitempty"`
	// Progress is the reader-saved position, read from the progress.json sidecar
	// at ReadMetadata time. It is tagged json:"-" so a ReadMetadata→mutate→
	// WriteMetadata round-trip can never snapshot the sidecar into metadata.json
	// (the binary stays the sole writer of each durable file). nil when there is
	// no progress.json or it could not be read.
	Progress *Progress `json:"-"`
	// Repo is the canonical identifier (host/org/repo) of the git repository the
	// tutorial was written for, derived from the repo's origin remote by the
	// generation skill and normalized by NormalizeRepo. Tutorials with no repo
	// leave it empty. RepoBranch records the branch the tutorial targets (only
	// meaningful when Repo is set).
	Repo       string `json:"repo,omitempty"`
	RepoBranch string `json:"repo_branch,omitempty"`
	// Tools are the languages/tools and their versions the tutorial is rooted in,
	// captured up front so an old tutorial (e.g. written against an outdated
	// toolchain) is identifiable later. Surfaced as version chips and a dedicated
	// "Versions" filter in the web UI — distinct from the free-form Tags.
	// Populated via `lathe store --tool name:version`; the skill never writes
	// metadata.json directly.
	Tools []Tool `json:"tools,omitempty"`
	// Sources are the URLs the generation skill actually consulted while
	// researching the tutorial — the research trail behind the prose. They are
	// distinct from the per-part inline `## Sources` citations in the markdown:
	// this is the durable, metadata-level record surfaced as provenance in the
	// web UI. Populated via `lathe store --source` and `lathe extend-commit
	// --source`; the skill never writes metadata.json directly.
	Sources []string `json:"sources,omitempty"`
	// Voice is the writing voice the tutorial was generated in (a built-in preset
	// or a custom voice name). Recorded so /lathe-extend continues in the same
	// voice and the served page can disclose it. Empty (pre-feature tutorials)
	// means the reader/skill should fall back to the configured default voice.
	// Populated via `lathe store --voice`; the skill never writes metadata.json
	// directly.
	Voice string `json:"voice,omitempty"`
	// Model is the free-form display label of the LLM that authored the tutorial
	// (e.g. "Claude Opus 4.8"), shown in the byline on the served reading page.
	// Populated via `lathe store --model` (and refreshed by `lathe extend-commit
	// --model`); the skill never writes metadata.json directly. Empty (pre-feature
	// tutorials) means the reader falls back to a generic "an LLM".
	Model string `json:"model,omitempty"`
}

// Tool is a single language/tool the tutorial targets, paired with the version
// it was written against (Version may be empty if unknown).
type Tool struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// Progress is a reader-saved position within a tutorial. Part is the rendered
// markdown file (part-NN.md or legacy index.md), Ratio is a 0..1 scroll ratio,
// HeadingID is an optional best-effort hint, and UpdatedAt records when progress
// was last saved.
type Progress struct {
	Part      string    `json:"part"`
	Ratio     float64   `json:"ratio"`
	HeadingID string    `json:"heading_id,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (t *Tutorial) IsSeries() bool {
	return len(t.Parts) > 1
}

// RepoDisplay returns the short, human-facing form of the repo (the last two
// path segments, e.g. "devenjarvis/lathe"), or "" when no repo is set. Used as
// the data-repo attribute on list-page cards for client-side search.
func (t *Tutorial) RepoDisplay() string {
	if t.Repo == "" {
		return ""
	}
	parts := strings.Split(t.Repo, "/")
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], "/")
	}
	return t.Repo
}

type VerifyResult struct {
	Status     Status `json:"status"`
	Part       string `json:"part,omitempty"`
	FailedStep int    `json:"failed_step,omitempty"`
	Error      string `json:"error,omitempty"`
	CheckedAt  string `json:"checked_at,omitempty"`
}

func ReadMetadata(tutorialDir string) (*Tutorial, error) {
	var t Tutorial
	if err := readJSONFile(filepath.Join(tutorialDir, "metadata.json"), &t); err != nil {
		return nil, err
	}
	// Read progress best-effort: a corrupt, locked, or missing progress.json
	// must never block reading the tutorial. Any error leaves Progress nil and
	// is swallowed here — mirroring how verify-result.json is read at point of
	// use with errors ignored.
	if progress, err := ReadProgress(tutorialDir); err == nil {
		t.Progress = progress
	}
	return &t, nil
}

func WriteMetadata(tutorialDir string, t *Tutorial) error {
	return writeJSONFile(filepath.Join(tutorialDir, "metadata.json"), t)
}

func ReadProgress(tutorialDir string) (*Progress, error) {
	var progress Progress
	if err := readJSONFile(filepath.Join(tutorialDir, "progress.json"), &progress); err != nil {
		return nil, err
	}
	return &progress, nil
}

func WriteProgress(tutorialDir string, progress *Progress) error {
	return writeJSONFile(filepath.Join(tutorialDir, "progress.json"), progress)
}

// ExerciseState maps a part filename (e.g. "part-02.md") to the indices of the
// exercises a reader has checked off in that part. It lives in an exercises.json
// sidecar, deliberately separate from progress.json: checkbox state is per-part
// and bidirectional (unchecking is a normal action), so it must never be folded
// into the monotonic, single-slot reading-progress record.
type ExerciseState map[string][]int

// ReadExercises returns the saved exercise checkbox state for a tutorial. A
// missing exercises.json is not an error — it yields an empty (non-nil) state —
// so callers can treat "never saved" and "saved nothing" identically. A present
// but unreadable or corrupt file still surfaces its error.
func ReadExercises(tutorialDir string) (ExerciseState, error) {
	state := ExerciseState{}
	if err := readJSONFile(filepath.Join(tutorialDir, "exercises.json"), &state); err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return state, err
	}
	return state, nil
}

// WriteExercisePart merges one part's checked indices into exercises.json,
// leaving every other part's entry untouched — so saving part 2 can never
// clobber or check part 1's boxes. The incoming slice is the part's complete
// checked set (an unchecked box is simply absent), so an empty set deletes the
// part's entry entirely rather than storing an empty array.
func WriteExercisePart(tutorialDir, part string, checked []int) error {
	state, err := ReadExercises(tutorialDir)
	if err != nil {
		return err
	}
	if len(checked) > 0 {
		state[part] = checked
	} else {
		delete(state, part)
	}
	return writeJSONFile(filepath.Join(tutorialDir, "exercises.json"), state)
}

func ReadVerifyResult(tutorialDir string) (*VerifyResult, error) {
	var v VerifyResult
	if err := readJSONFile(filepath.Join(tutorialDir, "verify-result.json"), &v); err != nil {
		return nil, err
	}
	return &v, nil
}

func WriteVerifyResult(tutorialDir string, v *VerifyResult) error {
	return writeJSONFile(filepath.Join(tutorialDir, "verify-result.json"), v)
}

// readJSONFile reads path and unmarshals its JSON into v. It returns the raw
// os/json error to the caller (callers like ReadMetadata decide whether to
// swallow it).
func readJSONFile(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// writeJSONFile marshals v as indented JSON and writes it to path atomically:
// it writes a temp file in the same directory (so the rename stays on one
// filesystem) and os.Rename's it into place, so a torn write can never leave a
// half-written or corrupt file behind.
func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(0644); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
