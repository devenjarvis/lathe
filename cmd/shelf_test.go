package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/devenjarvis/lathe/internal/store"
	"github.com/spf13/cobra"
)

func runShelf(t *testing.T, c *cobra.Command, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	c.SetOut(&out)
	t.Cleanup(func() { c.SetOut(nil) })
	err := c.RunE(c, args)
	return out.String(), err
}

func TestShelfNewSetList(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	writeTutorial(t, home, "synth", store.StatusUnverified, []string{"part-01.md"})
	writeTutorial(t, home, "kernel", store.StatusUnverified, []string{"part-01.md"})

	out, err := runShelf(t, shelfNewCmd, "Audio & DSP")
	if err != nil || !strings.Contains(out, `Created shelf "Audio & DSP" (audio-dsp)`) {
		t.Fatalf("new: %q, %v", out, err)
	}
	if _, err := runShelf(t, shelfNewCmd, "audio & dsp"); err == nil {
		t.Error("duplicate shelf name accepted")
	}
	out, err = runShelf(t, shelfSetCmd, "synth", "Audio & DSP")
	if err != nil || !strings.Contains(out, "synth is on Audio & DSP") {
		t.Fatalf("set: %q, %v", out, err)
	}
	if _, err := runShelf(t, shelfSetCmd, "synth", "nope"); err == nil {
		t.Error("set onto a missing shelf succeeded")
	}
	if _, err := runShelf(t, shelfSetCmd, "missing", "audio-dsp"); err == nil {
		t.Error("set for a missing tutorial succeeded")
	}

	out, err = runShelf(t, shelfListCmd)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"audio-dsp", "1  Audio & DSP", "unsorted", "1  Unsorted"} {
		if !strings.Contains(out, want) {
			t.Errorf("list missing %q:\n%s", want, out)
		}
	}

	if _, err := runShelf(t, shelfSetCmd, "synth", "unsorted"); err != nil {
		t.Fatal(err)
	}
	path, _ := store.DefaultShelvesPath()
	s, _ := store.ReadShelves(path)
	if !s.ShelfOf("synth").Unsorted() {
		t.Error("set unsorted did not take the tutorial off its shelf")
	}
}
