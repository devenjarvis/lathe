package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var openCmd = &cobra.Command{
	Use:   "open <slug>",
	Short: "Open a tutorial in the browser (requires lathe serve to be running)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug := args[0]
		url := fmt.Sprintf("http://localhost:%d/%s/", servePort, slug)
		fmt.Printf("Opening %s\n", url)
		openBrowser(url)
		return nil
	},
}

func init() {
	openCmd.Flags().IntVar(&servePort, "port", 4242, "port where lathe serve is running")
	rootCmd.AddCommand(openCmd)
}
