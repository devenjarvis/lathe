package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devenjarvis/lathe/internal/drift"
	"github.com/devenjarvis/lathe/internal/store"
)

func resetDriftFlags(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		driftRepoPath = ""
		driftJSON = false
		driftCmd.SetOut(nil)
	})
}

func driftGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

const driftSource = "package app\n\nfunc One() {}\n\nfunc Two() {}\n\nfunc Three() {}\n"

// newDriftRepo builds a git repo with a single source file and returns its dir
// plus the SHA of the initial commit.
func newDriftRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	driftGit(t, dir, "init", "-b", "main")
	driftGit(t, dir, "config", "user.email", "test@example.com")
	driftGit(t, dir, "config", "user.name", "Test")
	driftGit(t, dir, "config", "commit.gpgsign", "false")
	driftGit(t, dir, "remote", "add", "origin", "git@github.com:devenjarvis/example.git")
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte(driftSource), 0644); err != nil {
		t.Fatal(err)
	}
	driftGit(t, dir, "add", "-A")
	driftGit(t, dir, "commit", "-m", "init")
	return dir, driftGit(t, dir, "rev-parse", "HEAD")
}

// writeOnboarding stores an onboarding guide whose single part anchors lines
// 3-3 of app.go (the `func One() {}` line).
func writeOnboarding(t *testing.T, homeDir, slug, repoDir, sha string, status store.Status) string {
	t.Helper()
	tutDir := filepath.Join(homeDir, ".lathe", "tutorials", slug)
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	part := "# Guide\n\nHere is the entry point.\n\n```go path=app.go lines=3-3\nfunc One() {}\n```\n"
	if err := os.WriteFile(filepath.Join(tutDir, "part-01.md"), []byte(part), 0644); err != nil {
		t.Fatal(err)
	}
	tut := &store.Tutorial{
		Slug:       slug,
		Title:      store.SlugToTitle(slug),
		Status:     status,
		Parts:      []string{"part-01.md"},
		Kind:       store.KindOnboarding,
		Repo:       "github.com/devenjarvis/example",
		RepoBranch: "main",
		RepoCommit: sha,
		RepoPath:   repoDir,
	}
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}
	return tutDir
}

func runDrift(t *testing.T, slug string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	driftCmd.SetOut(&out)
	err := driftCmd.RunE(driftCmd, []string{slug})
	return out.String(), err
}

func TestDriftCleanLeavesStatusAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repoDir, sha := newDriftRepo(t)
	tutDir := writeOnboarding(t, home, "example-onboarding", repoDir, sha, store.StatusVerified)
	resetDriftFlags(t)

	out, err := runDrift(t, "example-onboarding")
	if err != nil {
		t.Fatalf("drift: %v\n%s", err, out)
	}

	tut, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatal(err)
	}
	if tut.Status != store.StatusVerified {
		t.Errorf("Status = %q, want verified (a clean check must not touch status)", tut.Status)
	}
	res, err := store.ReadDrift(tutDir)
	if err != nil {
		t.Fatalf("ReadDrift: %v", err)
	}
	if res.PinnedCommit != sha || res.HeadCommit != sha {
		t.Errorf("drift.json commits = %q/%q, want %q", res.PinnedCommit, res.HeadCommit, sha)
	}
	if res.Summary[drift.VerdictOK] != 1 {
		t.Errorf("Summary = %#v, want one ok", res.Summary)
	}
	if !strings.Contains(out, "no drift") {
		t.Errorf("output should report no drift:\n%s", out)
	}
}

