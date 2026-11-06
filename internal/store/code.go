package store

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// CodeDirName is the subdirectory of a stored tutorial that holds the code
// snapshot: a git repository the /lathe-verify skill built while following the
// tutorial, with one commit per part, each tagged after its part (part-01,
// part-02, ...). It is written only by SaveCode (via `lathe verify-result
// --code`), never by a skill directly.
const CodeDirName = "code"

// PartTag is the git tag that marks the code as of a part: the part's filename
// without its .md suffix ("part-04.md" → "part-04").
func PartTag(part string) string {
	return strings.TrimSuffix(part, ".md")
}

// SaveCode replaces a tutorial's code snapshot with a clone of the git
// repository at srcDir. The verify skill reconstructs the tutorial in a scratch
// dir and commits + tags each part as it passes; this keeps that history so a
// reader can check out or diff the code at any part after the scratch dir is
// gone.
//
// Cloning (rather than copying the directory) stores only committed content, so
// untracked build output in the scratch dir is left behind, and every part tag
// comes along. The clone is built beside the destination and swapped in only
// once it is complete, so a failed save leaves any previous snapshot intact.
// srcDir must contain at least one part-NN tag.
func SaveCode(tutDir, srcDir string) error {
	src, err := filepath.Abs(srcDir)
	if err != nil {
		return err
	}
	if info, err := os.Stat(filepath.Join(src, ".git")); err != nil || !info.IsDir() {
		return fmt.Errorf("%s is not a git repository (no .git directory)", srcDir)
	}

	tmp, err := os.MkdirTemp(tutDir, ".code-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	repo := filepath.Join(tmp, "repo")

	if err := runGit("", "clone", "--quiet", "--no-hardlinks", src, repo); err != nil {
		return err
	}
	// The origin remote points at the throwaway scratch dir; drop it so the
	// snapshot doesn't carry a dangling remote.
	if err := runGit(repo, "remote", "remove", "origin"); err != nil {
		return err
	}
	if len(CodeTags(repo)) == 0 {
		return fmt.Errorf("%s has no part-NN tags; tag each part's commit (git tag part-01) before saving", srcDir)
	}

	dest := filepath.Join(tutDir, CodeDirName)
	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("remove previous snapshot: %w", err)
	}
	return os.Rename(repo, dest)
}

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("git %s: %s", args[0], msg)
	}
	return nil
}

// CodeTags returns the sorted part-NN tags in the git repository at repoDir, or
// nil when it isn't a repository or has none. It reads the refs on disk (both
// loose refs and packed-refs, which a fresh clone writes) rather than shelling
// out, so the server can call it on every page render without git installed.
func CodeTags(repoDir string) []string {
	gitDir := filepath.Join(repoDir, ".git")
	seen := map[string]bool{}

	if entries, err := os.ReadDir(filepath.Join(gitDir, "refs", "tags")); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				seen[e.Name()] = true
			}
		}
	}
	if f, err := os.Open(filepath.Join(gitDir, "packed-refs")); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			// "<sha> refs/tags/<name>"; skip comments and "^<sha>" peel lines.
			_, ref, ok := strings.Cut(sc.Text(), " ")
			if name, found := strings.CutPrefix(ref, "refs/tags/"); ok && found {
				seen[name] = true
			}
		}
		_ = f.Close()
	}

	var tags []string
	for name := range seen {
		if strings.HasPrefix(name, "part-") {
			tags = append(tags, name)
		}
	}
	sort.Strings(tags)
	return tags
}
