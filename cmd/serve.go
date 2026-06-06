package cmd

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/serve"
	"github.com/spf13/cobra"
)

var servePort int

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the tutorial web server and open the browser",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := config.TutorialsDir()
		if err != nil {
			return err
		}
		srv := serve.NewServer(dir)
		url := fmt.Sprintf("http://localhost:%d", servePort)
		fmt.Printf("Serving tutorials at %s\n", url)
		openBrowser(url)
		// Bind to loopback only: the server is unauthenticated and exposes a
		// destructive delete endpoint, so it must never be reachable from other
		// devices on a shared network.
		return http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", servePort), srv.Handler())
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
	rootCmd.AddCommand(serveCmd)
}
