package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spelech/gmessage-rag/internal/gm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCookiePairHandler verifies AUTH-02: the web pairing endpoint serves the
// cookie-import form, reports JSON status, rejects invalid cookies, and fails
// closed when no Google Messages client is configured. Device pairing uses cookies.
func TestCookiePairHandler(t *testing.T) {
	t.Run("GET serves the cookie import form", func(t *testing.T) {
		handler := NewCookiePairHandler(nil)
		req := httptest.NewRequest(http.MethodGet, "/pair", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "Pair Google Messages with cookies")
		assert.Contains(t, rec.Body.String(), "cookies")
		assert.NotContains(t, rec.Body.String(), "Device pairing uses QR")
	})

	t.Run("GET format=json reports unpaired", func(t *testing.T) {
		handler := NewCookiePairHandler(nil)
		req := httptest.NewRequest(http.MethodGet, "/pair?format=json", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"paired":false`)
	})

	t.Run("POST rejects invalid cookies", func(t *testing.T) {
		client, err := gm.NewClient(gm.ClientOptions{
			SessionPath: filepath.Join(t.TempDir(), "session.json"),
		})
		require.NoError(t, err)
		handler := NewCookiePairHandler(client)

		req := httptest.NewRequest(http.MethodPost, "/pair", strings.NewReader(`{"cookies":"SID=only"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "missing required Google cookies")
	})

	t.Run("POST fails closed without a configured client", func(t *testing.T) {
		handler := NewCookiePairHandler(nil)
		req := httptest.NewRequest(http.MethodPost, "/pair", strings.NewReader(`{"cookies":"SID=x"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
		assert.Contains(t, rec.Body.String(), "not configured")
	})
}

func TestCookiePairHandler_JSONSubmission(t *testing.T) {
	handler := NewCookiePairHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/pair", strings.NewReader(`{"cookies":"invalid"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestCookiePairHandler_FormSubmission(t *testing.T) {
	handler := NewCookiePairHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/pair", strings.NewReader("cookies=invalid"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}
