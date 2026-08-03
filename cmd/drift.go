package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/devenjarvis/lathe/internal/anchor"
	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/drift"
	"github.com/devenjarvis/lathe/internal/gitrepo"
	"github.com/devenjarvis/lathe/internal/store"
	"github.com/spf13/cobra"
)

var (
	driftRepoPath string
	driftJSON     bool
)

// driftCmd checks whether an onboarding guide still describes its repository.
//
// This is the one verification signal the binary produces by itself, and it does
// not violate the no-model-in-Go rule: drift is a git computation (diff the
// pinned commit against HEAD, map anchored line ranges through the hunks), not a
// judgement. Interpreting what the drift *means* for the prose is still the
// /lathe-verify skill's job in the user's interactive session.
var driftCmd = &cobra.Command{
	Use:   "drift <slug>",
	Short: "Check whether an onboarding guide still matches its repository",
	Long: `Diff the commit an onboarding guide is pinned to against HEAD and classify
every anchored code excerpt as ok, moved, changed, renamed, or broken.

A changed or broken anchor sets the guide's status to stale; a later clean check
returns a stale guide to unverified. Moved and renamed anchors are not drift —
the guide still describes the code, it just lives at a different line or path.

When the pinned commit is not in the repository (a shallow clone, a rebase, a
GC), the result is unknown: drift exits non-zero and leaves status untouched
rather than reporting drift it cannot actually see.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug := args[0]
		if err := validateSlug(slug); err != nil {
			return err
		}
		tutorialsDir, err := config.TutorialsDir()
		if err != nil {
			return err
		}
		tutDir := filepath.Join(tutorialsDir, slug)
		tut, err := store.ReadMetadata(tutDir)
		if err != nil {
			return fmt.Errorf("read metadata for %q: %w", slug, err)
		}
		if !tut.IsOnboarding() || tut.RepoCommit == "" {
			return fmt.Errorf("%q is not an onboarding guide with a pinned commit; drift only applies to guides stored with --kind onboarding --repo-commit", slug)
		}

		repo, err := resolveDriftRepo(tut, driftRepoPath)
		if err != nil {
			return err
		}

		parts, err := driftParts(tutDir, tut)
		if err != nil {
			return err
		}

		result, err := drift.CheckParts(repo, tut.RepoCommit, parts)
		if err != nil {
			if errors.Is(err, drift.ErrUnknownPin) || errors.Is(err, drift.ErrNoCommonHistory) {
				// Degrade to unknown rather than to stale: reporting drift we
				// cannot actually see is worse than reporting nothing. Status and
				// drift.json are both left exactly as they were.
				return fmt.Errorf("drift is unknown for %q: %w", slug, err)
			}
			return err
		}

		if err := store.WriteDrift(tutDir, &result); err != nil {
			return fmt.Errorf("write drift result: %w", err)
		}

		transition := applyDriftStatus(tut, &result)
		if transition != "" {
			if err := store.WriteMetadata(tutDir, tut); err != nil {
				return fmt.Errorf("write metadata: %w", err)
			}
		}

		out := cmd.OutOrStdout()
		if driftJSON {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			return enc.Encode(result)
		}
		printDriftReport(out, slug, tut, &result, transition)
		return nil
	},
}

// applyDriftStatus mutates tut's status for a completed drift check and returns
// a human-readable description of what it did (empty when nothing changed).
//
// Two guards matter here. A guide that is mid-verify or mid-extend is left
// alone entirely — those statuses are in-flight markers owned by the skills, and
// stomping one would strand a spinner in the web UI. And a guide only leaves
// stale when a later check comes back clean, so stale never sticks after the
// code is fixed.
func applyDriftStatus(tut *store.Tutorial, result *drift.Result) string {
	if tut.Status == store.StatusVerifying || tut.Status == store.StatusExtending {
		return ""
	}
	switch {
	case result.Stale() && tut.Status != store.StatusStale:
		tut.Status = store.StatusStale
		return "status set to stale"
	case !result.Stale() && tut.Status == store.StatusStale:
		tut.Status = store.StatusUnverified
		return "status returned to unverified"
	}
	return ""
}

// resolveDriftRepo finds the working copy to check against, in priority order:
// an explicit --repo-path, then the current directory when its origin matches
// the guide's recorded repo, and only then the RepoPath recorded at store time.
//
// The recorded path is last on purpose: it is machine-local, so it is wrong the
// moment ~/.lathe is copied to another machine or the repo is re-cloned
// elsewhere. Running from inside a checkout has to be the path that always
// works.
func resolveDriftRepo(tut *store.Tutorial, flagPath string) (*gitrepo.Repo, error) {
	if flagPath != "" {
		repo, err := gitrepo.Open(flagPath)
		if err != nil {
			return nil, fmt.Errorf("--repo-path %s: %w", flagPath, err)
		}
		return repo, nil
	}

	if cwd, err := os.Getwd(); err == nil {
		if repo, err := gitrepo.Open(cwd); err == nil {
			if origin, err := repo.OriginURL(); err == nil {
				if store.NormalizeRepo(origin) == tut.Repo {
					return repo, nil
				}
			}
		}
	}

	if tut.RepoPath != "" {
		if repo, err := gitrepo.Open(tut.RepoPath); err == nil {
			return repo, nil
		}
	}

	return nil, fmt.Errorf(
		"cannot find a checkout of %s for %q — run this from inside the repository, or point at it:\n\n  lathe drift %s --repo-path /path/to/repo",
		tut.Repo, tut.Slug, tut.Slug)
}

// driftParts reads every part of the guide and parses its anchored blocks.
func driftParts(tutDir string, tut *store.Tutorial) ([]drift.Part, error) {
	names := tut.Parts
	if len(names) == 0 {
		// Legacy single-part tutorials still store index.md.
		names = []string{"index.md"}
	}
	var parts []drift.Part
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(tutDir, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		parts = append(parts, drift.Part{Name: name, Anchors: anchor.Parse(data)})
	}
	return parts, nil
}

func printDriftReport(w io.Writer, slug string, tut *store.Tutorial, result *drift.Result, transition string) {
	_, _ = fmt.Fprintf(w, "%s — pinned %s, HEAD %s\n", slug, short(result.PinnedCommit), short(result.HeadCommit))
	_, _ = fmt.Fprintf(w, "%s\n", result.SummaryLine())
	if result.Dirty {
		_, _ = fmt.Fprintln(w, "note: the working tree has uncommitted changes; drift is measured against HEAD")
	}

	// List everything worth a reader's attention — the problems, plus anything
	// that moved, since a moved anchor's printed line number is now wrong.
	var noteworthy []drift.AnchorResult
	for _, a := range result.Anchors {
		if a.Verdict != drift.VerdictOK {
			noteworthy = append(noteworthy, a)
		}
	}
	if len(noteworthy) > 0 {
		_, _ = fmt.Fprintln(w)
		part := ""
		for _, a := range noteworthy {
			if a.Part != part {
				part = a.Part
				_, _ = fmt.Fprintf(w, "%s\n", part)
			}
			_, _ = fmt.Fprintf(w, "  %s — %s%s\n", anchorLocation(a), a.Verdict, driftDetail(a))
		}
		_, _ = fmt.Fprintln(w)
	}

	if result.Stale() {
		_, _ = fmt.Fprintf(w, "drift detected")
	} else {
		_, _ = fmt.Fprintf(w, "clean — no drift detected")
	}
	if transition != "" {
		_, _ = fmt.Fprintf(w, "; %s", transition)
	} else {
		_, _ = fmt.Fprintf(w, "; status unchanged (%s)", tut.Status)
	}
	_, _ = fmt.Fprintln(w)
}

// anchorLocation renders the anchor as the guide wrote it (path:line).
func anchorLocation(a drift.AnchorResult) string {
	if a.Start == 0 {
		return a.Path
	}
	if a.Start == a.End {
		return fmt.Sprintf("%s:%d", a.Path, a.Start)
	}
	return fmt.Sprintf("%s:%d-%d", a.Path, a.Start, a.End)
}

// driftDetail adds the "where it is now" half of a verdict.
func driftDetail(a drift.AnchorResult) string {
	switch a.Verdict {
	case drift.VerdictMoved:
		return fmt.Sprintf(" (now at line %d)", a.CurrentStart)
	case drift.VerdictRenamed:
		return fmt.Sprintf(" (now %s)", a.NewPath)
	case drift.VerdictBroken:
		if a.Detail != "" {
			return fmt.Sprintf(" (%s)", a.Detail)
		}
	case drift.VerdictChanged:
		if a.NewPath != "" {
			return fmt.Sprintf(" (now %s:%d)", a.NewPath, a.CurrentStart)
		}
	}
	return ""
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func init() {
	driftCmd.Flags().StringVar(&driftRepoPath, "repo-path", "", "path to the repository checkout to check against (defaults to the current directory, then the path recorded at store time)")
	driftCmd.Flags().BoolVar(&driftJSON, "json", false, "print the raw drift result as JSON")
	rootCmd.AddCommand(driftCmd)
}
