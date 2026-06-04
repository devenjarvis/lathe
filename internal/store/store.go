package store

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/devenjarvis/lathe/internal/config"
)

// Store copies a tutorial directory into ~/.lathe/tutorials/ and writes its
// metadata with status=unverified. Verification is opt-in and never auto-runs
// here — the user triggers it separately via the /lathe-verify skill.
func Store(srcPath string) (*Tutorial, error) {
	slug := filepath.Base(strings.TrimSuffix(srcPath, string(filepath.Separator)))
	// The generation skill writes to /tmp/lathe-<slug>/ (the "lathe-" prefix
	// namespaces the temp dir). Strip it so the prefix doesn't leak into the
	// stored slug — and from there into the derived title.
	slug = strings.TrimPrefix(slug, "lathe-")

	tutorialsDir, err := config.TutorialsDir()
	if err != nil {
		return nil, err
	}

	destDir := filepath.Join(tutorialsDir, slug)
	if err := copyDir(srcPath, destDir); err != nil {
		return nil, fmt.Errorf("copy tutorial: %w", err)
	}

	parts := detectParts(destDir)

	t := &Tutorial{
		Slug:    slug,
		Title:   SlugToTitle(slug),
		Topic:   slug,
		Created: time.Now().UTC(),
		Status:  StatusUnverified,
		Parts:   parts,
	}

	if err := WriteMetadata(destDir, t); err != nil {
		return nil, err
	}

	return t, nil
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if err := copyFile(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}

func detectParts(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var parts []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "part-") && strings.HasSuffix(e.Name(), ".md") {
			parts = append(parts, e.Name())
		}
	}
	sort.Strings(parts)
	return parts
}

func Delete(slug string) error {
	if slug == "" || slug == "." || slug == ".." || strings.ContainsAny(slug, `/\`) {
		return fmt.Errorf("invalid slug: %q", slug)
	}
	tutorialsDir, err := config.TutorialsDir()
	if err != nil {
		return err
	}
	target := filepath.Join(tutorialsDir, slug)
	if !strings.HasPrefix(target, tutorialsDir+string(filepath.Separator)) {
		return fmt.Errorf("invalid slug: %q", slug)
	}
	info, err := os.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("tutorial %q not found", slug)
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a tutorial directory: %q", slug)
	}
	return os.RemoveAll(target)
}

func SlugToTitle(slug string) string {
	words := strings.Split(slug, "-")
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// PromoteIndexToPart renames index.md to part-01.md and updates metadata.Parts.
// No-op if part-01.md already exists or index.md is absent.
// Rename is done first; metadata is written only after a successful rename,
// so a failure mid-operation leaves the tutorial in a consistent state.
func PromoteIndexToPart(tutorialDir string) error {
	indexPath := filepath.Join(tutorialDir, "index.md")
	partPath := filepath.Join(tutorialDir, "part-01.md")

	// stat the tutorial dir to detect missing dir early
	if _, err := os.Stat(tutorialDir); err != nil {
		return fmt.Errorf("tutorial dir: %w", err)
	}

	// already promoted or nothing to promote
	if _, err := os.Stat(partPath); err == nil {
		return nil
	}
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		return nil
	}

	if err := os.Rename(indexPath, partPath); err != nil {
		return fmt.Errorf("rename index.md to part-01.md: %w", err)
	}

	tut, err := ReadMetadata(tutorialDir)
	if err != nil {
		return fmt.Errorf("read metadata after rename: %w", err)
	}
	tut.Parts = []string{"part-01.md"}
	return WriteMetadata(tutorialDir, tut)
}
