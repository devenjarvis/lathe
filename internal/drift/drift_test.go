package drift

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devenjarvis/lathe/internal/anchor"
	"github.com/devenjarvis/lathe/internal/gitrepo"
)

func TestMapLine(t *testing.T) {
	tests := []struct {
		name      string
		old       int
		hunks     []gitrepo.Hunk
		wantLine  int
		wantTouch bool
	}{
		{
			name:     "no hunks",
			old:      10,
			hunks:    nil,
			wantLine: 10,
		},
		{
			// The pure-insertion case named in the plan's risks: OldCount == 0 is
			// an insertion *after* old line OldStart, never an overlap.
			name:     "insertion at top of file shifts everything down",
			old:      10,
			hunks:    []gitrepo.Hunk{{OldStart: 0, OldCount: 0, NewStart: 1, NewCount: 2}},
			wantLine: 12,
		},
		{
			name:     "insertion strictly before the line",
			old:      10,
			hunks:    []gitrepo.Hunk{{OldStart: 4, OldCount: 0, NewStart: 5, NewCount: 3}},
			wantLine: 13,
		},
		{
			name:     "insertion after the line does not shift it",
			old:      10,
			hunks:    []gitrepo.Hunk{{OldStart: 50, OldCount: 0, NewStart: 51, NewCount: 3}},
			wantLine: 10,
		},
		{
			// "after old line 10" is below line 10, so line 10 itself is unmoved
			// and untouched.
			name:     "insertion immediately after the line",
			old:      10,
			hunks:    []gitrepo.Hunk{{OldStart: 10, OldCount: 0, NewStart: 11, NewCount: 4}},
			wantLine: 10,
		},
		{
			name:      "modification on exactly the line",
			old:       10,
			hunks:     []gitrepo.Hunk{{OldStart: 10, OldCount: 1, NewStart: 10, NewCount: 1}},
			wantLine:  10,
			wantTouch: true,
		},
		{
			name:      "modification spanning the line",
			old:       10,
			hunks:     []gitrepo.Hunk{{OldStart: 5, OldCount: 20, NewStart: 5, NewCount: 20}},
			wantLine:  10,
			wantTouch: true,
		},
		{
			name:      "modification ending on the line",
			old:       10,
			hunks:     []gitrepo.Hunk{{OldStart: 8, OldCount: 3, NewStart: 8, NewCount: 3}},
			wantLine:  10,
			wantTouch: true,
		},
		{
			name:     "modification entirely before, net growth",
			old:      10,
			hunks:    []gitrepo.Hunk{{OldStart: 1, OldCount: 2, NewStart: 1, NewCount: 5}},
			wantLine: 13,
		},
		{
			name:     "deletion entirely before",
			old:      10,
			hunks:    []gitrepo.Hunk{{OldStart: 1, OldCount: 3, NewStart: 0, NewCount: 0}},
			wantLine: 7,
		},
		{
			name:     "modification entirely after has no effect",
			old:      10,
			hunks:    []gitrepo.Hunk{{OldStart: 40, OldCount: 3, NewStart: 40, NewCount: 9}},
			wantLine: 10,
		},
		{
			name: "offsets accumulate across hunks",
			old:  100,
			hunks: []gitrepo.Hunk{
				{OldStart: 0, OldCount: 0, NewStart: 1, NewCount: 2},   // +2
				{OldStart: 10, OldCount: 5, NewStart: 12, NewCount: 1}, // -4
				{OldStart: 90, OldCount: 0, NewStart: 89, NewCount: 7}, // +7
			},
			wantLine: 105,
		},
		{
			name: "touched wins even when other hunks shift",
			old:  20,
			hunks: []gitrepo.Hunk{
				{OldStart: 0, OldCount: 0, NewStart: 1, NewCount: 3},
				{OldStart: 19, OldCount: 4, NewStart: 22, NewCount: 4},
			},
			wantLine:  23,
			wantTouch: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line, touched := MapLine(tt.old, tt.hunks)
			if line != tt.wantLine {
				t.Errorf("MapLine() line = %d, want %d", line, tt.wantLine)
			}
			if touched != tt.wantTouch {
				t.Errorf("MapLine() touched = %v, want %v", touched, tt.wantTouch)
			}
		})
	}
}

