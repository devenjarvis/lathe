package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resetSkillsFlags restores the shared flag vars between cases.
func resetSkillsFlags(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		skillsAgent = "claude-code"
		skillsUser = false
	})
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected file %s: %v", path, err)
	}
}

func TestSkillsInstallClaudeProject(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	resetSkillsFlags(t)

	skillsAgent = "claude-code"
	if err := skillsInstallCmd.RunE(skillsInstallCmd, nil); err != nil {
		t.Fatalf("install: %v", err)
	}
	for _, slug := range []string{"lathe", "lathe-ask", "lathe-extend", "lathe-tag", "lathe-verify"} {
		mustExist(t, filepath.Join(dir, ".claude", "skills", slug, "SKILL.md"))
	}
}

func TestSkillsInstallClaudeUser(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Also chdir somewhere clean so a stray project dir can't mask a bug.
	t.Chdir(t.TempDir())
	resetSkillsFlags(t)

	skillsAgent = "claude-code"
	skillsUser = true
	if err := skillsInstallCmd.RunE(skillsInstallCmd, nil); err != nil {
		t.Fatalf("install --user: %v", err)
	}
	mustExist(t, filepath.Join(home, ".claude", "skills", "lathe", "SKILL.md"))
}

func TestSkillsInstallCursorStripsFrontmatter(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	resetSkillsFlags(t)

	skillsAgent = "cursor"
	if err := skillsInstallCmd.RunE(skillsInstallCmd, nil); err != nil {
		t.Fatalf("install cursor: %v", err)
	}
	path := filepath.Join(dir, ".cursor", "commands", "lathe.md")
	mustExist(t, path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if strings.HasPrefix(body, "---") {
		t.Errorf("cursor command should not start with YAML frontmatter:\n%s", body[:80])
	}
	if !strings.Contains(body, "# /lathe") {
		t.Errorf("expected /lathe header in cursor command")
	}
}

func TestSkillsInstallCursorUserFallsBackToProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	t.Chdir(dir)
	resetSkillsFlags(t)

	var buf strings.Builder
	skillsInstallCmd.SetOut(&buf)
	t.Cleanup(func() { skillsInstallCmd.SetOut(nil) })

	skillsAgent = "cursor"
	skillsUser = true
	if err := skillsInstallCmd.RunE(skillsInstallCmd, nil); err != nil {
		t.Fatalf("install cursor --user: %v", err)
	}
	if !strings.Contains(buf.String(), "no standard user-level") {
		t.Errorf("expected a cursor --user warning, got:\n%s", buf.String())
	}
	// Falls back to the project dir, not the user home.
	mustExist(t, filepath.Join(dir, ".cursor", "commands", "lathe.md"))
	if _, err := os.Stat(filepath.Join(home, ".cursor")); err == nil {
		t.Errorf("cursor --user should not write into the home dir")
	}
}