func TestDriftChangedFlipsToStale(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repoDir, sha := newDriftRepo(t)
	tutDir := writeOnboarding(t, home, "example-onboarding", repoDir, sha, store.StatusVerified)
	resetDriftFlags(t)

	edited := strings.Replace(driftSource, "func One() {}", "func One(ctx context.Context) {}", 1)
	if err := os.WriteFile(filepath.Join(repoDir, "app.go"), []byte(edited), 0644); err != nil {
		t.Fatal(err)
	}
	driftGit(t, repoDir, "commit", "-am", "change One")

	out, err := runDrift(t, "example-onboarding")
	if err != nil {
		t.Fatalf("drift: %v\n%s", err, out)
	}

	tut, _ := store.ReadMetadata(tutDir)
	if tut.Status != store.StatusStale {
		t.Errorf("Status = %q, want stale", tut.Status)
	}
	res, err := store.ReadDrift(tutDir)
	if err != nil {
		t.Fatalf("ReadDrift: %v", err)
	}
	if res.Summary[drift.VerdictChanged] != 1 {
		t.Errorf("Summary = %#v, want one changed", res.Summary)
	}
	if res.Anchors[0].Part != "part-01.md" {
		t.Errorf("anchor Part = %q, want part-01.md", res.Anchors[0].Part)
	}
	if !strings.Contains(out, "changed") || !strings.Contains(out, "app.go") {
		t.Errorf("output should name the changed anchor:\n%s", out)
	}
}

func TestDriftInsertionAboveIsMovedNotStale(t *testing.T) {
	// The false-positive test: a blank line inserted above the anchored range
	// must report moved and leave status alone.
	home := t.TempDir()
	t.Setenv("HOME", home)
	repoDir, sha := newDriftRepo(t)
	tutDir := writeOnboarding(t, home, "example-onboarding", repoDir, sha, store.StatusVerified)
	resetDriftFlags(t)

	if err := os.WriteFile(filepath.Join(repoDir, "app.go"), []byte("// added\n"+driftSource), 0644); err != nil {
		t.Fatal(err)
	}
	driftGit(t, repoDir, "commit", "-am", "add a comment at the top")

	if _, err := runDrift(t, "example-onboarding"); err != nil {
		t.Fatalf("drift: %v", err)
	}

	tut, _ := store.ReadMetadata(tutDir)
	if tut.Status != store.StatusVerified {
		t.Errorf("Status = %q, want verified — a moved anchor is not drift", tut.Status)
	}
	res, _ := store.ReadDrift(tutDir)
	if res.Anchors[0].Verdict != drift.VerdictMoved {
		t.Fatalf("Verdict = %q, want moved", res.Anchors[0].Verdict)
	}
	if res.Anchors[0].CurrentStart != 4 {
		t.Errorf("CurrentStart = %d, want 4", res.Anchors[0].CurrentStart)
	}
}

func TestDriftRevertReturnsStaleToUnverified(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repoDir, sha := newDriftRepo(t)
	tutDir := writeOnboarding(t, home, "example-onboarding", repoDir, sha, store.StatusVerified)
	resetDriftFlags(t)

	edited := strings.Replace(driftSource, "func One() {}", "func One(ctx context.Context) {}", 1)
	if err := os.WriteFile(filepath.Join(repoDir, "app.go"), []byte(edited), 0644); err != nil {
		t.Fatal(err)
	}
	driftGit(t, repoDir, "commit", "-am", "change One")
	if _, err := runDrift(t, "example-onboarding"); err != nil {
		t.Fatal(err)
	}
	if tut, _ := store.ReadMetadata(tutDir); tut.Status != store.StatusStale {
		t.Fatalf("setup: Status = %q, want stale", tut.Status)
	}

	driftGit(t, repoDir, "revert", "--no-edit", "HEAD")
	if _, err := runDrift(t, "example-onboarding"); err != nil {
		t.Fatal(err)
	}
	if tut, _ := store.ReadMetadata(tutDir); tut.Status != store.StatusUnverified {
		t.Errorf("Status = %q, want unverified after a clean re-check", tut.Status)
	}
}

