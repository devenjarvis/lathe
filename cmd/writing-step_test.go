package cmd

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devenjarvis/lathe/internal/store"
)

func TestWritingStepRecordsProgress(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	res, dir, err := store.ReserveNew(filepath.Join(home, ".lathe", "tutorials"), store.NewReservation{Topic: "synth"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { writingStepSources, writingStepAsk, writingStepReplies = nil, "", nil })
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetArgs([]string{"writing-step", res.Slug, "research", "--source", "Zig docs|https://ziglang.org/documentation/"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "step research, 1 source") {
		t.Errorf("output = %q", out.String())
	}
	w, _ := store.ReadWriting(dir)
	if w == nil || w.Step != "research" || len(w.Sources) != 1 || w.Sources[0].Title != "Zig docs" {
		t.Errorf("writing = %+v", w)
	}

	// A stored tutorial takes no progress reports.
	writeTutorial(t, home, "done", store.StatusUnverified, []string{"part-01.md"})
	writingStepSources = nil
	rootCmd.SetArgs([]string{"writing-step", "done", "write"})
	if err := rootCmd.Execute(); err == nil || !strings.Contains(err.Error(), "not being written") {
		t.Errorf("writing-step on a stored tutorial = %v", err)
	}
}
