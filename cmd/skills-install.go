package cmd

import (
	"fmt"

	"github.com/devenjarvis/lathe/internal/skills"
	"github.com/spf13/cobra"
)

var (
	skillsAgent string
	skillsUser  bool
)

var skillsInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Write the bundled skills into Claude Code, Cursor, Codex, Gemini, opencode, Cline, Windsurf, and/or Antigravity",
	Long: `Write the bundled Lathe skills into an agent's skills/commands directory.

Targets (--agent):
  claude-code   ./.claude/skills/<name>/SKILL.md     (--user: ~/.claude/skills/...)
  cursor        ./.cursor/commands/<slug>.md         (slash-invoked as /<slug>)
  codex         ./.agents/skills/<name>/SKILL.md     (--user: ~/.agents/skills/...)
  gemini        ./.gemini/skills/<name>/SKILL.md     (--user: ~/.gemini/skills/...)
  opencode      ./.opencode/skills/<name>/SKILL.md   (--user: ~/.config/opencode/skills/...)
  cline         ./.cline/skills/<name>/SKILL.md      (--user: ~/.cline/skills/...)
  windsurf      ./.windsurf/skills/<name>/SKILL.md   (project-only; --user falls back)
  antigravity   ./.antigravity/skills/<name>/SKILL.md (--user: ~/.gemini/config/plugins/custom-skills/skills/...)
  all           all of the above

By default skills install into the current project (cwd). Pass --user to install
into your home directory instead — supported for every target except Cursor and
Windsurf, which have no standard user-level dir and warn + fall back to the
project. The SKILL.md format is a cross-tool standard, so every target except
Cursor ships the raw bytes verbatim; Cursor gets a translated /<slug> command.

Existing files are overwritten (install is idempotent).`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		all, err := skills.All()
		if err != nil {
			return err
		}

		var agents []string
		switch skillsAgent {
		case "claude-code", "cursor", "codex", "gemini", "opencode", "cline", "windsurf", "antigravity":
			agents = []string{skillsAgent}
		case "all":
			agents = []string{"claude-code", "cursor", "codex", "gemini", "opencode", "cline", "windsurf", "antigravity"}
		default:
			return fmt.Errorf("invalid --agent %q (want claude-code, cursor, codex, gemini, opencode, cline, windsurf, antigravity, or all)", skillsAgent)
		}

		out := cmd.OutOrStdout()
		total := 0
		for _, agent := range agents {
			n, err := installForAgent(out, agent, skillsUser, all)
			if err != nil {
				return err
			}
			total += n
		}
		_, _ = fmt.Fprintf(out, "\nInstalled %d skill file(s).\n", total)
		return nil
	},
}

func init() {
	skillsInstallCmd.Flags().StringVar(&skillsAgent, "agent", "claude-code", "target agent: claude-code, cursor, codex, gemini, opencode, cline, windsurf, antigravity, or all")
	skillsInstallCmd.Flags().BoolVar(&skillsUser, "user", false, "install into the user home dir instead of the project (cursor/windsurf are project-only: warn and fall back to the project)")
	skillsCmd.AddCommand(skillsInstallCmd)
}
