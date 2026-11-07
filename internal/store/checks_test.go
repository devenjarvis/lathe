package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordCheckAppendsAndLatestWins(t *testing.T) {
	dir := cancelFixture(t, StatusUnverified, "part-01.md", "part-02.md")

	first := &CheckResult{Part: "part-01.md", Status: CheckFail, Findings: []CheckFinding{{File: "main.go", Nudge: "  Look at the loop again.  "}}}
	if err := RecordCheck(dir, first); err != nil {
		t.Fatalf("RecordCheck: %v", err)
	}
	if first.Findings[0].Nudge != "Look at the loop again." {
		t.Errorf("nudge not trimmed: %q", first.Findings[0].Nudge)
	}
	if first.Confidence != CheckConfidenceText {
		t.Errorf("confidence = %q, want text (no snapshot)", first.Confidence)
	}
	if first.CheckedAt == "" || first.Time().IsZero() {
		t.Errorf("checked_at not filled: %q", first.CheckedAt)
	}
	if err := RecordCheck(dir, &CheckResult{Part: "part-02.md", Status: CheckPass}); err != nil {
		t.Fatal(err)
	}
	if err := RecordCheck(dir, &CheckResult{Part: "part-01.md", Status: CheckPass, Summary: "fixed"}); err != nil {
		t.Fatal(err)
	}

	all, err := ReadChecks(dir)
	if err != nil || len(all) != 3 {
		t.Fatalf("ReadChecks = %d, %v; want 3", len(all), err)
	}
	got := LatestCheck(dir, "part-01.md")
	if got == nil || got.Status != CheckPass || got.Summary != "fixed" {
		t.Errorf("LatestCheck(part-01) = %+v, want the second part-01 result", got)
	}
	if LatestCheck(dir, "part-03.md") != nil {
		t.Error("LatestCheck for an unchecked part should be nil")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ChecksFile))
	if n := strings.Count(string(raw), "\n"); n != 3 {
		t.Errorf("checks.jsonl has %d lines, want 3", n)
	}
}

func TestRecordCheckValidates(t *testing.T) {
	dir := cancelFixture(t, StatusUnverified, "part-01.md", "part-02.md")
	cases := map[string]*CheckResult{
		"unknown part":        {Part: "part-09.md", Status: CheckPass},
		"bad status":          {Part: "part-01.md", Status: "great"},
		"fail without hints":  {Part: "part-01.md", Status: CheckFail},
		"finding w/o nudge":   {Part: "part-01.md", Status: CheckFail, Findings: []CheckFinding{{Where: "main.go"}}},
		"divergence w/o note": {Part: "part-01.md", Status: CheckPass, Divergences: []CheckDivergence{{File: "a"}}},
		"bad later part":      {Part: "part-01.md", Status: CheckPass, Divergences: []CheckDivergence{{Note: "n", LaterPart: "part-07.md"}}},
		"bad confidence":      {Part: "part-01.md", Status: CheckPass, Confidence: "high"},
		"bad timestamp":       {Part: "part-01.md", Status: CheckPass, CheckedAt: "yesterday"},
		"bad on_part":         {Part: "part-01.md", Status: CheckPass, OnPart: "part-05.md"},
	}
	for name, c := range cases {
		if err := RecordCheck(dir, c); err == nil {
			t.Errorf("%s: RecordCheck accepted %+v", name, c)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ChecksFile)); !os.IsNotExist(err) {
		t.Errorf("rejected checks still wrote %s", ChecksFile)
	}
}

func TestRecordCheckReferenceConfidenceFromSnapshot(t *testing.T) {
	dir := cancelFixture(t, StatusUnverified, "part-01.md", "part-02.md")
	if err := SaveCode(dir, gitRepo(t, "part-01")); err != nil {
		t.Fatal(err)
	}
	c := &CheckResult{Part: "part-01.md", Status: CheckPass}
	if err := RecordCheck(dir, c); err != nil {
		t.Fatal(err)
	}
	if c.Confidence != CheckConfidenceReference {
		t.Errorf("confidence = %q, want reference (part-01 is in the snapshot)", c.Confidence)
	}
	c2 := &CheckResult{Part: "part-02.md", Status: CheckPass}
	if err := RecordCheck(dir, c2); err != nil {
		t.Fatal(err)
	}
	if c2.Confidence != CheckConfidenceText {
		t.Errorf("confidence = %q, want text (part-02 has no tag)", c2.Confidence)
	}
}

