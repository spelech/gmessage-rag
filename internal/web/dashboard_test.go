package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/spelech/gmessage-rag/internal/gm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDashboardUI_AssetServing(t *testing.T) {
	handler := NewDashboardHandler(nil, nil)

	// Test GET /
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, rr.Body.String(), "gmessages-rag Management Dashboard")
	assert.Contains(t, rr.Body.String(), "Database Status")
	assert.Contains(t, rr.Body.String(), "Google Messages Link")

	// Test GET /dashboard
	reqDashboard := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rrDashboard := httptest.NewRecorder()
	handler.ServeHTTP(rrDashboard, reqDashboard)

	assert.Equal(t, http.StatusOK, rrDashboard.Code)
	assert.Contains(t, rrDashboard.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, rrDashboard.Body.String(), "Tracked Contacts &amp; Conversations")

	// Test GET /nonexistent
	reqNotFound := httptest.NewRequest(http.MethodGet, "/nonexistent-route", nil)
	rrNotFound := httptest.NewRecorder()
	handler.ServeHTTP(rrNotFound, reqNotFound)

	assert.Equal(t, http.StatusNotFound, rrNotFound.Code)
}

func TestAPI_StatusEndpoint(t *testing.T) {
	tempDir := t.TempDir()
	sessionPath := tempDir + "/session.json"
	gmClient, err := gm.NewClient(gm.ClientOptions{
		SessionPath: sessionPath,
		Logger:      zerolog.Nop(),
	})
	require.NoError(t, err)

	handler := NewDashboardHandler(nil, gmClient)

	// Test GET /api/v1/status
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var statusResp StatusResponse
	err = json.Unmarshal(rr.Body.Bytes(), &statusResp)
	require.NoError(t, err)

	assert.Equal(t, "degraded", statusResp.Status) // Nil DB means degraded
	assert.False(t, statusResp.DBConnected)
	assert.False(t, statusResp.GMConnected)
	assert.Equal(t, int64(0), statusResp.UnindexedMessages)

	// Test Method Not Allowed
	reqPost := httptest.NewRequest(http.MethodPost, "/api/v1/status", nil)
	rrPost := httptest.NewRecorder()
	handler.ServeHTTP(rrPost, reqPost)
	assert.Equal(t, http.StatusMethodNotAllowed, rrPost.Code)
}

func TestAPI_ContactsEndpoint_DBUnavailable(t *testing.T) {
	handler := NewDashboardHandler(nil, nil)

	// Test GET /api/v1/contacts with nil DB
	req := httptest.NewRequest(http.MethodGet, "/api/v1/contacts", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)

	// Test Method Not Allowed
	reqPost := httptest.NewRequest(http.MethodPost, "/api/v1/contacts", nil)
	rrPost := httptest.NewRecorder()
	handler.ServeHTTP(rrPost, reqPost)
	assert.Equal(t, http.StatusMethodNotAllowed, rrPost.Code)
}

func TestAPI_TrackContactEndpoint_Validation(t *testing.T) {
	handler := NewDashboardHandler(nil, nil)

	// Test with nil DB
	body := bytes.NewBufferString(`{"conversation_id": "conv-123", "is_tracked": true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/contacts/track", body)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)

	// Test invalid JSON with dummy db instance
	dummyDB := &db.DB{}
	apiHandler := NewAPIHandler(dummyDB, nil)

	badJSON := bytes.NewBufferString(`{invalid-json`)
	reqBad := httptest.NewRequest(http.MethodPost, "/api/v1/contacts/track", badJSON)
	rrBad := httptest.NewRecorder()
	apiHandler.HandleTrackContact(rrBad, reqBad)
	assert.Equal(t, http.StatusBadRequest, rrBad.Code)
	assert.Contains(t, rrBad.Body.String(), "invalid request body")

	// Test empty conversation_id
	emptyIDJSON := bytes.NewBufferString(`{"conversation_id": ""}`)
	reqEmpty := httptest.NewRequest(http.MethodPost, "/api/v1/contacts/track", emptyIDJSON)
	rrEmpty := httptest.NewRecorder()
	apiHandler.HandleTrackContact(rrEmpty, reqEmpty)
	assert.Equal(t, http.StatusBadRequest, rrEmpty.Code)
	assert.Contains(t, rrEmpty.Body.String(), "conversation_id is required")

	// Test Method Not Allowed
	reqGet := httptest.NewRequest(http.MethodGet, "/api/v1/contacts/track", nil)
	rrGet := httptest.NewRecorder()
	handler.ServeHTTP(rrGet, reqGet)
	assert.Equal(t, http.StatusMethodNotAllowed, rrGet.Code)
}

func TestDashboard_PairingRoute(t *testing.T) {
	tempDir := t.TempDir()
	sessionPath := tempDir + "/session.json"
	gmClient, err := gm.NewClient(gm.ClientOptions{
		SessionPath: sessionPath,
		Logger:      zerolog.Nop(),
	})
	require.NoError(t, err)

	handler := NewDashboardHandler(nil, gmClient)

	// GET /pair?format=json
	req := httptest.NewRequest(http.MethodGet, "/pair?format=json", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.True(t, strings.Contains(rr.Body.String(), `"paired":false`))
}

func TestDashboard_WithMockDB(t *testing.T) {
	// If PostgreSQL is running locally, we can run full DB integration tests
	dbURL := "postgres://jules:postgres@localhost:5432/gmessages?sslmode=disable"
	ctx := context.Background()
	database, err := db.Connect(ctx, dbURL)
	if err != nil {
		t.Skip("PostgreSQL not accessible in this environment, skipping live DB test")
		return
	}
	defer database.Close()

	require.NoError(t, database.Migrate(ctx))

	// Upsert a test conversation
	convID := "test-conv-dashboard"
	err = database.UpsertConversation(ctx, &db.Conversation{
		ID:        convID,
		Name:      "Test User",
		IsGroup:   false,
		IsTracked: false,
	})
	require.NoError(t, err)

	handler := NewDashboardHandler(database, nil)

	// Test GET /api/v1/status
	reqStatus := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	rrStatus := httptest.NewRecorder()
	handler.ServeHTTP(rrStatus, reqStatus)
	assert.Equal(t, http.StatusOK, rrStatus.Code)

	var statusResp StatusResponse
	err = json.Unmarshal(rrStatus.Body.Bytes(), &statusResp)
	require.NoError(t, err)
	assert.True(t, statusResp.DBConnected)
	assert.Equal(t, "healthy", statusResp.Status)

	// Test GET /api/v1/contacts
	reqContacts := httptest.NewRequest(http.MethodGet, "/api/v1/contacts", nil)
	rrContacts := httptest.NewRecorder()
	handler.ServeHTTP(rrContacts, reqContacts)
	assert.Equal(t, http.StatusOK, rrContacts.Code)

	var contacts []db.Conversation
	err = json.Unmarshal(rrContacts.Body.Bytes(), &contacts)
	require.NoError(t, err)
	assert.NotEmpty(t, contacts)

	// Test POST /api/v1/contacts/track
	trackBody := bytes.NewBufferString(`{"conversation_id": "test-conv-dashboard", "is_tracked": true}`)
	reqTrack := httptest.NewRequest(http.MethodPost, "/api/v1/contacts/track", trackBody)
	rrTrack := httptest.NewRecorder()
	handler.ServeHTTP(rrTrack, reqTrack)
	assert.Equal(t, http.StatusOK, rrTrack.Code)

	conv, err := database.GetConversation(ctx, convID)
	require.NoError(t, err)
	assert.True(t, conv.IsTracked)
}
