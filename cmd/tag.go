package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
	"github.com/spf13/cobra"
)

var (
	tagSet    []string
	tagAdd    []string
	tagRemove []string
)

// tagCmd mutates a tutorial's tags. Like verify-result and extend-commit, it is
// a write-only state mutation: the /lathe-tag skill (or the user) chooses the
// tags — the model work — and this command persists them, keeping the Go binary
// the sole writer of metadata.json.
var tagCmd = &cobra.Command{
	Use:   "tag <slug>",
	Short: "Set, add, or remove tags on a tutorial (used by the /lathe-tag skill)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug := args[0]
		if err := validateSlug(slug); err != nil {
			return err
		}
		if len(tagSet) == 0 && len(tagAdd) == 0 && len(tagRemove) == 0 {
			return fmt.Errorf("nothing to do: pass --set, --add, or --remove")
		}

		tutorialsDir, err := config.TutorialsDir()
		if err != nil {
			return err
		}
		tutDir := filepath.Join(tutorialsDir, slug)
		tut, err := store.ReadMetadata(tutDir)
		if err != nil {
			return fmt.Errorf("read metadata for %q: %w", slug, err)
		}

		// --set replaces the whole list; --add/--remove edit it in place.
		tags := tut.Tags
		if len(tagSet) > 0 {
			tags = splitTags(tagSet)
		}
		tags = append(tags, splitTags(tagAdd)...)
		tags = store.NormalizeTags(tags)

		if remove := store.NormalizeTags(splitTags(tagRemove)); len(remove) > 0 {
			drop := make(map[string]struct{}, len(remove))
			for _, r := range remove {
				drop[r] = struct{}{}
			}
			kept := tags[:0]
			for _, t := range tags {
				if _, ok := drop[t]; !ok {
					kept = append(kept, t)
				}
			}
			tags = kept
		}

		tut.Tags = tags
		if err := store.WriteMetadata(tutDir, tut); err != nil {
			return fmt.Errorf("write metadata: %w", err)
		}
		if len(tags) == 0 {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Cleared tags for %q\n", slug)
		} else {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Tags for %q: %s\n", slug, strings.Join(tags, ", "))
		}
		return nil
	},
}

func init() {
	tagCmd.Flags().StringArrayVar(&tagSet, "set", nil, "replace all tags (repeatable; accepts comma-separated values)")
	tagCmd.Flags().StringArrayVar(&tagAdd, "add", nil, "add a tag (repeatable; accepts comma-separated values)")
	tagCmd.Flags().StringArrayVar(&tagRemove, "remove", nil, "remove a tag (repeatable; accepts comma-separated values)")
	rootCmd.AddCommand(tagCmd)
}