func TestPartDiff(t *testing.T) {
	dir := cancelFixture(t, StatusUnverified, "part-01.md", "part-02.md", "part-03.md")
	if err := SaveCode(dir, gitRepo(t, "part-01", "part-02")); err != nil {
		t.Fatal(err)
	}
	parts := []string{"part-01.md", "part-02.md", "part-03.md"}

	d, ok := PartDiff(dir, parts, "part-02.md")
	if !ok || !strings.Contains(d, "-part-01") || !strings.Contains(d, "+part-02") {
		t.Errorf("PartDiff(part-02) = %q, %v; want the part-01→part-02 change", d, ok)
	}
	d, ok = PartDiff(dir, parts, "part-01.md")
	if !ok || !strings.Contains(d, "+part-01") || !strings.Contains(d, "new file") {
		t.Errorf("PartDiff(part-01) = %q, %v; want the whole first part as new", d, ok)
	}
	if _, ok := PartDiff(dir, parts, "part-03.md"); ok {
		t.Error("PartDiff for an untagged part should report ok=false")
	}
}

func TestCopyWorkspaceKeepsLearnerFolderAndDropsEscapingLinks(t *testing.T) {
	ws := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(ws, "src", "lib"), 0o755))
	must(os.WriteFile(filepath.Join(ws, "src", "main.go"), []byte("package main\n"), 0o644))
	must(os.WriteFile(filepath.Join(ws, "run.sh"), []byte("#!/bin/sh\n"), 0o755))
	must(os.Symlink("src/main.go", filepath.Join(ws, "inside-link")))
	must(os.Symlink(outside, filepath.Join(ws, "abs-link")))
	must(os.Symlink("../../escape", filepath.Join(ws, "src", "rel-escape")))

	dst := t.TempDir()
	if err := CopyWorkspace(ws, dst); err != nil {
		t.Fatalf("CopyWorkspace: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "src", "main.go")); err != nil || string(b) != "package main\n" {
		t.Errorf("copied main.go = %q, %v", b, err)
	}
	if info, err := os.Stat(filepath.Join(dst, "run.sh")); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Errorf("run.sh lost its exec bit: %v %v", info, err)
	}
	if link, err := os.Readlink(filepath.Join(dst, "inside-link")); err != nil || link != "src/main.go" {
		t.Errorf("inside link = %q, %v; want it recreated", link, err)
	}
	for _, gone := range []string{"abs-link", filepath.Join("src", "rel-escape")} {
		if _, err := os.Lstat(filepath.Join(dst, gone)); !os.IsNotExist(err) {
			t.Errorf("%s was copied; links that leave the workspace must be dropped", gone)
		}
	}

	// Writing in the copy never reaches the learner's files.
	must(os.WriteFile(filepath.Join(dst, "src", "main.go"), []byte("changed"), 0o644))
	must(os.WriteFile(filepath.Join(dst, "inside-link"), []byte("via link"), 0o644))
	if b, _ := os.ReadFile(filepath.Join(ws, "src", "main.go")); string(b) != "package main\n" {
		t.Errorf("learner's main.go changed to %q", b)
	}
}

func TestCheckpointSection(t *testing.T) {
	md := "# Part 1\n\nIntro.\n\n## Checkpoint\n\nRun it:\n\n```bash\ngo run .\n## not a heading\n```\n\nYou should see `ok`.\n\n## Exercises\n\n- [ ] one\n"
	got := CheckpointSection([]byte(md))
	if !strings.HasPrefix(got, "## Checkpoint") || !strings.Contains(got, "## not a heading") || !strings.Contains(got, "You should see") {
		t.Errorf("CheckpointSection = %q", got)
	}
	if strings.Contains(got, "Exercises") {
		t.Errorf("section ran into the next heading: %q", got)
	}
	if CheckpointSection([]byte("# Part\n\nNo checkpoint.\n")) != "" {
		t.Error("want empty for a part with no checkpoint")
	}
}

func TestStoreDropsStaleChecks(t *testing.T) {
	src := filepath.Join(t.TempDir(), "check-me")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "part-01.md"), []byte("# One\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	if _, err := Store(src, StoreOptions{}); err != nil {
		t.Fatal(err)
	}
	dir, err := TutorialPath("check-me")
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordCheck(dir, &CheckResult{Part: "part-01.md", Status: CheckPass}); err != nil {
		t.Fatal(err)
	}
	if _, err := Store(src, StoreOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ChecksFile)); !os.IsNotExist(err) {
		t.Error("re-store kept checks.jsonl from the old parts")
	}
}
