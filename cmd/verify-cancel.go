package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
	"github.com/spf13/cobra"
)

// verifyCancelCmd recovers a tutorial whose verification was interrupted. The
// /lathe-verify skill marks the tutorial "verifying" when it starts, but if the
// session dies before recording a terminal status (token limit, accidental
// cancellation, or a verify started by mistake), the tutorial is stuck
// with the badge spinning forever and the web verify endpoint refuses to
// re-run. This command resets the status without hand-editing metadata.json or
// manually forcing verify-result to fail to fail with appropriate arguments.
//
// When `lathe serve --agent` is running the verification on its hosted agent,
// the command first asks the server to stop it (session/cancel); the server
// then restores the status itself.
var verifyCancelCmd = &cobra.Command{
	Use:   "verify-cancel <slug>",
	Short: "Cancel an interrupted verification and restore the previous status",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug := args[0]
		if err := validateSlug(slug); err != nil {
			return err
		}
		tutorialsDir, err := config.TutorialsDir()
		if err != nil {
			return err
		}
		tutDir := filepath.Join(tutorialsDir, slug)
		if _, err := store.ReadMetadata(tutDir); err != nil {
			return fmt.Errorf("no stored tutorial %q: %w", slug, err)
		}

		stopped := stopHostedVerify(slug)
		status, err := store.CancelVerify(tutDir)
		switch {
		case err == nil:
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Cancelled verification of %q (status: %s)\n", slug, status)
			return nil
		case stopped && errors.Is(err, store.ErrNotInFlight):
			// The server stopped its agent and already restored the status.
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Stopped the hosted agent's verification of %q (status: %s)\n", slug, status)
			return nil
		case errors.Is(err, store.ErrNotInFlight):
			return fmt.Errorf("%q is not verifying (status: %s) — nothing to cancel", slug, status)
		default:
			return err
		}
	},
}

// stopHostedVerify asks a running `lathe serve` to stop a verification its
// hosted agent is running for slug. It reports whether one was stopped; no
// server, no hosted run or an unreachable server all mean false.
func stopHostedVerify(slug string) bool {
	base, err := serveBaseURL()
	if err != nil {
		return false
	}
	req, err := http.NewRequest(http.MethodPost, base+"/-/verify/"+url.PathEscape(slug)+"/cancel", nil)
	if err != nil {
		return false
	}
	// The server waits for the agent to wind down before it answers.
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusNoContent
}

func init() {
	rootCmd.AddCommand(verifyCancelCmd)
}
