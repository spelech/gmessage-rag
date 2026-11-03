package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "gmessages-rag",
	Short: "Real-Time Google Messages Ingestion, Vision OCR, Living Dossier & Hybrid RAG MCP Server",
	Run: func(cmd *cobra.Command, args []string) {
		_ = cmd.Help()
	},
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the gmessages-rag ingestion daemon, web server, and MCP service",
	RunE:  runServe,
}

var pairCmd = &cobra.Command{
	Use:   "pair",
	Short: "Pair Google Messages by importing Google session cookies (Matrix-style)",
	Long: `Pair Google Messages by importing Google session cookies copied from a signed-in browser.

Sign in to https://messages.google.com/web in your browser, open DevTools, go to the
Network tab, select a request to messages.google.com, right-click it, and choose
"Copy" -> "Copy as cURL". Paste that cURL command when prompted. The daemon validates
the cookies, saves the session, and then starts the Gaia emoji challenge so you can
tap the matching emoji in Google Messages on your phone.`,
	RunE: runPair,
}

func init() {
	pairCmd.Flags().StringVar(&cookiesFlag, "cookies", "", "Read cookies from this file (JSON, cURL, or Cookie header); use '-' for stdin. If omitted, cookies are read from stdin.")
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(pairCmd)
}

// Execute executes the root CLI command.
func Execute() error {
	return rootCmd.Execute()
}

func main() {
	if err := Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