func TestSkillsInstallCodexProject(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	resetSkillsFlags(t)

	skillsAgent = "codex"
	if err := skillsInstallCmd.RunE(skillsInstallCmd, nil); err != nil {
		t.Fatalf("install codex: %v", err)
	}
	for _, slug := range []string{"lathe", "lathe-ask", "lathe-extend", "lathe-tag", "lathe-verify"} {
		mustExist(t, filepath.Join(dir, ".agents", "skills", slug, "SKILL.md"))
	}
	// Codex ships the raw SKILL.md, so the frontmatter is preserved (the
	// inverse of the Cursor test, which strips it).
	data, err := os.ReadFile(filepath.Join(dir, ".agents", "skills", "lathe", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "---") {
		t.Errorf("codex skill should ship raw with YAML frontmatter, got:\n%.80s", data)
	}
}

func TestSkillsInstallCodexUser(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Also chdir somewhere clean so a stray project dir can't mask a bug.
	t.Chdir(t.TempDir())
	resetSkillsFlags(t)

	skillsAgent = "codex"
	skillsUser = true
	if err := skillsInstallCmd.RunE(skillsInstallCmd, nil); err != nil {
		t.Fatalf("install codex --user: %v", err)
	}
	mustExist(t, filepath.Join(home, ".agents", "skills", "lathe", "SKILL.md"))
}

// TestSkillsInstallRawShipAgents covers the agents that consume the raw
// SKILL.md verbatim and have both a project and a user-level dir (gemini,
// opencode, cline). claude-code and codex are covered by their own tests above;
// windsurf is project-only and covered separately below.
func TestSkillsInstallRawShipAgents(t *testing.T) {
	cases := []struct {
		agent      string
		projectDir []string // path segments under the project root
		userDir    []string // path segments under $HOME for --user
	}{
		{"gemini", []string{".gemini", "skills"}, []string{".gemini", "skills"}},
		{"opencode", []string{".opencode", "skills"}, []string{".config", "opencode", "skills"}},
		{"cline", []string{".cline", "skills"}, []string{".cline", "skills"}},
	}
	for _, tc := range cases {
		t.Run(tc.agent+"-project", func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			resetSkillsFlags(t)

			skillsAgent = tc.agent
			if err := skillsInstallCmd.RunE(skillsInstallCmd, nil); err != nil {
				t.Fatalf("install %s: %v", tc.agent, err)
			}
			path := filepath.Join(dir, filepath.Join(tc.projectDir...), "lathe", "SKILL.md")
			mustExist(t, path)
			// Raw ship: the YAML frontmatter is preserved verbatim.
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(string(data), "---") {
				t.Errorf("%s skill should ship raw with YAML frontmatter, got:\n%.80s", tc.agent, data)
			}
		})
		t.Run(tc.agent+"-user", func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			// Chdir somewhere clean so a stray project dir can't mask a bug.
			t.Chdir(t.TempDir())
			resetSkillsFlags(t)

			skillsAgent = tc.agent
			skillsUser = true
			if err := skillsInstallCmd.RunE(skillsInstallCmd, nil); err != nil {
				t.Fatalf("install %s --user: %v", tc.agent, err)
			}
			mustExist(t, filepath.Join(home, filepath.Join(tc.userDir...), "lathe", "SKILL.md"))
		})
	}
}

func TestSkillsInstallWindsurfProject(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	resetSkillsFlags(t)

	skillsAgent = "windsurf"
	if err := skillsInstallCmd.RunE(skillsInstallCmd, nil); err != nil {
		t.Fatalf("install windsurf: %v", err)
	}
	mustExist(t, filepath.Join(dir, ".windsurf", "skills", "lathe", "SKILL.md"))
}

// TestSkillsInstallWindsurfUserFallsBackToProject mirrors the Cursor case:
// Windsurf is project-only, so --user warns and writes into the project rather
// than the home dir.
func TestSkillsInstallWindsurfUserFallsBackToProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	t.Chdir(dir)
	resetSkillsFlags(t)

	var buf strings.Builder
	skillsInstallCmd.SetOut(&buf)
	t.Cleanup(func() { skillsInstallCmd.SetOut(nil) })

	skillsAgent = "windsurf"
	skillsUser = true
	if err := skillsInstallCmd.RunE(skillsInstallCmd, nil); err != nil {
		t.Fatalf("install windsurf --user: %v", err)
	}
	if !strings.Contains(buf.String(), "no standard user-level") {
		t.Errorf("expected a windsurf --user warning, got:\n%s", buf.String())
	}
	// Falls back to the project dir, not the user home.
	mustExist(t, filepath.Join(dir, ".windsurf", "skills", "lathe", "SKILL.md"))
	if _, err := os.Stat(filepath.Join(home, ".windsurf")); err == nil {
		t.Errorf("windsurf --user should not write into the home dir")
	}
}

