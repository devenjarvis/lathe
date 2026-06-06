package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devenjarvis/lathe/internal/store"
)

func TestStoreSingleTutorial(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "index.md"), []byte("# Hello"), 0644); err != nil {
		t.Fatal(err)
	}
	// Override home so we don't pollute the real ~/.lathe
	t.Setenv("HOME", t.TempDir())

	tut, err := store.Store(src, store.StoreOptions{})
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	if tut.IsSeries() {
		t.Error("Store() IsSeries() = true, want false for single tutorial")
	}
	if tut.Status != store.StatusUnverified {
		t.Errorf("Store() Status = %q, want %q", tut.Status, store.StatusUnverified)
	}
}

func TestStoreStripsLathePrefixFromSlug(t *testing.T) {
	// The generation skill writes to /tmp/lathe-<slug>/; the "lathe-" prefix
	// must not leak into the stored slug or the derived title.
	src := filepath.Join(t.TempDir(), "lathe-digital-synth-zig")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "index.md"), []byte("# Hello"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())

	tut, err := store.Store(src, store.StoreOptions{})
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	if tut.Slug != "digital-synth-zig" {
		t.Errorf("Store() Slug = %q, want %q", tut.Slug, "digital-synth-zig")
	}
	if tut.Title != "Digital Synth Zig" {
		t.Errorf("Store() Title = %q, want %q", tut.Title, "Digital Synth Zig")
	}
}

func TestStoreSeriesTutorial(t *testing.T) {
	src := t.TempDir()
	for _, name := range []string{"part-01.md", "part-02.md", "part-03.md"} {
		if err := os.WriteFile(filepath.Join(src, name), []byte("# Part"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", t.TempDir())

	tut, err := store.Store(src, store.StoreOptions{})
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	if !tut.IsSeries() {
		t.Error("Store() IsSeries() = false, want true for series")
	}
	if len(tut.Parts) != 3 {
		t.Errorf("Store() Parts = %v, want 3 parts", tut.Parts)
	}
}

func TestStorePersistsAndNormalizesTags(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "index.md"), []byte("# Hello"), 0644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	tut, err := store.Store(src, store.StoreOptions{Tags: []string{"  Rust ", "audio", "rust", ""}})
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	want := []string{"rust", "audio"} // trimmed, lowercased, de-duped, empty dropped
	if got := tut.Tags; !equalStrings(got, want) {
		t.Errorf("Store() Tags = %v, want %v", got, want)
	}

	// Tags must round-trip through metadata.json.
	read, err := store.ReadMetadata(filepath.Join(home, ".lathe", "tutorials", tut.Slug))
	if err != nil {
		t.Fatalf("ReadMetadata() error = %v", err)
	}
	if !equalStrings(read.Tags, want) {
		t.Errorf("ReadMetadata() Tags = %v, want %v", read.Tags, want)
	}
}

func TestStorePersistsAndNormalizesRepoAndTools(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "index.md"), []byte("# Hello"), 0644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	tut, err := store.Store(src, store.StoreOptions{
		Repo:   "git@github.com:devenjarvis/lathe.git",
		Branch: " main ",
		Tools:  []store.Tool{{Name: " Zig ", Version: "0.13.0"}, {Name: "zig", Version: "0.12"}},
	})
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	if tut.Repo != "github.com/devenjarvis/lathe" {
		t.Errorf("Store() Repo = %q, want %q", tut.Repo, "github.com/devenjarvis/lathe")
	}
	if tut.RepoBranch != "main" {
		t.Errorf("Store() RepoBranch = %q, want %q", tut.RepoBranch, "main")
	}
	if len(tut.Tools) != 1 || tut.Tools[0] != (store.Tool{Name: "zig", Version: "0.13.0"}) {
		t.Errorf("Store() Tools = %v, want [{zig 0.13.0}]", tut.Tools)
	}

	// Round-trips through metadata.json.
	read, err := store.ReadMetadata(filepath.Join(home, ".lathe", "tutorials", tut.Slug))
	if err != nil {
		t.Fatalf("ReadMetadata() error = %v", err)
	}
	if read.Repo != tut.Repo || read.RepoBranch != tut.RepoBranch || len(read.Tools) != 1 {
		t.Errorf("ReadMetadata() = %+v, want repo/branch/tools to match", read)
	}
}

func TestStorePersistsAndNormalizesVoice(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "index.md"), []byte("# Hello"), 0644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	tut, err := store.Store(src, store.StoreOptions{Voice: "  Companion  "})
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	if tut.Voice != "companion" {
		t.Errorf("Store() Voice = %q, want %q", tut.Voice, "companion")
	}
	read, err := store.ReadMetadata(filepath.Join(home, ".lathe", "tutorials", tut.Slug))
	if err != nil {
		t.Fatalf("ReadMetadata() error = %v", err)
	}
	if read.Voice != "companion" {
		t.Errorf("ReadMetadata() Voice = %q, want companion", read.Voice)
	}
}

