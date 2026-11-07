package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/devenjarvis/lathe/internal/store"
)

func TestCheckResultRecordsFromStdin(t *testing.T) {
	homeDir := t.TempDir()
	setHome(t, homeDir)
	tutDir := writeTutorial(t, homeDir, "test-slug", store.StatusUnverified, []string{"part-01.md", "part-02.md"})
	t.Cleanup(func() { checkResultFile = "" })

	checkResultFile = "-"
	var out bytes.Buffer
	checkResultCmd.SetIn(strings.NewReader(`{"part":"part-02.md","status":"fail","findings":[{"file":"main.go","nudge":"Re-read the loop.","reveal":"for i := 0; i < n; i++ {}"}]}`))
	checkResultCmd.SetOut(&out)
	t.Cleanup(func() { checkResultCmd.SetIn(nil); checkResultCmd.SetOut(nil) })
	if err := checkResultCmd.RunE(checkResultCmd, []string{"test-slug"}); err != nil {
		t.Fatalf("check-result: %v", err)
	}
	if !strings.Contains(out.String(), "part-02.md") || !strings.Contains(out.String(), "fail") {
		t.Errorf("output = %q", out.String())
	}
	got := store.LatestCheck(tutDir, "part-02.md")
	if got == nil || len(got.Findings) != 1 || got.Findings[0].Reveal == "" {
		t.Fatalf("LatestCheck = %+v", got)
	}
}

func TestCheckResultRejectsUnknownFieldsAndBadResults(t *testing.T) {
	homeDir := t.TempDir()
	setHome(t, homeDir)
	writeTutorial(t, homeDir, "test-slug", store.StatusUnverified, []string{"part-01.md"})
	t.Cleanup(func() { checkResultFile = ""; checkResultCmd.SetIn(nil) })

	checkResultFile = "-"
	for _, body := range []string{
		`{"part":"part-01.md","status":"pass","level":3}`,
		`{"part":"part-01.md","status":"fail"}`,
		`not json`,
	} {
		checkResultCmd.SetIn(strings.NewReader(body))
		if err := checkResultCmd.RunE(checkResultCmd, []string{"test-slug"}); err == nil {
			t.Errorf("check-result accepted %s", body)
		}
	}
}