func TestRangeTouched(t *testing.T) {
	tests := []struct {
		name  string
		s, e  int
		hunks []gitrepo.Hunk
		want  bool
	}{
		{"hunk on first line", 10, 20, []gitrepo.Hunk{{OldStart: 10, OldCount: 1, NewStart: 10, NewCount: 1}}, true},
		{"hunk on last line", 10, 20, []gitrepo.Hunk{{OldStart: 20, OldCount: 1, NewStart: 20, NewCount: 1}}, true},
		{"hunk containing the range", 10, 20, []gitrepo.Hunk{{OldStart: 1, OldCount: 100, NewStart: 1, NewCount: 100}}, true},
		{"hunk just above", 10, 20, []gitrepo.Hunk{{OldStart: 8, OldCount: 2, NewStart: 8, NewCount: 2}}, false},
		{"hunk just below", 10, 20, []gitrepo.Hunk{{OldStart: 21, OldCount: 1, NewStart: 21, NewCount: 1}}, false},
		{"insertion inside the range is not a touch", 10, 20, []gitrepo.Hunk{{OldStart: 15, OldCount: 0, NewStart: 16, NewCount: 3}}, false},
		{"insertion above the range is not a touch", 10, 20, []gitrepo.Hunk{{OldStart: 5, OldCount: 0, NewStart: 6, NewCount: 3}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rangeTouched(tt.s, tt.e, tt.hunks); got != tt.want {
				t.Errorf("rangeTouched(%d,%d) = %v, want %v", tt.s, tt.e, got, tt.want)
			}
		})
	}
}

// --- integration against real git repos ---

func newTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-b", "main")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "Test")
	git(t, dir, "config", "commit.gpgsign", "false")
	return dir
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitAll(t *testing.T, dir, msg string) string {
	t.Helper()
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", msg)
	return git(t, dir, "rev-parse", "HEAD")
}

const twelveLines = "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\neleven\ntwelve\n"

func setup(t *testing.T) (*gitrepo.Repo, string, string) {
	t.Helper()
	dir := newTestRepo(t)
	write(t, dir, "src/app.go", twelveLines)
	sha := commitAll(t, dir, "init")
	r, err := gitrepo.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r, dir, sha
}

func anchors(a ...anchor.Anchor) []anchor.Anchor { return a }

func ranged(path string, start, end int) anchor.Anchor {
	return anchor.Anchor{Path: path, Start: start, End: end, Lang: "go"}
}

func TestCheckClean(t *testing.T) {
	r, _, sha := setup(t)
	res, err := Check(r, sha, anchors(ranged("src/app.go", 4, 6)))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Anchors) != 1 || res.Anchors[0].Verdict != VerdictOK {
		t.Fatalf("Anchors = %#v, want one ok", res.Anchors)
	}
	if res.Anchors[0].CurrentStart != 4 {
		t.Errorf("CurrentStart = %d, want 4", res.Anchors[0].CurrentStart)
	}
	if res.Stale() {
		t.Error("a clean check must not be stale")
	}
	if res.Summary[VerdictOK] != 1 {
		t.Errorf("Summary = %#v", res.Summary)
	}
	if res.PinnedCommit != sha {
		t.Errorf("PinnedCommit = %q, want %q", res.PinnedCommit, sha)
	}
	if res.HeadCommit != sha {
		t.Errorf("HeadCommit = %q, want %q", res.HeadCommit, sha)
	}
	if res.CheckedAt == "" {
		t.Error("CheckedAt should be set")
	}
}

