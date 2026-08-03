package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devenjarvis/lathe/internal/store"
)

func TestParseTools(t *testing.T) {
	got := parseTools([]string{"zig:0.13.0", "go:1.22", "make"})
	want := []store.Tool{
		{Name: "zig", Version: "0.13.0"},
		{Name: "go", Version: "1.22"},
		{Name: "make", Version: ""}, // no ":" → name only, empty version
	}
	if len(got) != len(want) {
		t.Fatalf("parseTools = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("parseTools[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestParseToolsSplitsOnFirstColon(t *testing.T) {
	// A version that itself contains ":" must survive — only the first ":" splits.
	got := parseTools([]string{"docker:image:tag"})
	if len(got) != 1 || got[0] != (store.Tool{Name: "docker", Version: "image:tag"}) {
		t.Errorf("parseTools = %v, want [{docker image:tag}]", got)
	}
}

func TestStoreCmdRecordsModel(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "index.md"), []byte("# Hello"), 0644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	storeModel = "Claude Opus 4.8"
	t.Cleanup(func() { storeModel = "" })

	if err := storeCmd.RunE(storeCmd, []string{src}); err != nil {
		t.Fatalf("store: %v", err)
	}
	slug := filepath.Base(src)
	got, err := store.ReadMetadata(filepath.Join(home, ".lathe", "tutorials", slug))
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "Claude Opus 4.8" {
		t.Errorf("Model = %q, want %q", got.Model, "Claude Opus 4.8")
	}
}

func resetOnboardingStoreFlags(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		storeKind = ""
		storeRepo = ""
		storeBranch = ""
		storeRepoCommit = ""
		storeRepoPath = ""
	})
}

func TestStoreCmdOnboardingRequiresTheRepoTriple(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "part-01.md"), []byte("# Hi"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	resetOnboardingStoreFlags(t)

	storeKind = "onboarding"
	storeRepo = "git@github.com:devenjarvis/lathe.git"
	// No --repo-commit and no --repo-path.
	if err := storeCmd.RunE(storeCmd, []string{src}); err == nil {
		t.Fatal("store --kind onboarding without --repo-commit should error")
	}
}

func TestStoreCmdRejectsUnknownKind(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "part-01.md"), []byte("# Hi"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	resetOnboardingStoreFlags(t)

	storeKind = "guide"
	if err := storeCmd.RunE(storeCmd, []string{src}); err == nil {
		t.Fatal("store --kind guide should error")
	}
}

func TestStoreCmdRecordsOnboardingPin(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "part-01.md"), []byte("# Hi"), 0644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetOnboardingStoreFlags(t)

	storeKind = "onboarding"
	storeRepo = "git@github.com:devenjarvis/lathe.git"
	storeBranch = "main"
	storeRepoCommit = "1e7c9f6aaaabbbbccccddddeeeeffff0000111122"
	storeRepoPath = "/Users/x/Code/lathe"

	if err := storeCmd.RunE(storeCmd, []string{src}); err != nil {
		t.Fatalf("store: %v", err)
	}
	got, err := store.ReadMetadata(filepath.Join(home, ".lathe", "tutorials", filepath.Base(src)))
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsOnboarding() {
		t.Error("IsOnboarding() = false")
	}
	if got.RepoCommit != storeRepoCommit {
		t.Errorf("RepoCommit = %q, want %q", got.RepoCommit, storeRepoCommit)
	}
	if got.RepoPath != "/Users/x/Code/lathe" {
		t.Errorf("RepoPath = %q", got.RepoPath)
	}
}
