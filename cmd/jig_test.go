package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runJigCheck(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	jigCheckCmd.SetOut(&out)
	t.Cleanup(func() { jigCheckCmd.SetOut(nil) })
	err := jigCheckCmd.RunE(jigCheckCmd, args)
	return out.String(), err
}

func TestJigCheckPassesValidJigs(t *testing.T) {
	dir := t.TempDir()
	part := "# P\n\n```jig guess\nask: q\n- a\n- [x] b\nexplain: e\n```\n"
	if err := os.WriteFile(filepath.Join(dir, "part-01.md"), []byte(part), 0644); err != nil {
		t.Fatal(err)
	}
	out, err := runJigCheck(t, dir)
	if err != nil {
		t.Fatalf("check: %v\n%s", err, out)
	}
	if !strings.Contains(out, "ok: 1 jig(s) in 1 file(s)") {
		t.Errorf("out = %q", out)
	}
}

func TestJigCheckReportsFileAndLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "part-02.md")
	part := "# P\n\n```jig guess\nask: q\n- a\n- b\nexplain: e\n```\n"
	if err := os.WriteFile(path, []byte(part), 0644); err != nil {
		t.Fatal(err)
	}
	out, err := runJigCheck(t, path)
	if err == nil {
		t.Fatal("want an error for a jig with no marked answer")
	}
	if !strings.Contains(out, path+":3: jig guess: no option is marked [x]") {
		t.Errorf("out = %q, want file:line and the problem", out)
	}
}

func TestJigListAndShow(t *testing.T) {
	var out bytes.Buffer
	writeJigList(&out)
	if !strings.Contains(out.String(), "guess (Guess first)") {
		t.Errorf("list = %q", out.String())
	}
	out.Reset()
	jigShowCmd.SetOut(&out)
	t.Cleanup(func() { jigShowCmd.SetOut(nil) })
	if err := jigShowCmd.RunE(jigShowCmd, []string{"guess"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "```jig guess\nask:") {
		t.Errorf("show should end with a copyable example:\n%s", out.String())
	}
	if err := jigShowCmd.RunE(jigShowCmd, []string{"nope"}); err == nil || !strings.Contains(err.Error(), "available: guess") {
		t.Errorf("unknown kind err = %v", err)
	}
}