func TestStoreEmptyVoiceStaysOmitted(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "index.md"), []byte("# Hello"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	tut, err := store.Store(src, store.StoreOptions{})
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	if tut.Voice != "" {
		t.Errorf("Store() Voice = %q, want empty", tut.Voice)
	}
}

func TestStorePersistsAndNormalizesModel(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "index.md"), []byte("# Hello"), 0644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Trim only — case is preserved because Model is a human-facing label.
	tut, err := store.Store(src, store.StoreOptions{Model: "  Claude Opus 4.8  "})
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	if tut.Model != "Claude Opus 4.8" {
		t.Errorf("Store() Model = %q, want %q", tut.Model, "Claude Opus 4.8")
	}
	read, err := store.ReadMetadata(filepath.Join(home, ".lathe", "tutorials", tut.Slug))
	if err != nil {
		t.Fatalf("ReadMetadata() error = %v", err)
	}
	if read.Model != "Claude Opus 4.8" {
		t.Errorf("ReadMetadata() Model = %q, want %q", read.Model, "Claude Opus 4.8")
	}
}

func TestStoreEmptyModelStaysOmitted(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "index.md"), []byte("# Hello"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	tut, err := store.Store(src, store.StoreOptions{})
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	if tut.Model != "" {
		t.Errorf("Store() Model = %q, want empty", tut.Model)
	}
}

func TestNormalizeModel(t *testing.T) {
	cases := map[string]string{
		"  Claude Opus 4.8  ": "Claude Opus 4.8",
		"GPT-4o":              "GPT-4o", // case preserved
		"":                    "",
		"   ":                 "",
	}
	for in, want := range cases {
		if got := store.NormalizeModel(in); got != want {
			t.Errorf("NormalizeModel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStoreDropsBranchWithoutRepo(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "index.md"), []byte("# Hello"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())

	// A branch with no repo is meaningless and must not be recorded.
	tut, err := store.Store(src, store.StoreOptions{Branch: "main"})
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	if tut.Repo != "" || tut.RepoBranch != "" {
		t.Errorf("Store() Repo/Branch = %q/%q, want both empty", tut.Repo, tut.RepoBranch)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestStoreDefaultsToUnverified(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "index.md"), []byte("# Hello"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	// Store never auto-verifies; the default status is unverified.
	tut, err := store.Store(src, store.StoreOptions{})
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	if tut.Status != store.StatusUnverified {
		t.Errorf("Store() Status = %q, want %q", tut.Status, store.StatusUnverified)
	}
}

func TestStoreDoesNotSpawnVerifier(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "index.md"), []byte("# Hello"), 0644); err != nil {
		t.Fatal(err)
	}
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	tut, err := store.Store(src, store.StoreOptions{})
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}

	tutDir := filepath.Join(homeDir, ".lathe", "tutorials", tut.Slug)
	// No verify.log means no subprocess was launched.
	if _, err := os.Stat(filepath.Join(tutDir, "verify.log")); !os.IsNotExist(err) {
		t.Errorf("Store() should not spawn a verifier; verify.log stat err = %v", err)
	}
}

func TestDeleteRemovesTutorial(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "index.md"), []byte("# Hello"), 0644); err != nil {
		t.Fatal(err)
	}
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	tut, err := store.Store(src, store.StoreOptions{})
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	tutDir := filepath.Join(homeDir, ".lathe", "tutorials", tut.Slug)
	if _, err := os.Stat(tutDir); err != nil {
		t.Fatalf("tutorial dir not created: %v", err)
	}

	if err := store.Delete(tut.Slug); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := os.Stat(tutDir); !os.IsNotExist(err) {
		t.Errorf("tutorial dir still exists after Delete: stat err = %v", err)
	}
}