func TestSkillsInstallAll(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	resetSkillsFlags(t)

	skillsAgent = "all"
	if err := skillsInstallCmd.RunE(skillsInstallCmd, nil); err != nil {
		t.Fatalf("install all: %v", err)
	}
	// "all" expands to every target.
	mustExist(t, filepath.Join(dir, ".claude", "skills", "lathe", "SKILL.md"))
	mustExist(t, filepath.Join(dir, ".cursor", "commands", "lathe.md"))
	mustExist(t, filepath.Join(dir, ".agents", "skills", "lathe", "SKILL.md"))
	mustExist(t, filepath.Join(dir, ".gemini", "skills", "lathe", "SKILL.md"))
	mustExist(t, filepath.Join(dir, ".opencode", "skills", "lathe", "SKILL.md"))
	mustExist(t, filepath.Join(dir, ".cline", "skills", "lathe", "SKILL.md"))
	mustExist(t, filepath.Join(dir, ".windsurf", "skills", "lathe", "SKILL.md"))
}

// TestSkillsInstallAllUser exercises the one non-trivial interaction the
// multi-agent expansion introduced: with --agent all --user, the projectOnly
// fallback (windsurf) and Cursor's no-user-dir fallback both fire live inside
// the install loop, while every other target lands in the user home dir.
func TestSkillsInstallAllUser(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	proj := t.TempDir()
	t.Chdir(proj)
	resetSkillsFlags(t)

	skillsAgent = "all"
	skillsUser = true
	if err := skillsInstallCmd.RunE(skillsInstallCmd, nil); err != nil {
		t.Fatalf("install all --user: %v", err)
	}
	// Raw-ship targets with a user dir land in $HOME.
	mustExist(t, filepath.Join(home, ".claude", "skills", "lathe", "SKILL.md"))
	mustExist(t, filepath.Join(home, ".agents", "skills", "lathe", "SKILL.md"))
	mustExist(t, filepath.Join(home, ".gemini", "skills", "lathe", "SKILL.md"))
	mustExist(t, filepath.Join(home, ".config", "opencode", "skills", "lathe", "SKILL.md"))
	mustExist(t, filepath.Join(home, ".cline", "skills", "lathe", "SKILL.md"))
	// Project-only / no-user-dir targets fall back to the project even under --user.
	mustExist(t, filepath.Join(proj, ".windsurf", "skills", "lathe", "SKILL.md"))
	mustExist(t, filepath.Join(proj, ".cursor", "commands", "lathe.md"))
	if _, err := os.Stat(filepath.Join(home, ".windsurf")); err == nil {
		t.Errorf("windsurf --user should not write into the home dir")
	}
	if _, err := os.Stat(filepath.Join(home, ".cursor")); err == nil {
		t.Errorf("cursor --user should not write into the home dir")
	}
}

func TestSkillsInstallInvalidAgent(t *testing.T) {
	t.Chdir(t.TempDir())
	resetSkillsFlags(t)
	skillsAgent = "vim"
	if err := skillsInstallCmd.RunE(skillsInstallCmd, nil); err == nil {
		t.Error("expected error for invalid --agent")
	}
}

func TestSkillsInstallIdempotent(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	resetSkillsFlags(t)

	skillsAgent = "claude-code"
	if err := skillsInstallCmd.RunE(skillsInstallCmd, nil); err != nil {
		t.Fatalf("first install: %v", err)
	}
	// Second run must overwrite without error.
	if err := skillsInstallCmd.RunE(skillsInstallCmd, nil); err != nil {
		t.Fatalf("second install: %v", err)
	}
	mustExist(t, filepath.Join(dir, ".claude", "skills", "lathe", "SKILL.md"))
}

func TestSkillsList(t *testing.T) {
	var sb strings.Builder
	skillsListCmd.SetOut(&sb)
	t.Cleanup(func() { skillsListCmd.SetOut(nil) })
	if err := skillsListCmd.RunE(skillsListCmd, nil); err != nil {
		t.Fatalf("list: %v", err)
	}
	out := sb.String()
	for _, slug := range []string{"lathe", "lathe-ask", "lathe-extend", "lathe-tag", "lathe-verify"} {
		if !strings.Contains(out, slug) {
			t.Errorf("list output missing %q:\n%s", slug, out)
		}
	}
}
