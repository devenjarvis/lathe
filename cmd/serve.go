package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/devenjarvis/lathe/internal/agents"
	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/serve"
	"github.com/spf13/cobra"
)

var (
	servePort  int
	serveAgent string
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the tutorial web server and open the browser",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := config.TutorialsDir()
		if err != nil {
			return err
		}
		srv := serve.NewServer(dir)
		// --agent wins; otherwise host the agent picked in the web UI's
		// "Bring your agent" panel (remembered in ~/.lathe/config.json).
		agentCmd := serveAgent
		if agentCmd == "" {
			if a, ok := agents.ByID(config.Agent()); ok {
				agentCmd = a.Command
			}
		}
		if agentCmd != "" {
			srv.SetAgent(agentCmd)
		}
		defer srv.Close()
		url := fmt.Sprintf("http://localhost:%d", servePort)

		// Record the running server so the worker CLI (`lathe work ...`) can find
		// its URL, and clean it up on shutdown. Best-effort: a failed write only
		// means the worker can't auto-discover the server, not that serving fails.
		rt := &config.ServeRuntime{URL: url, PID: os.Getpid(), Started: time.Now().UTC()}
		if werr := config.WriteServeRuntime(rt); werr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "warning: could not write serve runtime file: %v\n", werr)
		}
		defer func() { _ = config.RemoveServeRuntime() }()

		fmt.Printf("Serving tutorials at %s\n", url)
		if agentCmd != "" {
			fmt.Printf("Agent: Ask, Verify and Extend run in %q over the Agent Client Protocol (its permission prompts appear in the agent panel).\n", agentCmd)
		} else {
			// Nudge toward live mode. Starting the loop, or naming an agent to
			// host, is the user's call — see the boundary note in AGENTS.md.
			fmt.Println("Live mode: pick your coding agent in the reader's agent panel, run /lathe-work in your coding agent, or restart with --agent <acp-command> (otherwise the buttons hand you a command to paste).")
		}
		openBrowser(url)

		// Bind to loopback only: the server is unauthenticated and exposes a
		// destructive delete endpoint, so it must never be reachable from other
		// devices on a shared network.
		httpSrv := &http.Server{
			Addr:    fmt.Sprintf("127.0.0.1:%d", servePort),
			Handler: srv.Handler(),
		}

		// Shut down gracefully on Ctrl-C / SIGTERM so the deferred runtime-file
		// cleanup runs (ListenAndServe alone never returns on a signal).
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		errCh := make(chan error, 1)
		go func() {
			err := httpSrv.ListenAndServe()
			if errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
			errCh <- err
		}()

		select {
		case err := <-errCh:
			return err
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return httpSrv.Shutdown(shutdownCtx)
		}
	},
}

func openBrowser(url string) {
	var bin string
	switch runtime.GOOS {
	case "darwin":
		bin = "open"
	case "linux":
		bin = "xdg-open"
	default:
		return
	}
	if err := exec.Command(bin, url).Start(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "warning: could not open browser: %v\n", err)
	}
}

func init() {
	serveCmd.Flags().IntVar(&servePort, "port", 4242, "port to listen on")
	serveCmd.Flags().StringVar(&serveAgent, "agent", "", "command that starts your coding agent's ACP server (e.g. \"claude-agent-acp\"); Ask then streams from it in the browser")
	rootCmd.AddCommand(serveCmd)
}
