package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/drift"
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

		// The whole flow — repo resolution, anchor parsing, the check, drift.json,
		// and the status transition — lives in store.RunDriftCheck so this command
		// and the reading page's "Check for drift" button cannot disagree about
		// what a drift check does.
		result, transition, err := store.RunDriftCheck(tutDir, tut, driftRepoPath)
		if err != nil {
			if errors.Is(err, drift.ErrUnknownPin) || errors.Is(err, drift.ErrNoCommonHistory) {
				// Degrade to unknown rather than to stale: reporting drift we
				// cannot actually see is worse than reporting nothing. Status and
				// drift.json are both left exactly as they were.
				return fmt.Errorf("drift is unknown for %q: %w", slug, err)
			}
			return err
		}

		out := cmd.OutOrStdout()
		if driftJSON {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			return enc.Encode(result)
		}
		printDriftReport(out, slug, tut, result, transition)
		return nil
	},
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
