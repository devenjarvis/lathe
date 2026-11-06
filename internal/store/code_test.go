package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// gitRepo builds a scratch repository the way the verify skill does: one commit
// per part, each tagged after the part. Untracked files are left out of commits
// to prove the snapshot doesn't copy them.
func gitRepo(t *testing.T, parts ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "--quiet")
	for _, p := range parts {
		if err := os.WriteFile(filepath.Join(dir, "main.txt"), []byte(p), 0644); err != nil {
			t.Fatal(err)
		}
		git("add", "main.txt")
		git("commit", "--quiet", "-m", p)
		git("tag", p)
	}
	if err := os.WriteFile(filepath.Join(dir, "build-output.bin"), []byte("junk"), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPartTag(t *testing.T) {
	if got := PartTag("part-04.md"); got != "part-04" {
		t.Errorf("PartTag = %q, want part-04", got)
	}
}

func TestSaveCodeClonesTaggedHistory(t *testing.T) {
	src := gitRepo(t, "part-01", "part-02")
	tutDir := t.TempDir()

	if err := SaveCode(tutDir, src); err != nil {
		t.Fatalf("SaveCode: %v", err)
	}
	dest := filepath.Join(tutDir, CodeDirName)
	if got := CodeTags(dest); !reflect.DeepEqual(got, []string{"part-01", "part-02"}) {
		t.Errorf("CodeTags = %v, want [part-01 part-02]", got)
	}
	// The working tree is the last part's code.
	if b, err := os.ReadFile(filepath.Join(dest, "main.txt")); err != nil || string(b) != "part-02" {
		t.Errorf("main.txt = %q, %v; want part-02", b, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "build-output.bin")); !os.IsNotExist(err) {
		t.Errorf("untracked build output should not be snapshotted (stat err = %v)", err)
	}
	// No leftover temp dirs beside the snapshot.
	entries, _ := os.ReadDir(tutDir)
	if len(entries) != 1 {
		t.Errorf("tutorial dir has %d entries, want only %s", len(entries), CodeDirName)
	}
	// The scratch-dir remote is dropped.
	cmd := exec.Command("git", "remote")
	cmd.Dir = dest
	if out, err := cmd.Output(); err != nil || len(out) != 0 {
		t.Errorf("git remote = %q, %v; want no remotes", out, err)
	}
}

func TestSaveCodeReplacesPreviousSnapshot(t *testing.T) {
	tutDir := t.TempDir()
	if err := SaveCode(tutDir, gitRepo(t, "part-01", "part-02", "part-03")); err != nil {
		t.Fatal(err)
	}
	if err := SaveCode(tutDir, gitRepo(t, "part-01")); err != nil {
		t.Fatal(err)
	}
	if got := CodeTags(filepath.Join(tutDir, CodeDirName)); !reflect.DeepEqual(got, []string{"part-01"}) {
		t.Errorf("CodeTags = %v, want [part-01]", got)
	}
}

func TestSaveCodeRejectsNonRepo(t *testing.T) {
	tutDir := t.TempDir()
	if err := SaveCode(tutDir, t.TempDir()); err == nil {
		t.Fatal("SaveCode on a plain dir should fail")
	}
}

func TestSaveCodeRejectsUntaggedRepoAndKeepsOldSnapshot(t *testing.T) {
	tutDir := t.TempDir()
	if err := SaveCode(tutDir, gitRepo(t, "part-01")); err != nil {
		t.Fatal(err)
	}
	if err := SaveCode(tutDir, gitRepo(t)); err == nil {
		t.Fatal("SaveCode on a repo with no part tags should fail")
	}
	if got := CodeTags(filepath.Join(tutDir, CodeDirName)); !reflect.DeepEqual(got, []string{"part-01"}) {
		t.Errorf("previous snapshot should survive a failed save; CodeTags = %v", got)
	}
}

func TestCodeTagsReadsLooseAndPackedRefs(t *testing.T) {
	repo := t.TempDir()
	tags := filepath.Join(repo, ".git", "refs", "tags")
	if err := os.MkdirAll(tags, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tags, "part-02"), []byte("abc\n"), 0644); err != nil {
		t.Fatal(err)
	}
	packed := "# pack-refs with: peeled fully-peeled sorted\n" +
		"aaa refs/heads/main\n" +
		"bbb refs/tags/part-01\n" +
		"^ccc\n" +
		"ddd refs/tags/v1.0\n"
	if err := os.WriteFile(filepath.Join(repo, ".git", "packed-refs"), []byte(packed), 0644); err != nil {
		t.Fatal(err)
	}
	if got := CodeTags(repo); !reflect.DeepEqual(got, []string{"part-01", "part-02"}) {
		t.Errorf("CodeTags = %v, want [part-01 part-02]", got)
	}
	if got := CodeTags(t.TempDir()); got != nil {
		t.Errorf("CodeTags on a non-repo = %v, want nil", got)
	}
}
