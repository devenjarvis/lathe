// Package gitrepo is a thin, dependency-free wrapper over the `git` binary.
//
// It knows nothing about anchors, tutorials, or ~/.lathe — it answers plain git
// questions: what is origin, what is HEAD, does this commit exist, and how did a
// single file change between a commit and HEAD. Everything is shelled out via
// os/exec rather than a git library, keeping go.mod as small as it already is.
package gitrepo

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// ErrPathNotAtCommit is returned by FileDiff when the requested path does not
// exist at the pinned commit. That is an authoring error (the anchor names a
// file the guide's own pin never contained), not drift, so callers surface it
// distinctly rather than reporting the file as deleted.
var ErrPathNotAtCommit = errors.New("path does not exist at the given commit")

// FileStatus is how a single file changed between the pinned commit and HEAD.
type FileStatus string

const (
	FileUnchanged FileStatus = "unchanged"
	FileModified  FileStatus = "modified"
	FileDeleted   FileStatus = "deleted"
	FileRenamed   FileStatus = "renamed"
)

// Hunk is one `@@ -OldStart,OldCount +NewStart,NewCount @@` header from a
// --unified=0 diff. OldCount == 0 means a pure insertion *after* old line
// OldStart (git writes OldStart == 0 for an insertion before the first line);
// NewCount == 0 means a pure deletion.
type Hunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
}

// FileDiff is the change to one file between a pinned commit and HEAD. NewPath
// is the file's path at HEAD (equal to the requested path unless it was
// renamed); Hunks is empty for an unchanged file, a deleted file, and a pure
// rename.
type FileDiff struct {
	Status  FileStatus
	NewPath string
	Hunks   []Hunk
}

// Repo is an opened git working tree, identified by its toplevel directory.
type Repo struct {
	dir string

	// nameStatus caches the whole-tree name-status diff per pinned commit.
	// Rename detection needs to see the full change set, so it cannot be run
	// per-file with a pathspec; caching keeps one `git diff` per drift run
	// rather than one per anchored file.
	mu         sync.Mutex
	nameStatus map[string]map[string]changedFile
}

type changedFile struct {
	status  FileStatus
	newPath string
}

// Open resolves dir to the toplevel of the git working tree containing it.
func Open(dir string) (*Repo, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("git binary not found in PATH: %w", err)
	}
	out, err := runGit(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("%s is not inside a git repository: %w", dir, err)
	}
	return &Repo{dir: out, nameStatus: map[string]map[string]changedFile{}}, nil
}

// Dir returns the toplevel directory of the working tree.
func (r *Repo) Dir() string { return r.dir }

// OriginURL returns the URL of the "origin" remote. It errors when the repo has
// no origin — callers treat that as "cannot match this repo to a tutorial".
func (r *Repo) OriginURL() (string, error) {
	out, err := runGit(r.dir, "remote", "get-url", "origin")
	if err != nil {
		return "", fmt.Errorf("read origin remote: %w", err)
	}
	return out, nil
}

// HeadSHA returns the full SHA of HEAD.
func (r *Repo) HeadSHA() (string, error) {
	out, err := runGit(r.dir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve HEAD: %w", err)
	}
	return out, nil
}

// HasCommit reports whether sha names a commit object present in this repo. It
// is the guard against reporting false drift in a shallow clone, after a
// rebase, or after a GC dropped the pinned commit.
func (r *Repo) HasCommit(sha string) bool {
	if strings.TrimSpace(sha) == "" {
		return false
	}
	_, err := runGit(r.dir, "cat-file", "-e", sha+"^{commit}")
	return err == nil
}

// IsDirty reports whether the working tree has uncommitted changes. Drift is
// computed against HEAD, so a dirty tree means the answer describes committed
// state only — recorded on the result so the UI can say so.
func (r *Repo) IsDirty() (bool, error) {
	out, err := runGit(r.dir, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("read working tree status: %w", err)
	}
	return out != "", nil
}

// MergeBase returns the best common ancestor of a and b. It errors when the two
// commits share no history (an orphan branch, an unrelated repo), which is the
// other case where drift is unknowable rather than clean or stale.
func (r *Repo) MergeBase(a, b string) (string, error) {
	out, err := runGit(r.dir, "merge-base", a, b)
	if err != nil {
		return "", fmt.Errorf("no common ancestor between %s and %s: %w", a, b, err)
	}
	return out, nil
}

// FileDiff describes how path (as it existed at fromSHA) changed by HEAD.
func (r *Repo) FileDiff(fromSHA, path string) (FileDiff, error) {
	if _, err := runGit(r.dir, "cat-file", "-e", fromSHA+":"+path); err != nil {
		return FileDiff{}, fmt.Errorf("%q at %s: %w", path, shortSHA(fromSHA), ErrPathNotAtCommit)
	}

	changed, err := r.changedFiles(fromSHA)
	if err != nil {
		return FileDiff{}, err
	}

	entry, ok := changed[path]
	if !ok {
		// Absent from the change set means the file is byte-identical at HEAD.
		return FileDiff{Status: FileUnchanged, NewPath: path}, nil
	}
	if entry.status == FileDeleted {
		return FileDiff{Status: FileDeleted}, nil
	}

	newPath := entry.newPath
	if newPath == "" {
		newPath = path
	}
	hunks, err := r.blobHunks(fromSHA, path, newPath)
	if err != nil {
		return FileDiff{}, err
	}
	return FileDiff{Status: entry.status, NewPath: newPath, Hunks: hunks}, nil
}

