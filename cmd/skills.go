package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/devenjarvis/lathe/internal/skills"
	"github.com/spf13/cobra"
)

// skillsCmd groups the bundled-skill commands (install/list, each in its own
// file per the one-subcommand-per-file convention). The skills themselves are
// embedded in the binary (internal/skills), so install works after a plain
// `brew install` / `go install` with no repo clone.
var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Manage the bundled Lathe skills (install into Claude Code / Cursor / Codex)",
}

// installForAgent writes every skill for one agent and returns the file count.
func installForAgent(out io.Writer, agent string, user bool, all []skills.Skill) (int, error) {
	switch agent {
	case "claude-code":
		dir, err := claudeSkillsDir(user)
		if err != nil {
			return 0, err
		}
		_, _ = fmt.Fprintf(out, "Claude Code -> %s\n", dir)
		count := 0
		for _, s := range all {
			dst := filepath.Join(dir, s.Slug, "SKILL.md")
			if err := writeSkillFile(out, dst, s.Raw); err != nil {
				return count, err
			}
			count++
		}
		return count, nil

	case "cursor":
		if user {
			_, _ = fmt.Fprintln(out, "note: Cursor has no standard user-level command dir; installing into the project instead.")
		}
		dir := filepath.Join(".cursor", "commands")
		_, _ = fmt.Fprintf(out, "Cursor -> %s\n", dir)
		count := 0
		for _, s := range all {
			dst := filepath.Join(dir, skills.CursorFilename(s))
			if err := writeSkillFile(out, dst, []byte(skills.CursorCommand(s))); err != nil {
				return count, err
			}
			count++
		}
		return count, nil

	case "codex":
		// Codex's Agent Skills use the same SKILL.md format as Claude Code
		// (name + description frontmatter), so we ship the raw bytes verbatim —
		// no translation needed (unlike Cursor). Codex also supports both
		// project- and user-level skills, so --user works with no fallback.
		dir, err := codexSkillsDir(user)
		if err != nil {
			return 0, err
		}
		_, _ = fmt.Fprintf(out, "Codex -> %s\n", dir)
		count := 0
		for _, s := range all {
			dst := filepath.Join(dir, s.Slug, "SKILL.md")
			if err := writeSkillFile(out, dst, s.Raw); err != nil {
				return count, err
			}
			count++
		}
		return count, nil
	}
	return 0, fmt.Errorf("unknown agent %q", agent)
}

// claudeSkillsDir returns the project (./.claude/skills) or user
// (~/.claude/skills) skills directory.
func claudeSkillsDir(user bool) (string, error) {
	if !user {
		return filepath.Join(".claude", "skills"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "skills"), nil
}

// codexSkillsDir returns the project (./.agents/skills) or user
// (~/.agents/skills) skills directory used by Codex's Agent Skills.
func codexSkillsDir(user bool) (string, error) {
	if !user {
		return filepath.Join(".agents", "skills"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agents", "skills"), nil
}

// writeSkillFile creates parent dirs and writes the file, reporting whether it
// was newly written or updated.
func writeSkillFile(out io.Writer, dst string, data []byte) error {
	verb := "wrote"
	if _, err := os.Stat(dst); err == nil {
		verb = "updated"
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("mkdir for %s: %w", dst, err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	_, _ = fmt.Fprintf(out, "  %s %s\n", verb, dst)
	return nil
}

func init() {
	rootCmd.AddCommand(skillsCmd)
}
