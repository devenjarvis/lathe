package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ChecksFile is the per-tutorial log of "Check my work" results: one JSON
// object per line, appended each time a learner's code is checked against a
// part. Like progress.json it is local reader state, never part of
// metadata.json, and it is dropped on re-store (the results describe the old
// parts). It is kept as a log, not just the latest result, so later features
// (adaptive parts) can see how a learner got there.
const ChecksFile = "checks.jsonl"

// CheckStatus is the outcome of running a part's checkpoint against the
// learner's code.
type CheckStatus string

const (
	// CheckPass: the checkpoint behaves as the part says.
	CheckPass CheckStatus = "pass"
	// CheckFail: it doesn't; Findings say where to look.
	CheckFail CheckStatus = "fail"
	// CheckSkipped: the checkpoint could not be run here (a required tool is
	// missing), so the code was only read, not run.
	CheckSkipped CheckStatus = "skipped"
)

// Check confidence: whether the checker had the verified code snapshot's
// diff for the part to compare against, or only the part's text.
const (
	CheckConfidenceReference = "reference"
	CheckConfidenceText      = "text"
)

// CheckFinding is one problem in the learner's code, as a hint ladder: each
// rung gives away a little more. Nudge is required; Reveal (the reference
// code) is shown only when the learner asks for it.
type CheckFinding struct {
	// File is the learner's file the finding is about, relative to their
	// workspace (optional: some problems are about a missing file).
	File string `json:"file,omitempty"`
	// Nudge: what to look at again, without saying what is wrong.
	Nudge string `json:"nudge"`
	// Pointer: the step or side-note of the part that covers it.
	Pointer string `json:"pointer,omitempty"`
	// Where: the file and roughly where in it.
	Where string `json:"where,omitempty"`
	// Reveal: the reference code (markdown), behind an explicit request.
	Reveal string `json:"reveal,omitempty"`
}

// CheckDivergence is a place where passing code differs from the reference:
// a choice, not a mistake.
type CheckDivergence struct {
	File string `json:"file,omitempty"`
	Note string `json:"note"`
	// LaterPart names a later part that relies on the reference shape, when
	// one does, so the learner knows the choice will cost them there.
	LaterPart string `json:"later_part,omitempty"`
}

// CheckResult is one "Check my work" result.
type CheckResult struct {
	Part   string      `json:"part"`
	Status CheckStatus `json:"status"`
	// Confidence is "reference" when the part's diff from the code snapshot
	// guided the check, "text" when only the part's text did.
	Confidence string `json:"confidence"`
	// Summary is one or two sentences for the top of the result.
	Summary string `json:"summary,omitempty"`
	// OnPart is the part the learner's code matches best, when it is not
	// Part (they are ahead or behind).
	OnPart      string            `json:"on_part,omitempty"`
	Findings    []CheckFinding    `json:"findings,omitempty"`
	Divergences []CheckDivergence `json:"divergences,omitempty"`
	CheckedAt   string            `json:"checked_at"`
}

// Time parses CheckedAt (zero when it doesn't parse).
func (c *CheckResult) Time() time.Time {
	t, _ := time.Parse(time.RFC3339Nano, c.CheckedAt)
	return t
}

