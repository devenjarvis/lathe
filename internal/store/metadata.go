package store

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	// Sources are the URLs the generation skill actually consulted while
	// researching the tutorial — the research trail behind the prose. They are
	// distinct from the per-part inline `## Sources` citations in the markdown:
	// this is the durable, metadata-level record surfaced as provenance in the
	// web UI. Populated via `lathe store --source` and `lathe extend-commit
	// --source`; the skill never writes metadata.json directly.
	Sources []string `json:"sources,omitempty"`
}

func (t *Tutorial) IsSeries() bool {
	return len(t.Parts) > 1
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
