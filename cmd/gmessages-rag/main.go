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
	Short: "Initiate Google Messages pairing via Gaia emoji challenge (default) or legacy QR",
	RunE:  runPair,
}

func init() {
	pairCmd.Flags().BoolVar(&qrFlag, "qr", false, "Use legacy QR code terminal pairing instead of Google Account emoji challenge (AUTH-05)")

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
