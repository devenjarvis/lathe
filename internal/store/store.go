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

// StoreOptions carries the optional metadata captured at store time. All fields
// are normalized by Store before they land in metadata.json. Keeping them in a
// struct (rather than a long positional signature) lets callers set only what
// they have and keeps call sites readable as the captured set grows.
type StoreOptions struct {
	Tags    []string // search tags (NormalizeTags)
	Sources []string // research-trail URLs (NormalizeSources)
	Repo    string   // git remote the tutorial was written for (NormalizeRepo)
	Branch  string   // branch the tutorial targets (only meaningful with Repo)
	Tools   []Tool   // languages/tools + versions (NormalizeTools)
	Voice   string   // writing voice the tutorial was generated in (NormalizeVoice)
	Model   string   // LLM that authored the tutorial, a display label (NormalizeModel)
}

// Store copies a tutorial directory into ~/.lathe/tutorials/ and writes its
// metadata with status=unverified. Verification is opt-in and never auto-runs
// here — the user triggers it separately via the /lathe-verify skill. Every
// field of opts is normalized before it lands in metadata.
func Store(srcPath string, opts StoreOptions) (*Tutorial, error) {
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

	repo := NormalizeRepo(opts.Repo)
	branch := strings.TrimSpace(opts.Branch)
	if repo == "" {
		// A branch with no repo is meaningless — don't record a dangling branch.
		branch = ""
	}

	t := &Tutorial{
		Slug:       slug,
		Title:      SlugToTitle(slug),
		Topic:      slug,
		Created:    time.Now().UTC(),
		Status:     StatusUnverified,
		Tags:       NormalizeTags(opts.Tags),
		Parts:      parts,
		Repo:       repo,
		RepoBranch: branch,
		Tools:      NormalizeTools(opts.Tools),
		Sources:    NormalizeSources(opts.Sources),
		Voice:      NormalizeVoice(opts.Voice),
		Model:      NormalizeModel(opts.Model),
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

// NormalizeRepo canonicalizes a git remote into a stable host/org/repo grouping
// key. It strips the scheme (https://, http://, ssh://, git://) and any userinfo
// (git@), rewrites scp-style "host:org/repo" into "host/org/repo", drops a port
// and a trailing ".git"/"/", and lowercases the host so the same repo addressed
// over https or ssh groups together. Returns "" for empty input so Repo stays
// omitempty in metadata.json.
//
//	https://github.com/org/repo.git   → github.com/org/repo
//	git@github.com:org/repo.git       → github.com/org/repo
//	ssh://git@github.com:22/org/repo   → github.com/org/repo
func NormalizeRepo(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}

	// Drop scheme (scheme://...). Whether one was present disambiguates the
	// meaning of a later ":" below.
	hadScheme := false
	if i := strings.Index(s, "://"); i != -1 {
		s = s[i+3:]
		hadScheme = true
	}

	// Drop userinfo (e.g. git@) when it precedes the host.
	if at := strings.Index(s, "@"); at != -1 {
		if sep := strings.IndexAny(s, "/:"); sep == -1 || at < sep {
			s = s[at+1:]
		}
	}

	// Resolve the first ":". A scheme URL ("ssh://host:22/…") can carry a numeric
	// :port we drop. The scp short form ("host:org/repo") never carries a port —
	// the colon is always the host/path separator — so we always rewrite it to
	// "/", which also keeps a purely-numeric first path segment intact.
	if i := strings.Index(s, ":"); i != -1 {
		rest := s[i+1:]
		j := 0
		for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
			j++
		}
		if hadScheme && j > 0 && (j == len(rest) || rest[j] == '/') {
			s = s[:i] + rest[j:] // scheme URL → numeric is a port, drop it
		} else {
			s = s[:i] + "/" + rest // scp short form → path separator
		}
	}

	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimSuffix(s, "/")

	// Lowercase only the host (first segment); paths can be case-sensitive.
	if i := strings.Index(s, "/"); i != -1 {
		s = strings.ToLower(s[:i]) + s[i:]
	} else {
		s = strings.ToLower(s)
	}
	return s
}

// NormalizeTools cleans a tool list: trims and lowercases each Name, trims the
// Version (preserving its case — versions are identifiers), drops entries with
// an empty name, and de-dupes by name keeping the first occurrence. Returns nil
// for an all-empty input so Tools stays omitempty in metadata.json.
func NormalizeTools(tools []Tool) []Tool {
	seen := make(map[string]struct{}, len(tools))
	var out []Tool
	for _, t := range tools {
		name := strings.ToLower(strings.TrimSpace(t.Name))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, Tool{Name: name, Version: strings.TrimSpace(t.Version)})
	}
	return out
}

// NormalizeVoice canonicalizes a voice name to its selection key: trimmed and
// lowercased, matching how the voice package keys its presets and custom files.
// Returns "" for empty input so Voice stays omitempty in metadata.json (an empty
// voice means "fall back to the configured default").
func NormalizeVoice(voice string) string {
	return strings.ToLower(strings.TrimSpace(voice))
}

// NormalizeModel canonicalizes a model label by trimming surrounding whitespace
// only — case is preserved because it is a human-facing display label (e.g.
// "Claude Opus 4.8"), not a selection key. Returns "" for empty input so Model
// stays omitempty in metadata.json (an empty model falls back to "Claude" in the
// reading-page byline).
func NormalizeModel(model string) string {
	return strings.TrimSpace(model)
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
