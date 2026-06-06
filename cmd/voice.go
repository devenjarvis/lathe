package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
	"github.com/devenjarvis/lathe/internal/voice"
	"github.com/spf13/cobra"
)

// voiceCmd groups the writing-voice commands. Voices are tone/register presets
// (and user-authored custom voices) the /lathe skill fetches via `voice show`.
// The CLI owns the durable voice files and the default-voice config; skills only
// read (show/list) or hand content to `voice add`. This is the first skill→CLI
// read path, but the boundary holds: the binary is still the sole owner of
// ~/.lathe state.
var voiceCmd = &cobra.Command{
	Use:   "voice",
	Short: "Manage writing voices for generated tutorials",
}

var voiceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available voices (built-in presets + custom), marking the default",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		voices, err := voice.List()
		if err != nil {
			return err
		}
		// Normalize the configured default the same way voice names are keyed, so
		// a hand-edited mixed-case config.json still marks the right entry (this
		// mirrors the EqualFold reset in `voice rm`).
		def := store.NormalizeVoice(config.DefaultVoice())
		out := cmd.OutOrStdout()
		for _, v := range voices {
			marker := " "
			if v.Name == def {
				marker = "*"
			}
			kind := "custom"
			if v.Builtin {
				kind = "built-in"
			}
			_, _ = fmt.Fprintf(out, "%s %-14s %-9s %s\n", marker, v.Name, kind, v.Description)
		}
		_, _ = fmt.Fprintf(out, "\n* = default (set with `lathe voice set-default <name>`)\n")
		return nil
	},
}

var voiceShowTutorial string

var voiceShowCmd = &cobra.Command{
	Use:   "show [name]",
	Short: "Print a voice's spec (wrapped with the fixed guardrail preamble)",
	Long: "Print the wrapped spec markdown for a voice. With no name, prints the configured default. " +
		"With --tutorial <slug>, prints the voice recorded on that tutorial (falling back to the default " +
		"for tutorials stored before voices existed). This is the read path the /lathe and /lathe-extend " +
		"skills use.",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, err := resolveShowName(args, voiceShowTutorial)
		if err != nil {
			return err
		}
		v, err := voice.Resolve(name)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprint(cmd.OutOrStdout(), v.Wrapped())
		return nil
	},
}

// resolveShowName decides which voice `voice show` should print, in priority
// order: an explicit name argument, then the voice recorded on --tutorial (or
// the default if that tutorial has none), then the configured default.
func resolveShowName(args []string, tutorialSlug string) (string, error) {
	if len(args) == 1 {
		return args[0], nil
	}
	if tutorialSlug != "" {
		if err := validateSlug(tutorialSlug); err != nil {
			return "", err
		}
		tutorialsDir, err := config.TutorialsDir()
		if err != nil {
			return "", err
		}
		tut, err := store.ReadMetadata(filepath.Join(tutorialsDir, tutorialSlug))
		if err != nil {
			return "", fmt.Errorf("read metadata for %q: %w", tutorialSlug, err)
		}
		if tut.Voice != "" {
			return tut.Voice, nil
		}
		// Pre-feature tutorial with no recorded voice → fall back to the default.
	}
	return config.DefaultVoice(), nil
}

var voiceSetDefaultCmd = &cobra.Command{
	Use:   "set-default <name>",
	Short: "Set the default voice used when a /lathe run names none",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate the voice exists before recording it as the default.
		v, err := voice.Resolve(args[0])
		if err != nil {
			return err
		}
		if err := config.SetDefaultVoice(v.Name); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Default voice set to %q.\n", v.Name)
		return nil
	},
}

var voiceAddFile string

var voiceAddCmd = &cobra.Command{
	Use:   "add <name> --file <path>",
	Short: "Add a custom voice from a file (use --file - for stdin)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if voiceAddFile == "" {
			return fmt.Errorf("--file is required (use --file - to read the spec from stdin)")
		}
		content, err := readSpec(cmd, voiceAddFile)
		if err != nil {
			return err
		}
		if err := voice.Add(args[0], content); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Added custom voice %q. Use it with `/lathe ... voice: %s` or `lathe voice set-default %s`.\n", args[0], args[0], args[0])
		return nil
	},
}

// readSpec loads a voice spec from a file path, or from stdin when path is "-".
func readSpec(cmd *cobra.Command, path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(cmd.InOrStdin())
	}
	return os.ReadFile(path)
}

var voiceRmCmd = &cobra.Command{
	Use:   "rm <name>",
	Short: "Remove a custom voice (built-ins cannot be removed)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := store.NormalizeVoice(args[0])
		// If the voice being removed is the current default, reset the default to
		// the built-in plainspoken so no tutorial resolves a dangling voice.
		// EqualFold so a hand-edited, mixed-case config.json default still matches.
		wasDefault := strings.EqualFold(config.DefaultVoice(), name)
		if err := voice.Remove(args[0]); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed custom voice %q.\n", name)
		if wasDefault {
			if err := config.SetDefaultVoice(config.DefaultVoiceName); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "It was the default; reset default to %q.\n", config.DefaultVoiceName)
		}
		return nil
	},
}

func init() {
	voiceShowCmd.Flags().StringVar(&voiceShowTutorial, "tutorial", "", "print the voice recorded on this tutorial slug (falls back to the default)")
	voiceAddCmd.Flags().StringVar(&voiceAddFile, "file", "", "path to the voice spec markdown, or - for stdin (required)")
	voiceCmd.AddCommand(voiceListCmd, voiceShowCmd, voiceSetDefaultCmd, voiceAddCmd, voiceRmCmd)
	rootCmd.AddCommand(voiceCmd)
}
