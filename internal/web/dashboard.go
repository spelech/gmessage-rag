package web

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"

	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/spelech/gmessage-rag/internal/gm"
)

//go:embed assets/*
var dashboardAssets embed.FS

// NewDashboardHandler constructs an http.Handler that routes:
// - GET / and GET /dashboard to the embedded dashboard UI
// - GET /api/v1/status
// - GET /api/v1/contacts
// - POST /api/v1/contacts/track
// - GET /pair (QR pairing visualization via gm.NewPairingHandler)
func NewDashboardHandler(database *db.DB, gmClient *gm.Client) http.Handler {
	mux := http.NewServeMux()
	api := NewAPIHandler(database, gmClient)

	// API endpoints
	mux.HandleFunc("/api/v1/status", api.HandleStatus)
	mux.HandleFunc("/api/v1/contacts", api.HandleContacts)
	mux.HandleFunc("/api/v1/contacts/track", api.HandleTrackContact)

	// Pairing QR visualization endpoint
	if gmClient != nil {
		pairingHandler := gm.NewPairingHandler(gmClient)
		mux.Handle("/pair", pairingHandler)
	}

	// Dashboard UI handler
	dashboardHTML, err := dashboardAssets.ReadFile("assets/index.html")
	uiHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/dashboard" {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to load dashboard UI: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(dashboardHTML)
	})

	mux.HandleFunc("/dashboard", uiHandler)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			uiHandler.ServeHTTP(w, r)
			return
		}
		// If not root, check static assets or 404
		sub, subErr := fs.Sub(dashboardAssets, "assets")
		if subErr == nil {
			fileServer := http.FileServer(http.FS(sub))
			fileServer.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})

	return mux
}
