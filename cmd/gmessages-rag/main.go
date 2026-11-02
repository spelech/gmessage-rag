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
		fmt.Println("gmessages-rag daemon. Use 'serve' or 'pair'.")
	},
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the gmessages-rag ingestion daemon, web server, and MCP service",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Starting gmessages-rag service...")
	},
}

var pairCmd = &cobra.Command{
	Use:   "pair",
	Short: "Initiate Google Messages QR pairing via terminal",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Starting pairing session...")
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(pairCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