// normalize trims the result's text and fills defaults, and reports the first
// problem with it. parts is the tutorial's part list.
func (c *CheckResult) normalize(parts []string, hasSnapshot func(part string) bool) error {
	c.Part = strings.TrimSpace(c.Part)
	if c.Part == "" {
		return errors.New("part is required")
	}
	if !contains(parts, c.Part) {
		return fmt.Errorf("unknown part %q (the tutorial's parts are %s)", c.Part, strings.Join(parts, ", "))
	}
	c.OnPart = strings.TrimSpace(c.OnPart)
	if c.OnPart == c.Part {
		c.OnPart = ""
	}
	if c.OnPart != "" && !contains(parts, c.OnPart) {
		return fmt.Errorf("unknown on_part %q", c.OnPart)
	}
	switch c.Status {
	case CheckPass, CheckFail, CheckSkipped:
	default:
		return fmt.Errorf("status must be pass, fail or skipped, not %q", c.Status)
	}
	c.Confidence = strings.TrimSpace(c.Confidence)
	switch c.Confidence {
	case CheckConfidenceReference, CheckConfidenceText:
	case "":
		c.Confidence = CheckConfidenceText
		if hasSnapshot(c.Part) {
			c.Confidence = CheckConfidenceReference
		}
	default:
		return fmt.Errorf("confidence must be reference or text, not %q", c.Confidence)
	}
	c.Summary = strings.TrimSpace(c.Summary)
	for i := range c.Findings {
		f := &c.Findings[i]
		f.File = strings.TrimSpace(f.File)
		f.Nudge = strings.TrimSpace(f.Nudge)
		f.Pointer = strings.TrimSpace(f.Pointer)
		f.Where = strings.TrimSpace(f.Where)
		f.Reveal = strings.TrimSpace(f.Reveal)
		if f.Nudge == "" {
			return fmt.Errorf("finding %d needs a nudge (the first, gentlest hint)", i+1)
		}
	}
	if c.Status == CheckFail && len(c.Findings) == 0 {
		return errors.New("a failing check needs at least one finding")
	}
	for i := range c.Divergences {
		d := &c.Divergences[i]
		d.File = strings.TrimSpace(d.File)
		d.Note = strings.TrimSpace(d.Note)
		d.LaterPart = strings.TrimSpace(d.LaterPart)
		if d.Note == "" {
			return fmt.Errorf("divergence %d needs a note", i+1)
		}
		if d.LaterPart != "" && !contains(parts, d.LaterPart) {
			return fmt.Errorf("divergence %d: unknown later_part %q", i+1, d.LaterPart)
		}
	}
	if strings.TrimSpace(c.CheckedAt) == "" {
		c.CheckedAt = time.Now().UTC().Format(time.RFC3339Nano)
	} else if _, err := time.Parse(time.RFC3339Nano, c.CheckedAt); err != nil {
		return fmt.Errorf("checked_at must be an RFC3339 timestamp: %w", err)
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// RecordCheck validates a check result against the tutorial and appends it
// to checks.jsonl. It is the one write path for check results, shared by
// `lathe check-result` and the lathe_check_result MCP tool.
func RecordCheck(tutorialDir string, c *CheckResult) error {
	tut, err := ReadMetadata(tutorialDir)
	if err != nil {
		return err
	}
	parts := tut.Parts
	if len(parts) == 0 {
		parts = []string{"index.md"}
	}
	tags := map[string]bool{}
	for _, t := range CodeTags(filepath.Join(tutorialDir, CodeDirName)) {
		tags[t] = true
	}
	if err := c.normalize(parts, func(p string) bool { return tags[PartTag(p)] }); err != nil {
		return err
	}
	line, err := json.Marshal(c)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(tutorialDir, ChecksFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// ReadChecks returns every recorded check, oldest first. Lines that don't
// parse are skipped. A missing file is no checks, not an error.
func ReadChecks(tutorialDir string) ([]CheckResult, error) {
	f, err := os.Open(filepath.Join(tutorialDir, ChecksFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []CheckResult
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var c CheckResult
		if json.Unmarshal(line, &c) == nil && c.Part != "" {
			out = append(out, c)
		}
	}
	return out, sc.Err()
}

// LatestCheck returns the newest check recorded for part, or nil.
func LatestCheck(tutorialDir, part string) *CheckResult {
	all, err := ReadChecks(tutorialDir)
	if err != nil {
		return nil
	}
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Part == part {
			c := all[i]
			return &c
		}
	}
	return nil
}

// Limits on the scratch copy of a learner's workspace, so a check never
// fills the disk copying a huge tree.
const (
	maxCopyFiles = 100_000
	maxCopyBytes = 2 << 30
)

// CopyWorkspace copies the learner's workspace at src into dst (which must
// exist), so a check can build and run their code without touching their
// files. Directories, regular files (with their permissions) and .git come
// along. A symlink is recreated only when it is relative and stays inside the
// workspace — then it points inside the copy too; any other symlink, and
// anything that isn't a file, directory or link, is left out, so nothing in
// the copy leads back into the learner's folder or elsewhere.
func CopyWorkspace(src, dst string) error {
	root, err := filepath.EvalSymlinks(src)
	if err != nil {
		return err
	}
	if info, err := os.Stat(root); err != nil {
		return err
	} else if !info.IsDir() {
		return fmt.Errorf("%s is not a folder", src)
	}
	var files int
	var size int64
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			if rel == "." {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case d.Type()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil || filepath.IsAbs(link) {
				return nil
			}
			if !within(filepath.Join(filepath.Dir(path), link), root) {
				return nil
			}
			return os.Symlink(link, target)
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			files++
			size += info.Size()
			if files > maxCopyFiles || size > maxCopyBytes {
				return fmt.Errorf("the workspace is too big to copy for a check (over %d files or %d GiB)", maxCopyFiles, maxCopyBytes>>30)
			}
			return copyFileMode(path, target, info.Mode().Perm())
		default:
			return nil
		}
	})
}

func copyFileMode(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm|0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// emptyTree is git's well-known empty tree, the base for the first part's diff.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// maxPartDiff caps the reference diff Lathe puts in a check prompt.
const maxPartDiff = 48 << 10

// PartDiff returns what part changed in the verified code snapshot: the git
// diff from the previous part's tag (or from nothing, for the first tagged
// part) to this part's tag. ok is false when the snapshot has no tag for the
// part (no verify run has saved code for it). A diff over maxPartDiff is cut,
// with a note saying so.
func PartDiff(tutorialDir string, parts []string, part string) (diff string, ok bool) {
	repo := filepath.Join(tutorialDir, CodeDirName)
	saved := map[string]bool{}
	for _, t := range CodeTags(repo) {
		saved[t] = true
	}
	tag := PartTag(part)
	if !saved[tag] {
		return "", false
	}
	base := emptyTree
	for i, p := range parts {
		if p == part && i > 0 && saved[PartTag(parts[i-1])] {
			base = PartTag(parts[i-1])
		}
	}
	cmd := exec.Command("git", "-C", repo, "diff", "--no-color", "--no-ext-diff", base, tag)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	if len(out) > maxPartDiff {
		out = append(out[:maxPartDiff], []byte("\n… (diff cut at 48 KiB; run the git diff yourself for the rest)\n")...)
	}
	return string(out), true
}

// CheckpointSection returns a part's "## Checkpoint" section (heading
// included) from its markdown, or "" when it has none. Fenced code is
// skipped when looking for the next heading, so a "## " inside a code block
// doesn't end the section.
func CheckpointSection(md []byte) string {
	lines := strings.Split(string(md), "\n")
	start, end := -1, len(lines)
	fence := ""
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if fence != "" {
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = t[:3]
			continue
		}
		isH := strings.HasPrefix(l, "# ") || strings.HasPrefix(l, "## ")
		if start < 0 {
			if strings.HasPrefix(strings.ToLower(l), "## checkpoint") {
				start = i
			}
			continue
		}
		if isH {
			end = i
			break
		}
	}
	if start < 0 {
		return ""
	}
	return strings.TrimSpace(strings.Join(lines[start:end], "\n"))
}
