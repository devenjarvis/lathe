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
// here — the user triggers it separately via the /lathe-verify skill. sources
// is the research trail (URLs the skill consulted); both tags and sources are
// normalized before they land in metadata.
func Store(srcPath string, tags, sources []string) (*Tutorial, error) {
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
		Tags:    NormalizeTags(tags),
		Parts:   parts,
		Sources: NormalizeSources(sources),
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
	defer in.Close() //nolint:errcheck
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
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

// NormalizeTags cleans a tag list: trims surrounding whitespace, lowercases,
// drops empties, and removes duplicates while preserving first-seen order.
// Returns nil for an all-empty input so it stays omitempty in metadata.json.
func NormalizeTags(tags []string) []string {
	seen := make(map[string]struct{}, len(tags))
	var out []string
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

// NormalizeSources cleans a source-URL list: trims surrounding whitespace,
// drops empties, and removes duplicates while preserving first-seen order.
// Unlike NormalizeTags it does NOT lowercase — URLs are case-sensitive (paths,
// query strings, and fragments can all carry meaning). Returns nil for an
// all-empty input so it stays omitempty in metadata.json.
func NormalizeSources(sources []string) []string {
	seen := make(map[string]struct{}, len(sources))
	var out []string
	for _, s := range sources {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
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