func TestDriftUnknownPinExitsNonZeroAndLeavesStatus(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repoDir, _ := newDriftRepo(t)
	tutDir := writeOnboarding(t, home, "example-onboarding", repoDir,
		"0000000000000000000000000000000000000000", store.StatusVerified)
	resetDriftFlags(t)

	out, err := runDrift(t, "example-onboarding")
	if err == nil {
		t.Fatal("drift with an unknown pinned commit should return an error")
	}
	if tut, _ := store.ReadMetadata(tutDir); tut.Status != store.StatusVerified {
		t.Errorf("Status = %q, want verified — an unknown pin must not change status", tut.Status)
	}
	if _, err := store.ReadDrift(tutDir); !os.IsNotExist(err) {
		t.Errorf("an unknown pin must not write drift.json: err=%v", err)
	}
	if !strings.Contains(out+err.Error(), "unknown") {
		t.Errorf("output/error should say the result is unknown:\nout=%s\nerr=%v", out, err)
	}
}

func TestDriftSkipsInFlightStatuses(t *testing.T) {
	for _, status := range []store.Status{store.StatusVerifying, store.StatusExtending} {
		t.Run(string(status), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			repoDir, sha := newDriftRepo(t)
			tutDir := writeOnboarding(t, home, "example-onboarding", repoDir, sha, status)
			resetDriftFlags(t)

			edited := strings.Replace(driftSource, "func One() {}", "func One(ctx context.Context) {}", 1)
			if err := os.WriteFile(filepath.Join(repoDir, "app.go"), []byte(edited), 0644); err != nil {
				t.Fatal(err)
			}
			driftGit(t, repoDir, "commit", "-am", "change One")

			if _, err := runDrift(t, "example-onboarding"); err != nil {
				t.Fatal(err)
			}
			if tut, _ := store.ReadMetadata(tutDir); tut.Status != status {
				t.Errorf("Status = %q, want %q — drift must not disturb in-flight work", tut.Status, status)
			}
		})
	}
}

func TestDriftJSONOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repoDir, sha := newDriftRepo(t)
	writeOnboarding(t, home, "example-onboarding", repoDir, sha, store.StatusUnverified)
	resetDriftFlags(t)
	driftJSON = true

	out, err := runDrift(t, "example-onboarding")
	if err != nil {
		t.Fatal(err)
	}
	var res drift.Result
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("--json output is not valid JSON: %v\n%s", err, out)
	}
	if res.PinnedCommit != sha {
		t.Errorf("PinnedCommit = %q, want %q", res.PinnedCommit, sha)
	}
}

func TestDriftRejectsNonOnboardingTutorial(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeTutorial(t, home, "plain-tutorial", store.StatusUnverified, []string{"part-01.md"})
	resetDriftFlags(t)

	if _, err := runDrift(t, "plain-tutorial"); err == nil {
		t.Fatal("drift on a plain tutorial should error")
	}
}

func TestDriftRepoPathFlagWins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repoDir, sha := newDriftRepo(t)
	tutDir := writeOnboarding(t, home, "example-onboarding", repoDir, sha, store.StatusUnverified)
	// Point the recorded path somewhere useless, the way a copied ~/.lathe would.
	tut, _ := store.ReadMetadata(tutDir)
	tut.RepoPath = filepath.Join(home, "does-not-exist")
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}
	resetDriftFlags(t)
	driftRepoPath = repoDir

	if _, err := runDrift(t, "example-onboarding"); err != nil {
		t.Fatalf("--repo-path should resolve the repo: %v", err)
	}
}

func TestDriftUnresolvableRepoNamesTheCommandToRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_, sha := newDriftRepo(t)
	tutDir := writeOnboarding(t, home, "example-onboarding", filepath.Join(home, "gone"), sha, store.StatusVerified)
	resetDriftFlags(t)

	_, err := runDrift(t, "example-onboarding")
	if err == nil {
		t.Fatal("drift with no resolvable repo should error")
	}
	if !strings.Contains(err.Error(), "--repo-path") {
		t.Errorf("error should name the command to run, got: %v", err)
	}
	if tut, _ := store.ReadMetadata(tutDir); tut.Status != store.StatusVerified {
		t.Errorf("Status = %q, want verified", tut.Status)
	}
}

func TestDriftInvalidSlug(t *testing.T) {
	resetDriftFlags(t)
	if _, err := runDrift(t, "../escape"); err == nil {
		t.Fatal("drift should reject a traversal slug")
	}
}
