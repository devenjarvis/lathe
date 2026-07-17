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
	Short: "Manage the bundled Lathe skills (install into Claude Code / Cursor / Codex / Gemini / opencode / Cline / Windsurf / Antigravity)",
}

// rawShipTarget describes an agent that consumes the raw SKILL.md verbatim —
// the format is now a cross-tool standard (name + description frontmatter)
// shared by Claude Code, Codex, Gemini CLI, opencode, Cline, Windsurf, and
// Antigravity, so these all ship the bytes unchanged. Cursor is the lone
// exception: it needs a markdown translation, so it keeps its own branch in
// installForAgent.
//
// The path-segment data drives a single generic resolver (rawShipDir): project
// holds the segments under the project root, user holds the segments under $HOME
// for --user. A nil/empty user means the target has no user-level dir
// (project-only; --user warns + falls back to the project).
type rawShipTarget struct {
	display string   // human-facing name in output
	project []string // path segments under the project root for the skills dir
	user    []string // path segments under $HOME for --user; nil/empty ⇒ project-only
}

// rawShipTargets keys each verbatim-ship agent to its display name + path
// segments. Adding a new SKILL.md-consuming harness is a one-line entry here,
// no new resolver or install branch.
var rawShipTargets = map[string]rawShipTarget{
	"claude-code": {display: "Claude Code", project: []string{".claude", "skills"}, user: []string{".claude", "skills"}},
	"codex":       {display: "Codex", project: []string{".agents", "skills"}, user: []string{".agents", "skills"}},
	"gemini":      {display: "Gemini", project: []string{".gemini", "skills"}, user: []string{".gemini", "skills"}},
	"opencode":    {display: "opencode", project: []string{".opencode", "skills"}, user: []string{".config", "opencode", "skills"}},
	"cline":       {display: "Cline", project: []string{".cline", "skills"}, user: []string{".cline", "skills"}},
	"windsurf":    {display: "Windsurf", project: []string{".windsurf", "skills"}},
	"antigravity": {display: "Antigravity", project: []string{".antigravity", "skills"}, user: []string{".gemini", "config", "plugins", "custom-skills", "skills"}},
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
	// Targets with no user-level skills dir fall back to the project under --user
	// (mirroring Cursor), warning so the user knows where it landed.
	if user && len(t.user) == 0 {
		_, _ = fmt.Fprintf(out, "note: %s has no standard user-level skills dir; installing into the project instead.\n", t.display)
		user = false
	}
	dir, err := rawShipDir(t, user)
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

// rawShipDir resolves a raw-ship target's skills dir: the project-relative
// segments by default, or the segments under the user home dir when user is
// true. A target with no user segments is project-only.
func rawShipDir(t rawShipTarget, user bool) (string, error) {
	if !user {
		return filepath.Join(t.project...), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{home}, t.user...)...), nil
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
