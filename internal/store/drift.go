package store

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/devenjarvis/lathe/internal/anchor"
	"github.com/devenjarvis/lathe/internal/drift"
	"github.com/devenjarvis/lathe/internal/gitrepo"
)

// RunDriftCheck is the whole drift flow for one stored guide: resolve the
// repository, parse the anchors out of every part, diff the pin against HEAD,
// persist drift.json, and apply the status transition.
//
// It lives here rather than in cmd/ because there are two callers — `lathe
// drift` and the reading page's "Check for drift" button — and the two must not
// be able to disagree about what a drift check does to durable state.
//
// The returned string describes the status change in human terms, or is empty
// when the status was left alone. t is mutated in place when the status changes.
func RunDriftCheck(tutorialDir string, t *Tutorial, explicitRepoPath string) (*drift.Result, string, error) {
	if !t.IsOnboarding() || t.RepoCommit == "" {
		return nil, "", fmt.Errorf("%q is not an onboarding guide with a pinned commit; drift only applies to guides stored with --kind onboarding --repo-commit", t.Slug)
	}

	repo, err := ResolveRepo(t, explicitRepoPath)
	if err != nil {
		return nil, "", err
	}
	parts, err := DriftParts(tutorialDir, t)
	if err != nil {
		return nil, "", err
	}

	result, err := drift.CheckParts(repo, t.RepoCommit, parts)
	if err != nil {
		// ErrUnknownPin / ErrNoCommonHistory reach the caller unwrapped enough to
		// be matched with errors.Is. Nothing is written: an unknowable result must
		// leave both drift.json and the status exactly as they were.
		return nil, "", err
	}

	if err := WriteDrift(tutorialDir, &result); err != nil {
		return nil, "", fmt.Errorf("write drift result: %w", err)
	}

	transition := ApplyDriftStatus(t, &result)
	if transition != "" {
		if err := WriteMetadata(tutorialDir, t); err != nil {
			return nil, "", fmt.Errorf("write metadata: %w", err)
		}
	}
	return &result, transition, nil
}

// ApplyDriftStatus mutates t's status for a completed drift check and returns a
// human-readable description of what it did (empty when nothing changed).
//
// Two guards matter. A guide that is mid-verify or mid-extend is left alone
// entirely — those are in-flight markers owned by the skills, and stomping one
// would strand a spinner in the web UI. And a guide only leaves stale when a
// later check comes back clean, so stale never sticks after the code is fixed.
func ApplyDriftStatus(t *Tutorial, result *drift.Result) string {
	if t.Status == StatusVerifying || t.Status == StatusExtending {
		return ""
	}
	switch {
	case result.Stale() && t.Status != StatusStale:
		t.Status = StatusStale
		return "status set to stale"
	case !result.Stale() && t.Status == StatusStale:
		t.Status = StatusUnverified
		return "status returned to unverified"
	}
	return ""
}

// ResolveRepo finds the working copy to check a guide against, in priority
// order: an explicit path, then the current directory when its origin matches
// the guide's recorded repo, and only then the RepoPath recorded at store time.
//
// The recorded path is last on purpose: it is machine-local, so it is wrong the
// moment ~/.lathe is copied to another machine or the repo is re-cloned
// elsewhere. Running from inside a checkout has to be the path that always
// works.
func ResolveRepo(t *Tutorial, explicitPath string) (*gitrepo.Repo, error) {
	if explicitPath != "" {
		repo, err := gitrepo.Open(explicitPath)
		if err != nil {
			return nil, fmt.Errorf("--repo-path %s: %w", explicitPath, err)
		}
		return repo, nil
	}

	if cwd, err := os.Getwd(); err == nil {
		if repo, err := gitrepo.Open(cwd); err == nil {
			if origin, err := repo.OriginURL(); err == nil {
				if NormalizeRepo(origin) == t.Repo {
					return repo, nil
				}
			}
		}
	}

	if t.RepoPath != "" {
		if repo, err := gitrepo.Open(t.RepoPath); err == nil {
			return repo, nil
		}
	}

	return nil, fmt.Errorf(
		"cannot find a checkout of %s for %q — run this from inside the repository, or point at it:\n\n  lathe drift %s --repo-path /path/to/repo",
		t.Repo, t.Slug, t.Slug)
}

// DriftParts reads every part of a guide and parses its anchored blocks.
func DriftParts(tutorialDir string, t *Tutorial) ([]drift.Part, error) {
	names := t.Parts
	if len(names) == 0 {
		// Legacy single-part tutorials still store index.md.
		names = []string{"index.md"}
	}
	parts := make([]drift.Part, 0, len(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(tutorialDir, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		parts = append(parts, drift.Part{Name: name, Anchors: anchor.Parse(data)})
	}
	return parts, nil
}