func TestDeleteMissingSlug(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := store.Delete("does-not-exist"); err == nil {
		t.Error("Delete() of missing slug should error")
	}
}

func TestDeleteRejectsBadSlugs(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	sentinel := filepath.Join(homeDir, "sentinel")
	if err := os.WriteFile(sentinel, []byte("don't touch"), 0644); err != nil {
		t.Fatal(err)
	}

	for _, slug := range []string{"", ".", "..", "../sentinel", "foo/bar", `foo\bar`} {
		if err := store.Delete(slug); err == nil {
			t.Errorf("Delete(%q) should reject as invalid", slug)
		}
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Errorf("sentinel file disturbed by bad-slug delete: %v", err)
	}
}

func TestSlugToTitle(t *testing.T) {
	cases := []struct {
		slug  string
		title string
	}{
		{"digital-synth-zig", "Digital Synth Zig"},
		{"database-from-scratch-go", "Database From Scratch Go"},
		{"hello", "Hello"},
	}
	for _, c := range cases {
		got := store.SlugToTitle(c.slug)
		if got != c.title {
			t.Errorf("SlugToTitle(%q) = %q, want %q", c.slug, got, c.title)
		}
	}
}

func TestPromoteIndexToPartRenamesAndUpdatesMetadata(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte("# Hello"), 0644); err != nil {
		t.Fatal(err)
	}
	tut := &store.Tutorial{
		Slug:   "test-tut",
		Title:  "Test Tutorial",
		Status: store.StatusVerified,
	}
	if err := store.WriteMetadata(dir, tut); err != nil {
		t.Fatal(err)
	}

	if err := store.PromoteIndexToPart(dir); err != nil {
		t.Fatalf("PromoteIndexToPart() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "part-01.md")); err != nil {
		t.Error("part-01.md should exist after promotion")
	}
	if _, err := os.Stat(filepath.Join(dir, "index.md")); !os.IsNotExist(err) {
		t.Error("index.md should be gone after promotion")
	}
	got, err := store.ReadMetadata(dir)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if len(got.Parts) != 1 || got.Parts[0] != "part-01.md" {
		t.Errorf("Parts = %v, want [part-01.md]", got.Parts)
	}
}

func TestPromoteIndexToPartIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "part-01.md"), []byte("# Hello"), 0644); err != nil {
		t.Fatal(err)
	}
	tut := &store.Tutorial{
		Slug:   "test-tut",
		Parts:  []string{"part-01.md"},
		Status: store.StatusVerified,
	}
	if err := store.WriteMetadata(dir, tut); err != nil {
		t.Fatal(err)
	}

	if err := store.PromoteIndexToPart(dir); err != nil {
		t.Fatalf("PromoteIndexToPart() error = %v (should be no-op)", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "part-01.md")); err != nil {
		t.Error("part-01.md should still exist")
	}
}

func TestPromoteIndexToPartFailsCleanly(t *testing.T) {
	err := store.PromoteIndexToPart("/nonexistent/path/abc123xyz")
	if err == nil {
		t.Error("PromoteIndexToPart() on missing dir should return error")
	}
}