func TestCheckMovedNotChanged(t *testing.T) {
	r, dir, sha := setup(t)
	write(t, dir, "src/app.go", "header\nheader\n"+twelveLines)
	commitAll(t, dir, "insert two lines above")

	res, err := Check(r, sha, anchors(ranged("src/app.go", 4, 6)))
	if err != nil {
		t.Fatal(err)
	}
	got := res.Anchors[0]
	if got.Verdict != VerdictMoved {
		t.Fatalf("Verdict = %q, want moved", got.Verdict)
	}
	if got.CurrentStart != 6 {
		t.Errorf("CurrentStart = %d, want 6", got.CurrentStart)
	}
	if res.Stale() {
		t.Error("a moved anchor must not make the guide stale")
	}
}

func TestCheckChangedInsideRange(t *testing.T) {
	r, dir, sha := setup(t)
	write(t, dir, "src/app.go", strings.Replace(twelveLines, "five\n", "FIVE CHANGED\n", 1))
	commitAll(t, dir, "edit line 5")

	res, err := Check(r, sha, anchors(ranged("src/app.go", 4, 6)))
	if err != nil {
		t.Fatal(err)
	}
	if res.Anchors[0].Verdict != VerdictChanged {
		t.Fatalf("Verdict = %q, want changed", res.Anchors[0].Verdict)
	}
	if !res.Stale() {
		t.Error("a changed anchor must make the guide stale")
	}
}

func TestCheckChangeOutsideRangeIsClean(t *testing.T) {
	r, dir, sha := setup(t)
	write(t, dir, "src/app.go", strings.Replace(twelveLines, "twelve\n", "TWELVE CHANGED\n", 1))
	commitAll(t, dir, "edit the last line")

	res, err := Check(r, sha, anchors(ranged("src/app.go", 4, 6)))
	if err != nil {
		t.Fatal(err)
	}
	if res.Anchors[0].Verdict != VerdictOK {
		t.Errorf("Verdict = %q, want ok (the edit is below the anchored range)", res.Anchors[0].Verdict)
	}
}

func TestCheckBroken(t *testing.T) {
	r, dir, sha := setup(t)
	git(t, dir, "rm", "src/app.go")
	write(t, dir, "other.txt", "so the tree is not empty\n")
	commitAll(t, dir, "delete app.go")

	res, err := Check(r, sha, anchors(ranged("src/app.go", 4, 6)))
	if err != nil {
		t.Fatal(err)
	}
	if res.Anchors[0].Verdict != VerdictBroken {
		t.Fatalf("Verdict = %q, want broken", res.Anchors[0].Verdict)
	}
	if !res.Stale() {
		t.Error("a broken anchor must make the guide stale")
	}
}

