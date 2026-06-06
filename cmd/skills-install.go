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
	Short: "Write the bundled skills into Claude Code, Cursor, and/or Codex",
	Long: `Write the bundled Lathe skills into an agent's skills/commands directory.

Targets (--agent):
  claude-code   ./.claude/skills/<name>/SKILL.md   (--user: ~/.claude/skills/...)
  cursor        ./.cursor/commands/<slug>.md       (slash-invoked as /<slug>)
  codex         ./.agents/skills/<name>/SKILL.md   (--user: ~/.agents/skills/...)
  all           all of the above

By default skills install into the current project (cwd). Pass --user to install
Claude Code or Codex skills into your home directory instead (--user is fully
supported for both). Cursor has no standard user-level command directory, so
--user with cursor warns and falls back to the project ./.cursor/commands
directory. Codex consumes the same SKILL.md format as Claude Code, so its skills
ship verbatim.

Existing files are overwritten (install is idempotent).`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		all, err := skills.All()
		if err != nil {
			return err
		}

		var agents []string
		switch skillsAgent {
		case "claude-code", "cursor", "codex":
			agents = []string{skillsAgent}
		case "all":
			agents = []string{"claude-code", "cursor", "codex"}
		default:
			return fmt.Errorf("invalid --agent %q (want claude-code, cursor, codex, or all)", skillsAgent)
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
	skillsInstallCmd.Flags().StringVar(&skillsAgent, "agent", "claude-code", "target agent: claude-code, cursor, codex, or all")
	skillsInstallCmd.Flags().BoolVar(&skillsUser, "user", false, "install into the user home dir (Claude Code and Codex) instead of the project")
	skillsCmd.AddCommand(skillsInstallCmd)
}