// changedFiles returns the whole-tree name-status diff from fromSHA to HEAD,
// keyed by the path as it existed at fromSHA. Rename detection runs over the
// full change set (a pathspec-limited diff can only see the delete half of a
// rename), so this is computed once per pinned commit and cached.
func (r *Repo) changedFiles(fromSHA string) (map[string]changedFile, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cached, ok := r.nameStatus[fromSHA]; ok {
		return cached, nil
	}
	// -z keeps paths raw (git otherwise C-quotes anything non-ASCII or with
	// spaces), so no unquoting step is needed.
	//
	// --find-renames uses git's own similarity heuristic at its default 50%
	// threshold, deliberately un-tuned. A file that moved *and* was edited past
	// recognition scores below it and comes back as delete+add, which classifies
	// as `broken` rather than `renamed` — the honest answer, since an excerpt
	// whose file changed that much is genuinely stale. Lowering the threshold
	// would trade that for the worse failure: confidently mapping an anchored
	// range into a file that only vaguely resembles the original.
	out, err := runGit(r.dir, "diff", "--name-status", "--find-renames", "-z", fromSHA, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("diff %s..HEAD: %w", shortSHA(fromSHA), err)
	}
	changed := parseNameStatus(out)
	r.nameStatus[fromSHA] = changed
	return changed, nil
}

// parseNameStatus decodes NUL-separated `git diff --name-status -z` output.
// Every record is a status token followed by one path, except renames and
// copies (R/C), which are followed by the old path and then the new path.
func parseNameStatus(out string) map[string]changedFile {
	changed := map[string]changedFile{}
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); {
		code := fields[i]
		if code == "" {
			i++
			continue
		}
		switch code[0] {
		case 'R', 'C':
			if i+2 >= len(fields) {
				return changed
			}
			changed[fields[i+1]] = changedFile{status: FileRenamed, newPath: fields[i+2]}
			i += 3
		case 'D':
			if i+1 >= len(fields) {
				return changed
			}
			changed[fields[i+1]] = changedFile{status: FileDeleted}
			i += 2
		default: // A, M, T, U, X and friends all read as "content differs"
			if i+1 >= len(fields) {
				return changed
			}
			changed[fields[i+1]] = changedFile{status: FileModified}
			i += 2
		}
	}
	return changed
}

// blobHunks diffs the two blobs directly (rev:path form) rather than diffing
// the commits with a pathspec, so a renamed file's old and new contents are
// compared even though their paths differ.
func (r *Repo) blobHunks(fromSHA, oldPath, newPath string) ([]Hunk, error) {
	out, err := runGit(r.dir, "diff", "--unified=0", fromSHA+":"+oldPath, "HEAD:"+newPath)
	if err != nil {
		return nil, fmt.Errorf("diff %s:%s..HEAD:%s: %w", shortSHA(fromSHA), oldPath, newPath, err)
	}
	var hunks []Hunk
	for _, line := range strings.Split(out, "\n") {
		if h, ok := parseHunkHeader(line); ok {
			hunks = append(hunks, h)
		}
	}
	return hunks, nil
}

// parseHunkHeader decodes "@@ -a[,b] +c[,d] @@ [context]". An omitted count
// means 1, per the unified diff format.
func parseHunkHeader(line string) (Hunk, bool) {
	if !strings.HasPrefix(line, "@@ ") {
		return Hunk{}, false
	}
	rest := line[len("@@ "):]
	end := strings.Index(rest, " @@")
	if end == -1 {
		return Hunk{}, false
	}
	parts := strings.Fields(rest[:end])
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "-") || !strings.HasPrefix(parts[1], "+") {
		return Hunk{}, false
	}
	oldStart, oldCount, ok := parseRange(parts[0][1:])
	if !ok {
		return Hunk{}, false
	}
	newStart, newCount, ok := parseRange(parts[1][1:])
	if !ok {
		return Hunk{}, false
	}
	return Hunk{OldStart: oldStart, OldCount: oldCount, NewStart: newStart, NewCount: newCount}, true
}

func parseRange(s string) (start, count int, ok bool) {
	startStr, countStr, hasCount := strings.Cut(s, ",")
	start, err := strconv.Atoi(startStr)
	if err != nil || start < 0 {
		return 0, 0, false
	}
	if !hasCount {
		return start, 1, true
	}
	count, err = strconv.Atoi(countStr)
	if err != nil || count < 0 {
		return 0, 0, false
	}
	return start, count, true
}

// runGit executes git in dir and returns trimmed stdout. Stderr is folded into
// the error so a caller's message names the actual git complaint.
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return "", err
		}
		return "", fmt.Errorf("%w: %s", err, msg)
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
