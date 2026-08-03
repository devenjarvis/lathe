package gitrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newTestRepo builds a throwaway git repo in t.TempDir() with deterministic
// identity and no dependence on the developer's global git config.
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
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
		"GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
	)
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

func lines(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString("line ")
		b.WriteString(string(rune('0' + i%10)))
		b.WriteByte('\n')
	}
	return b.String()
}

func commitAll(t *testing.T, dir, msg string) string {
	t.Helper()
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", msg)
	return git(t, dir, "rev-parse", "HEAD")
}

func TestOpenRejectsNonRepo(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err == nil {
		t.Fatal("Open() on a non-git dir should error")
	}
}

func TestOpenResolvesToToplevel(t *testing.T) {
	dir := newTestRepo(t)
	write(t, dir, "sub/file.txt", "hi\n")
	commitAll(t, dir, "init")

	r, err := Open(filepath.Join(dir, "sub"))
	if err != nil {
		t.Fatalf("Open(subdir): %v", err)
	}
	// macOS temp dirs are symlinked (/var -> /private/var), so compare resolved paths.
	want, _ := filepath.EvalSymlinks(dir)
	got, _ := filepath.EvalSymlinks(r.Dir())
	if got != want {
		t.Errorf("Dir() = %q, want the toplevel %q", got, want)
	}
}

func TestOriginURLAndHeadSHA(t *testing.T) {
	dir := newTestRepo(t)
	write(t, dir, "a.txt", "hi\n")
	sha := commitAll(t, dir, "init")
	git(t, dir, "remote", "add", "origin", "git@github.com:devenjarvis/lathe.git")

	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	origin, err := r.OriginURL()
	if err != nil {
		t.Fatalf("OriginURL: %v", err)
	}
	if origin != "git@github.com:devenjarvis/lathe.git" {
		t.Errorf("OriginURL() = %q", origin)
	}
	head, err := r.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	if head != sha {
		t.Errorf("HeadSHA() = %q, want %q", head, sha)
	}
}

func TestOriginURLMissingRemote(t *testing.T) {
	dir := newTestRepo(t)
	write(t, dir, "a.txt", "hi\n")
	commitAll(t, dir, "init")
	r, _ := Open(dir)
	if _, err := r.OriginURL(); err == nil {
		t.Fatal("OriginURL() with no origin should error")
	}
}

func TestHasCommit(t *testing.T) {
	dir := newTestRepo(t)
	write(t, dir, "a.txt", "hi\n")
	sha := commitAll(t, dir, "init")
	r, _ := Open(dir)

	if !r.HasCommit(sha) {
		t.Error("HasCommit(HEAD) = false")
	}
	if r.HasCommit("0000000000000000000000000000000000000000") {
		t.Error("HasCommit(bogus) = true")
	}
	if r.HasCommit("") {
		t.Error("HasCommit(\"\") = true")
	}
}

func TestIsDirty(t *testing.T) {
	dir := newTestRepo(t)
	write(t, dir, "a.txt", "hi\n")
	commitAll(t, dir, "init")
	r, _ := Open(dir)

	dirty, err := r.IsDirty()
	if err != nil {
		t.Fatal(err)
	}
	if dirty {
		t.Error("IsDirty() = true on a clean tree")
	}

	write(t, dir, "a.txt", "changed\n")
	dirty, err = r.IsDirty()
	if err != nil {
		t.Fatal(err)
	}
	if !dirty {
		t.Error("IsDirty() = false after modifying a tracked file")
	}
}

func TestMergeBase(t *testing.T) {
	dir := newTestRepo(t)
	write(t, dir, "a.txt", "hi\n")
	base := commitAll(t, dir, "init")
	write(t, dir, "b.txt", "there\n")
	commitAll(t, dir, "second")

	r, _ := Open(dir)
	got, err := r.MergeBase(base, "HEAD")
	if err != nil {
		t.Fatalf("MergeBase: %v", err)
	}
	if got != base {
		t.Errorf("MergeBase = %q, want %q", got, base)
	}

	// An orphan branch shares no history — the pinned commit still exists, but
	// there is nothing to diff against meaningfully.
	git(t, dir, "checkout", "--orphan", "fresh")
	git(t, dir, "rm", "-rf", ".")
	write(t, dir, "c.txt", "unrelated\n")
	commitAll(t, dir, "orphan root")
	if _, err := r.MergeBase(base, "HEAD"); err == nil {
		t.Error("MergeBase across unrelated histories should error")
	}
}

