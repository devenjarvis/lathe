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

func TestSkillsInstallAll(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	resetSkillsFlags(t)

	skillsAgent = "all"
	if err := skillsInstallCmd.RunE(skillsInstallCmd, nil); err != nil {
		t.Fatalf("install all: %v", err)
	}
	mustExist(t, filepath.Join(dir, ".claude", "skills", "lathe", "SKILL.md"))
	mustExist(t, filepath.Join(dir, ".cursor", "commands", "lathe.md"))
	mustExist(t, filepath.Join(dir, ".agents", "skills", "lathe", "SKILL.md"))
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
