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
	Short: "Manage the bundled Lathe skills (install into Claude Code / Cursor / Codex / Gemini / opencode / Cline / Windsurf)",
}

// rawShipTarget describes an agent that consumes the raw SKILL.md verbatim —
// the format is now a cross-tool standard (name + description frontmatter)
// shared by Claude Code, Codex, Gemini CLI, opencode, Cline, and Windsurf, so
// these all ship the bytes unchanged. Cursor is the lone exception: it needs a
// markdown translation, so it keeps its own branch in installForAgent.
type rawShipTarget struct {
	display     string                          // human-facing name in output
	dir         func(user bool) (string, error) // resolves the skills dir
	projectOnly bool                            // no user-level dir; --user warns + falls back
}

// rawShipTargets keys each verbatim-ship agent to its display name + dir
// resolver. Adding a new SKILL.md-consuming harness is a one-line entry here
// plus a dir resolver, no new install branch.
var rawShipTargets = map[string]rawShipTarget{
	"claude-code": {display: "Claude Code", dir: claudeSkillsDir},
	"codex":       {display: "Codex", dir: codexSkillsDir},
	"gemini":      {display: "Gemini", dir: geminiSkillsDir},
	"opencode":    {display: "opencode", dir: opencodeSkillsDir},
	"cline":       {display: "Cline", dir: clineSkillsDir},
	"windsurf":    {display: "Windsurf", dir: windsurfSkillsDir, projectOnly: true},
}

// installForAgent writes every skill for one agent and returns the file count.
func installForAgent(out io.Writer, agent string, user bool, all []skills.Skill) (int, error) {
	if agent == "cursor" {
		return installCursor(out, user, all)
	}

	t, ok := rawShipTargets[agent]
	if !ok {
		return 0, fmt.Errorf("unknown agent %q", agent)
	}
	// projectOnly agents have no documented user-level skills dir, so --user
	// warns and falls back to the project (mirroring Cursor).
	if user && t.projectOnly {
		_, _ = fmt.Fprintf(out, "note: %s has no standard user-level skills dir; installing into the project instead.\n", t.display)
		user = false
	}
	dir, err := t.dir(user)
	if err != nil {
		return 0, err
	}
	_, _ = fmt.Fprintf(out, "%s -> %s\n", t.display, dir)
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

// installCursor writes the Cursor slash-command translation of every skill.
// Cursor is the one target that doesn't consume the raw SKILL.md (it needs the
// frontmatter stripped and a /<slug> header), and it has no standard user-level
// command dir, so --user warns and falls back to the project.
func installCursor(out io.Writer, user bool, all []skills.Skill) (int, error) {
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

// geminiSkillsDir returns the project (./.gemini/skills) or user
// (~/.gemini/skills) skills directory used by Gemini CLI.
func geminiSkillsDir(user bool) (string, error) {
	if !user {
		return filepath.Join(".gemini", "skills"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gemini", "skills"), nil
}

// opencodeSkillsDir returns the project (./.opencode/skills) or user
// (~/.config/opencode/skills, the XDG config dir) skills directory used by
// opencode.
func opencodeSkillsDir(user bool) (string, error) {
	if !user {
		return filepath.Join(".opencode", "skills"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "opencode", "skills"), nil
}

// clineSkillsDir returns the project (./.cline/skills) or user
// (~/.cline/skills) skills directory used by Cline.
func clineSkillsDir(user bool) (string, error) {
	if !user {
		return filepath.Join(".cline", "skills"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cline", "skills"), nil
}

// windsurfSkillsDir returns the project (./.windsurf/skills) skills directory
// used by Windsurf. Windsurf has no documented user-level skills dir, so this
// is project-only (installForAgent forces user=false before calling it).
func windsurfSkillsDir(_ bool) (string, error) {
	return filepath.Join(".windsurf", "skills"), nil
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