func TestFileDiffUnchanged(t *testing.T) {
	dir := newTestRepo(t)
	write(t, dir, "a.txt", lines(10))
	sha := commitAll(t, dir, "init")
	write(t, dir, "b.txt", "other\n")
	commitAll(t, dir, "unrelated change")

	r, _ := Open(dir)
	fd, err := r.FileDiff(sha, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if fd.Status != FileUnchanged {
		t.Errorf("Status = %q, want unchanged", fd.Status)
	}
	if len(fd.Hunks) != 0 {
		t.Errorf("Hunks = %#v, want none", fd.Hunks)
	}
	if fd.NewPath != "a.txt" {
		t.Errorf("NewPath = %q, want a.txt", fd.NewPath)
	}
}

func TestFileDiffInsertionAbove(t *testing.T) {
	dir := newTestRepo(t)
	write(t, dir, "a.txt", lines(10))
	sha := commitAll(t, dir, "init")
	write(t, dir, "a.txt", "new 1\nnew 2\n"+lines(10))
	commitAll(t, dir, "insert two lines at the top")

	r, _ := Open(dir)
	fd, err := r.FileDiff(sha, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if fd.Status != FileModified {
		t.Fatalf("Status = %q, want modified", fd.Status)
	}
	if len(fd.Hunks) != 1 {
		t.Fatalf("Hunks = %#v, want 1", fd.Hunks)
	}
	h := fd.Hunks[0]
	// A pure insertion carries OldCount == 0 — this is the arithmetic the whole
	// drift classifier hangs on, so pin the exact numbers.
	if h.OldCount != 0 {
		t.Errorf("OldCount = %d, want 0 for a pure insertion", h.OldCount)
	}
	if h.OldStart != 0 {
		t.Errorf("OldStart = %d, want 0 (insertion before old line 1)", h.OldStart)
	}
	if h.NewStart != 1 || h.NewCount != 2 {
		t.Errorf("new side = %d,%d want 1,2", h.NewStart, h.NewCount)
	}
}

func TestFileDiffInsertionMidFile(t *testing.T) {
	dir := newTestRepo(t)
	write(t, dir, "a.txt", "a\nb\nc\nd\n")
	sha := commitAll(t, dir, "init")
	write(t, dir, "a.txt", "a\nb\nINSERTED\nc\nd\n")
	commitAll(t, dir, "insert after line 2")

	r, _ := Open(dir)
	fd, err := r.FileDiff(sha, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(fd.Hunks) != 1 {
		t.Fatalf("Hunks = %#v, want 1", fd.Hunks)
	}
	h := fd.Hunks[0]
	if h.OldStart != 2 || h.OldCount != 0 {
		t.Errorf("old side = %d,%d want 2,0 (insertion after old line 2)", h.OldStart, h.OldCount)
	}
	if h.NewStart != 3 || h.NewCount != 1 {
		t.Errorf("new side = %d,%d want 3,1", h.NewStart, h.NewCount)
	}
}

func TestFileDiffModificationInside(t *testing.T) {
	dir := newTestRepo(t)
	write(t, dir, "a.txt", "a\nb\nc\nd\ne\n")
	sha := commitAll(t, dir, "init")
	write(t, dir, "a.txt", "a\nb\nCHANGED\nd\ne\n")
	commitAll(t, dir, "change line 3")

	r, _ := Open(dir)
	fd, err := r.FileDiff(sha, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(fd.Hunks) != 1 {
		t.Fatalf("Hunks = %#v, want 1", fd.Hunks)
	}
	h := fd.Hunks[0]
	if h.OldStart != 3 || h.OldCount != 1 || h.NewStart != 3 || h.NewCount != 1 {
		t.Errorf("hunk = %#v, want -3,1 +3,1", h)
	}
}

func TestFileDiffDeletion(t *testing.T) {
	dir := newTestRepo(t)
	write(t, dir, "a.txt", lines(5))
	write(t, dir, "keep.txt", "keep\n")
	sha := commitAll(t, dir, "init")
	git(t, dir, "rm", "a.txt")
	commitAll(t, dir, "delete a.txt")

	r, _ := Open(dir)
	fd, err := r.FileDiff(sha, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if fd.Status != FileDeleted {
		t.Errorf("Status = %q, want deleted", fd.Status)
	}
}

func TestFileDiffRename(t *testing.T) {
	dir := newTestRepo(t)
	write(t, dir, "a.txt", lines(20))
	sha := commitAll(t, dir, "init")
	if err := os.MkdirAll(filepath.Join(dir, "moved"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "mv", "a.txt", "moved/b.txt")
	commitAll(t, dir, "move a.txt")

	r, _ := Open(dir)
	fd, err := r.FileDiff(sha, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if fd.Status != FileRenamed {
		t.Fatalf("Status = %q, want renamed", fd.Status)
	}
	if fd.NewPath != "moved/b.txt" {
		t.Errorf("NewPath = %q, want moved/b.txt", fd.NewPath)
	}
	if len(fd.Hunks) != 0 {
		t.Errorf("a pure rename should have no hunks, got %#v", fd.Hunks)
	}
}

func TestFileDiffRenameWithEdit(t *testing.T) {
	dir := newTestRepo(t)
	write(t, dir, "a.txt", "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\n")
	sha := commitAll(t, dir, "init")
	git(t, dir, "mv", "a.txt", "b.txt")
	write(t, dir, "b.txt", "a\nb\nCHANGED\nd\ne\nf\ng\nh\ni\nj\nk\nl\n")
	commitAll(t, dir, "move and edit")

	r, _ := Open(dir)
	fd, err := r.FileDiff(sha, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if fd.Status != FileRenamed {
		t.Fatalf("Status = %q, want renamed", fd.Status)
	}
	if fd.NewPath != "b.txt" {
		t.Errorf("NewPath = %q, want b.txt", fd.NewPath)
	}
	if len(fd.Hunks) != 1 {
		t.Fatalf("Hunks = %#v, want 1", fd.Hunks)
	}
	if fd.Hunks[0].OldStart != 3 {
		t.Errorf("hunk = %#v, want old start 3", fd.Hunks[0])
	}
}

func TestFileDiffPathMissingAtPin(t *testing.T) {
	dir := newTestRepo(t)
	write(t, dir, "a.txt", "hi\n")
	sha := commitAll(t, dir, "init")
	write(t, dir, "later.txt", "added later\n")
	commitAll(t, dir, "add later.txt")

	r, _ := Open(dir)
	if _, err := r.FileDiff(sha, "later.txt"); err == nil {
		t.Fatal("FileDiff on a path absent at the pinned commit should error")
	}
	if _, err := r.FileDiff(sha, "never-existed.txt"); err == nil {
		t.Fatal("FileDiff on a nonexistent path should error")
	}
}

func TestParseHunkHeader(t *testing.T) {
	tests := []struct {
		line string
		want Hunk
		ok   bool
	}{
		{"@@ -0,0 +1,2 @@", Hunk{OldStart: 0, OldCount: 0, NewStart: 1, NewCount: 2}, true},
		{"@@ -3 +3 @@", Hunk{OldStart: 3, OldCount: 1, NewStart: 3, NewCount: 1}, true},
		{"@@ -10,5 +10,0 @@", Hunk{OldStart: 10, OldCount: 5, NewStart: 10, NewCount: 0}, true},
		{"@@ -1,2 +3,4 @@ func foo() {", Hunk{OldStart: 1, OldCount: 2, NewStart: 3, NewCount: 4}, true},
		{"not a hunk", Hunk{}, false},
		{"@@ bad @@", Hunk{}, false},
	}
	for _, tt := range tests {
		got, ok := parseHunkHeader(tt.line)
		if ok != tt.ok {
			t.Errorf("parseHunkHeader(%q) ok = %v, want %v", tt.line, ok, tt.ok)
			continue
		}
		if ok && got != tt.want {
			t.Errorf("parseHunkHeader(%q) = %#v, want %#v", tt.line, got, tt.want)
		}
	}
}
