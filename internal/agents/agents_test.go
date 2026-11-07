package agents

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func fakePath(found ...string) LookPath {
	set := map[string]bool{}
	for _, f := range found {
		set[f] = true
	}
	return func(file string) (string, error) {
		if set[file] {
			return "/usr/bin/" + file, nil
		}
		return "", errors.New("not found")
	}
}

func TestDetect(t *testing.T) {
	home := t.TempDir()
	skill := filepath.Join(home, ".claude", "skills", "lathe", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := map[string]Detected{}
	for _, d := range Detect(fakePath("claude", "claude-agent-acp", "codex", "opencode"), home) {
		got[d.ID] = d
	}
	if len(got) != len(Registry) {
		t.Fatalf("Detect returned %d entries, want %d", len(got), len(Registry))
	}

	c := got["claude"]
	if !c.Found || !c.Ready || c.SkillsInstalled == nil || !*c.SkillsInstalled {
		t.Errorf("claude = %+v, want found, ready, skills installed", c)
	}
	x := got["codex"]
	if !x.Found || x.Ready {
		t.Errorf("codex = %+v, want found but not ready (no codex-acp)", x)
	}
	if x.SkillsInstalled == nil || *x.SkillsInstalled {
		t.Errorf("codex skills = %v, want false", x.SkillsInstalled)
	}
	// "opencode acp" runs the opencode binary itself.
	if o := got["opencode"]; !o.Found || !o.Ready {
		t.Errorf("opencode = %+v, want found and ready", o)
	}
	if g := got["goose"]; g.Found || g.Ready || g.SkillsInstalled != nil {
		t.Errorf("goose = %+v, want not found and no skills dir", g)
	}
}

func TestDetectAdapterAloneCountsAsFound(t *testing.T) {
	for _, d := range Detect(fakePath("codex-acp"), "") {
		if d.ID == "codex" && (!d.Found || !d.Ready) {
			t.Errorf("codex with only codex-acp on PATH = %+v, want found and ready", d)
		}
	}
}

func TestLookups(t *testing.T) {
	if a, ok := ByID("gemini"); !ok || a.Command != "gemini --experimental-acp" {
		t.Errorf("ByID(gemini) = %+v, %v", a, ok)
	}
	if _, ok := ByID("nope"); ok {
		t.Error("ByID(nope) found an agent")
	}
	if got := DisplayName("  claude-agent-acp "); got != "Claude Code" {
		t.Errorf("DisplayName(claude-agent-acp) = %q", got)
	}
	if got := DisplayName("gemini   --experimental-acp"); got != "Gemini CLI" {
		t.Errorf("DisplayName(gemini) = %q", got)
	}
	if got := DisplayName("/opt/bin/my-agent --acp"); got != "my-agent" {
		t.Errorf("DisplayName(custom) = %q", got)
	}
	if got := DisplayName(""); got != "" {
		t.Errorf("DisplayName(empty) = %q", got)
	}
}
