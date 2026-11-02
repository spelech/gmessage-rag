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
	"github.com/spelech/gmessage-rag/internal/config"
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
	// Verify tabs rendering (UI-05)
	assert.Contains(t, rr.Body.String(), "Overview")
	assert.Contains(t, rr.Body.String(), "Diagnostic Test Bench")
	assert.Contains(t, rr.Body.String(), "Runtime Tuner")
	assert.Contains(t, rr.Body.String(), "MCP Tools Sandbox")
	assert.Contains(t, rr.Body.String(), "Hybrid RRF Search Query Sandbox")
	assert.Contains(t, rr.Body.String(), "Guardrail XML Boundary Sandbox")
	assert.Contains(t, rr.Body.String(), "list_conversations")
	assert.Contains(t, rr.Body.String(), "get_contact_dossier")
	assert.Contains(t, rr.Body.String(), "search_conversation_history")
	assert.Contains(t, rr.Body.String(), "get_messages_around_time")
	assert.Contains(t, rr.Body.String(), "search_shared_media")
	assert.Contains(t, rr.Body.String(), "manage_tracked_contact")

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

// [Requirement(UI-05)]
func TestAPI_TunerEndpoints(t *testing.T) {
	initialSettings := config.TunerSettings{
		DebouncerIdleMinutes: 25,
		RRFKFactor:           50,
		OCREndpoint:          "https://example.com/ocr",
		BackfillBatchSize:    150,
	}
	tuner := config.NewRuntimeTuner(initialSettings)
	handler := NewDashboardHandlerWithTuner(nil, nil, tuner)

	// 1. GET /api/v1/tuner
	reqGet := httptest.NewRequest(http.MethodGet, "/api/v1/tuner", nil)
	rrGet := httptest.NewRecorder()
	handler.ServeHTTP(rrGet, reqGet)

	assert.Equal(t, http.StatusOK, rrGet.Code)
	var gotSettings config.TunerSettings
	err := json.Unmarshal(rrGet.Body.Bytes(), &gotSettings)
	require.NoError(t, err)
	assert.Equal(t, 25, gotSettings.DebouncerIdleMinutes)
	assert.Equal(t, 50, gotSettings.RRFKFactor)
	assert.Equal(t, "https://example.com/ocr", gotSettings.OCREndpoint)
	assert.Equal(t, 150, gotSettings.BackfillBatchSize)

	// 2. POST /api/v1/tuner nominal update
	updatePayload := `{"debouncer_idle_minutes": 45, "rrf_k_factor": 80, "ocr_endpoint": "https://api.openai.com/v1", "backfill_batch_size": 200}`
	reqPost := httptest.NewRequest(http.MethodPost, "/api/v1/tuner", bytes.NewBufferString(updatePayload))
	rrPost := httptest.NewRecorder()
	handler.ServeHTTP(rrPost, reqPost)

	assert.Equal(t, http.StatusOK, rrPost.Code)
	var postResp map[string]any
	err = json.Unmarshal(rrPost.Body.Bytes(), &postResp)
	require.NoError(t, err)
	assert.True(t, postResp["success"].(bool))

	cur := tuner.Get()
	assert.Equal(t, 45, cur.DebouncerIdleMinutes)
	assert.Equal(t, 80, cur.RRFKFactor)
	assert.Equal(t, "https://api.openai.com/v1", cur.OCREndpoint)
	assert.Equal(t, 200, cur.BackfillBatchSize)

	// 3. POST /api/v1/tuner validation error (invalid DebouncerIdleMinutes <= 0)
	badPayload := `{"debouncer_idle_minutes": 0, "rrf_k_factor": 80, "ocr_endpoint": "https://api.openai.com/v1", "backfill_batch_size": 200}`
	reqBad := httptest.NewRequest(http.MethodPost, "/api/v1/tuner", bytes.NewBufferString(badPayload))
	rrBad := httptest.NewRecorder()
	handler.ServeHTTP(rrBad, reqBad)

	assert.Equal(t, http.StatusBadRequest, rrBad.Code)
	assert.Contains(t, rrBad.Body.String(), "debouncer idle minutes must be greater than 0")

	// 4. POST /api/v1/tuner invalid JSON
	reqInvalidJSON := httptest.NewRequest(http.MethodPost, "/api/v1/tuner", bytes.NewBufferString(`{not-json`))
	rrInvalidJSON := httptest.NewRecorder()
	handler.ServeHTTP(rrInvalidJSON, reqInvalidJSON)
	assert.Equal(t, http.StatusBadRequest, rrInvalidJSON.Code)

	// 5. Method not allowed (DELETE)
	reqDelete := httptest.NewRequest(http.MethodDelete, "/api/v1/tuner", nil)
	rrDelete := httptest.NewRecorder()
	handler.ServeHTTP(rrDelete, reqDelete)
	assert.Equal(t, http.StatusMethodNotAllowed, rrDelete.Code)

	// 6. Nil tuner returns 503 Service Unavailable
	handlerNilTuner := NewDashboardHandlerWithTuner(nil, nil, nil)
	reqNil := httptest.NewRequest(http.MethodGet, "/api/v1/tuner", nil)
	rrNil := httptest.NewRecorder()
	handlerNilTuner.ServeHTTP(rrNil, reqNil)
	assert.Equal(t, http.StatusServiceUnavailable, rrNil.Code)
}
