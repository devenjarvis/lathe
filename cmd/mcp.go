package cmd

import (
	"github.com/devenjarvis/lathe/internal/buildinfo"
	"github.com/devenjarvis/lathe/internal/mcp"
	"github.com/devenjarvis/lathe/internal/store"
	"github.com/spf13/cobra"
)

var mcpSlug string

// mcpCmd serves the skills' write commands as MCP tools over stdio.
// `lathe serve --agent` hands it to the hosted agent in every ACP session
// (scoped with --slug); any MCP-capable agent can also run it directly. The
// tools call the same internal/store functions as the CLI commands.
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
		s := &mcp.Server{Slug: mcpSlug, Version: buildinfo.Resolve()}
		return s.Serve(cmd.InOrStdin(), cmd.OutOrStdout())
	},
}

func init() {
	mcpCmd.Flags().StringVar(&mcpSlug, "slug", "", "scope the tools to one tutorial (lathe serve sets this per agent session)")
	rootCmd.AddCommand(mcpCmd)
}
