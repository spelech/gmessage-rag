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
	Short: "Initiate Google Messages QR pairing via terminal or Gaia emoji challenge",
	RunE:  runPair,
}

func init() {
	pairCmd.Flags().BoolVar(&gaiaFlag, "gaia", false, "Use Google Account (Gaia) emoji challenge pairing without QR camera scan (AUTH-04)")

	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(pairCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
