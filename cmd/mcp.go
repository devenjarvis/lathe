package cmd

import (
	"errors"
	"fmt"

	"github.com/devenjarvis/lathe/internal/buildinfo"
	"github.com/devenjarvis/lathe/internal/mcp"
	"github.com/devenjarvis/lathe/internal/store"
	"github.com/devenjarvis/lathe/internal/voice"
	"github.com/spf13/cobra"
)

var (
	mcpSlug string
	mcpNew  bool
	// mcpVoiceDraft is a New voice session's reserved voice name.
	mcpVoiceDraft string
	mcpPropose    string
	mcpShelf      string
)

// mcpCmd serves the skills' write commands as MCP tools over stdio.
// `lathe serve --agent` hands it to the hosted agent in every ACP session
// (scoped with --slug; a new-tutorial run adds --new; a New voice run uses
// --voice-draft); any MCP-capable agent
// can also run it directly. The tools call the same internal/store functions
// as the CLI commands.
var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Serve Lathe's write commands (store, verify-result, extend, tag, voice add) as MCP tools over stdio",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if mcpSlug != "" {
			if err := store.ValidateSlug(mcpSlug); err != nil {
				return err
			}
		}
		if mcpNew && mcpSlug == "" {
			return errors.New("--new needs --slug: the reserved tutorial to store into")
		}
		if mcpVoiceDraft != "" {
			if mcpSlug != "" {
				return errors.New("--voice-draft and --slug can't be combined")
			}
			if mcpPropose != "" {
				return errors.New("--voice-draft and --propose can't be combined")
			}
			if !voice.IsSlug(mcpVoiceDraft) {
				return fmt.Errorf("invalid voice name %q", mcpVoiceDraft)
			}
		}
		switch mcpPropose {
		case "":
			if mcpShelf != "" {
				return errors.New("--shelf goes with --propose split")
			}
		case mcp.ProposeSort, mcp.ProposeSplit:
			if mcpSlug != "" {
				return errors.New("--propose and --slug don't mix")
			}
			if (mcpPropose == mcp.ProposeSplit) != (mcpShelf != "") {
				return errors.New("--propose split needs --shelf <id> (and only split takes it)")
			}
		default:
			return errors.New("--propose is sort or split")
		}
		s := &mcp.Server{Slug: mcpSlug, New: mcpNew, Propose: mcpPropose, Shelf: mcpShelf, VoiceDraft: mcpVoiceDraft, Version: buildinfo.Resolve()}
		return s.Serve(cmd.InOrStdin(), cmd.OutOrStdout())
	},
}

func init() {
	mcpCmd.Flags().StringVar(&mcpSlug, "slug", "", "scope the tools to one tutorial (lathe serve sets this per agent session)")
	mcpCmd.Flags().BoolVar(&mcpNew, "new", false, "with --slug: a new-tutorial session; also offer lathe_store, storing only into that reserved slug")
	mcpCmd.Flags().StringVar(&mcpVoiceDraft, "voice-draft", "", "a New voice session: offer only the voice tools, drafting and saving only this reserved voice name")
	mcpCmd.Flags().StringVar(&mcpPropose, "propose", "", "a shelf-suggestion session: sort (propose a shelf per tutorial) or split (propose splitting --shelf); offers only the proposal tools")
	mcpCmd.Flags().StringVar(&mcpShelf, "shelf", "", "with --propose split: the shelf to propose a split for")
	rootCmd.AddCommand(mcpCmd)
}
