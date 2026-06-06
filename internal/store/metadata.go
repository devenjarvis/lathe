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
	// Repo is the canonical identifier (host/org/repo) of the git repository the
	// tutorial was written for, derived from the repo's origin remote by the
	// generation skill and normalized by NormalizeRepo. Tutorials with no repo
	// leave it empty and group under "No repo" in the web UI. RepoBranch records
	// the branch the tutorial targets (only meaningful when Repo is set).
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
	// tutorials) means the reader falls back to a generic "Claude".
	Model string `json:"model,omitempty"`
}

// Tool is a single language/tool the tutorial targets, paired with the version
// it was written against (Version may be empty if unknown).
type Tool struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

func (t *Tutorial) IsSeries() bool {
	return len(t.Parts) > 1
}

// RepoDisplay returns the short, human-facing form of the repo (the last two
// path segments, e.g. "devenjarvis/lathe"), or "" when no repo is set. Used as
// the group label on the web list page.
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
	data, err := os.ReadFile(filepath.Join(tutorialDir, "metadata.json"))
	if err != nil {
		return nil, err
	}
	var t Tutorial
	return &t, json.Unmarshal(data, &t)
}

func WriteMetadata(tutorialDir string, t *Tutorial) error {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(tutorialDir, "metadata.json"), data, 0644)
}

func ReadVerifyResult(tutorialDir string) (*VerifyResult, error) {
	data, err := os.ReadFile(filepath.Join(tutorialDir, "verify-result.json"))
	if err != nil {
		return nil, err
	}
	var v VerifyResult
	return &v, json.Unmarshal(data, &v)
}

func WriteVerifyResult(tutorialDir string, v *VerifyResult) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(tutorialDir, "verify-result.json"), data, 0644)
}