func TestCheckRenamed(t *testing.T) {
	r, dir, sha := setup(t)
	if err := os.MkdirAll(filepath.Join(dir, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "mv", "src/app.go", "internal/app.go")
	commitAll(t, dir, "move app.go")

	res, err := Check(r, sha, anchors(ranged("src/app.go", 4, 6)))
	if err != nil {
		t.Fatal(err)
	}
	got := res.Anchors[0]
	if got.Verdict != VerdictRenamed {
		t.Fatalf("Verdict = %q, want renamed", got.Verdict)
	}
	if got.NewPath != "internal/app.go" {
		t.Errorf("NewPath = %q, want internal/app.go", got.NewPath)
	}
	if res.Stale() {
		t.Error("a renamed anchor must not make the guide stale")
	}
}

func TestCheckRenamedAndEditedIsChanged(t *testing.T) {
	r, dir, sha := setup(t)
	git(t, dir, "mv", "src/app.go", "src/renamed.go")
	write(t, dir, "src/renamed.go", strings.Replace(twelveLines, "five\n", "FIVE CHANGED\n", 1))
	commitAll(t, dir, "move and edit")

	res, err := Check(r, sha, anchors(ranged("src/app.go", 4, 6)))
	if err != nil {
		t.Fatal(err)
	}
	got := res.Anchors[0]
	if got.Verdict != VerdictChanged {
		t.Fatalf("Verdict = %q, want changed", got.Verdict)
	}
	if got.NewPath != "src/renamed.go" {
		t.Errorf("NewPath = %q, want src/renamed.go", got.NewPath)
	}
}

func TestCheckPathOnlyAnchor(t *testing.T) {
	r, dir, sha := setup(t)
	write(t, dir, "src/app.go", strings.Replace(twelveLines, "five\n", "FIVE CHANGED\n", 1))
	commitAll(t, dir, "edit line 5")

	res, err := Check(r, sha, anchors(anchor.Anchor{Path: "src/app.go", Lang: "go"}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Anchors[0].Verdict != VerdictOK {
		t.Errorf("Verdict = %q, want ok — a path-only anchor has no range to drift", res.Anchors[0].Verdict)
	}
}

func TestCheckAnchorPathAbsentAtPinIsBroken(t *testing.T) {
	r, _, sha := setup(t)
	res, err := Check(r, sha, anchors(ranged("does/not/exist.go", 1, 3)))
	if err != nil {
		t.Fatal(err)
	}
	if res.Anchors[0].Verdict != VerdictBroken {
		t.Errorf("Verdict = %q, want broken for an anchor path that never existed", res.Anchors[0].Verdict)
	}
}

func TestCheckUnknownPin(t *testing.T) {
	r, _, _ := setup(t)
	_, err := Check(r, "0000000000000000000000000000000000000000", anchors(ranged("src/app.go", 1, 2)))
	if !errors.Is(err, ErrUnknownPin) {
		t.Fatalf("err = %v, want ErrUnknownPin", err)
	}
}

func TestCheckNoCommonHistory(t *testing.T) {
	r, dir, sha := setup(t)
	git(t, dir, "checkout", "--orphan", "fresh")
	git(t, dir, "rm", "-rf", ".")
	write(t, dir, "unrelated.txt", "nothing in common\n")
	commitAll(t, dir, "orphan root")

	_, err := Check(r, sha, anchors(ranged("src/app.go", 1, 2)))
	if !errors.Is(err, ErrNoCommonHistory) {
		t.Fatalf("err = %v, want ErrNoCommonHistory", err)
	}
}

func TestCheckPartsAttributesAnchorsToParts(t *testing.T) {
	r, dir, sha := setup(t)
	write(t, dir, "src/app.go", strings.Replace(twelveLines, "five\n", "FIVE CHANGED\n", 1))
	commitAll(t, dir, "edit line 5")

	res, err := CheckParts(r, sha, []Part{
		{Name: "part-01.md", Anchors: anchors(ranged("src/app.go", 1, 2))},
		{Name: "part-02.md", Anchors: anchors(ranged("src/app.go", 4, 6))},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Anchors) != 2 {
		t.Fatalf("Anchors = %#v, want 2", res.Anchors)
	}
	if res.Anchors[0].Part != "part-01.md" || res.Anchors[0].Verdict != VerdictOK {
		t.Errorf("first anchor = %#v", res.Anchors[0])
	}
	if res.Anchors[1].Part != "part-02.md" || res.Anchors[1].Verdict != VerdictChanged {
		t.Errorf("second anchor = %#v", res.Anchors[1])
	}
	if res.Summary[VerdictOK] != 1 || res.Summary[VerdictChanged] != 1 {
		t.Errorf("Summary = %#v", res.Summary)
	}
	if got := res.StalePartsList(); len(got) != 1 || got[0] != "part-02.md" {
		t.Errorf("StalePartsList() = %#v, want [part-02.md]", got)
	}
}

func TestCheckRecordsDirtyTree(t *testing.T) {
	r, dir, sha := setup(t)
	write(t, dir, "src/app.go", "uncommitted\n"+twelveLines)

	res, err := Check(r, sha, anchors(ranged("src/app.go", 4, 6)))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Dirty {
		t.Error("Dirty should be true when the working tree has uncommitted changes")
	}
	// Drift is computed against HEAD, so the uncommitted edit must not register.
	if res.Anchors[0].Verdict != VerdictOK {
		t.Errorf("Verdict = %q, want ok — uncommitted work is not drift", res.Anchors[0].Verdict)
	}
}
