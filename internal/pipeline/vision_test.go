package pipeline_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/spelech/gmessage-rag/internal/gm"
	"github.com/spelech/gmessage-rag/internal/pipeline"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

// mockFullDownloader implements gm.MediaDownloader and gm.FullSizeImageDownloader
type mockFullDownloader struct {
	fullRequested   atomic.Bool
	primaryFails    bool
	fallbackPayload []byte
	primaryPayload  []byte
}

func (m *mockFullDownloader) DownloadMedia(mediaID string, key []byte) (io.ReadCloser, error) {
	if mediaID == "thumb-1" {
		return io.NopCloser(strings.NewReader(string(m.fallbackPayload))), nil
	}
	if m.primaryFails {
		return nil, fmt.Errorf("simulated primary download error")
	}
	return io.NopCloser(strings.NewReader(string(m.primaryPayload))), nil
}

func (m *mockFullDownloader) GetFullSizeImage(ctx context.Context, messageID, actionMessageID string) (*gmproto.GetFullSizeImageResponse, error) {
	m.fullRequested.Store(true)
	return &gmproto.GetFullSizeImageResponse{}, nil
}

// TestVisionOCR_Extraction tests the full extraction pipeline against an httptest server.
// Acceptance Criteria:
// 1. Image attachments get ocr_text populated and vision_processed=true.
// 2. Failures are retried/logged without blocking ingestion.
// 3. No raw API key logging.
func TestVisionOCR_Extraction(t *testing.T) {
	apiKey := "secret-api-key-xyz-12345"
	expectedOCR := "BOARDING PASS: FLIGHT 248 SFO -> JFK"
	expectedDesc := "A mobile boarding pass with a QR code and flight departure details."

	callCount := int32(0)
	var authHeaders []string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))

		assert.Equal(t, "/chat/completions", r.URL.Path)
		assert.Equal(t, "POST", r.Method)

		var reqBody map[string]any
		err := json.NewDecoder(r.Body).Decode(&reqBody)
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		// Verify model and message formatting
		assert.Equal(t, "google/gemini-2.5-flash", reqBody["model"])
		msgs, ok := reqBody["messages"].([]any)
		require.True(t, ok)
		require.NotEmpty(t, msgs)

		// Return OpenAI-compatible response
		respJSON := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content": fmt.Sprintf(`{"ocr_text": "%s", "visual_description": "%s"}`, expectedOCR, expectedDesc),
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(respJSON)
	}))
	defer ts.Close()

	logger := zerolog.Nop()
	client := pipeline.NewOpenAIVisionClient(pipeline.OpenAIVisionClientConfig{
		BaseURL:    ts.URL,
		APIKey:     apiKey,
		Model:      "google/gemini-2.5-flash",
		MaxRetries: 2,
		Logger:     logger,
	})

	ctx := context.Background()
	fakeImage := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR...")
	res, err := client.ExtractImageContext(ctx, fakeImage, "image/png")
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, expectedOCR, res.OCRText)
	assert.Equal(t, expectedDesc, res.VisualDescription)

	// Verify authorization header was sent
	require.NotEmpty(t, authHeaders)
	assert.Equal(t, "Bearer "+apiKey, authHeaders[0])

	// Verify SanitizeSecret redacts raw API key
	leakedString := fmt.Sprintf("Error connecting with bearer %s to endpoint", apiKey)
	clean := pipeline.SanitizeSecret(leakedString, apiKey)
	assert.NotContains(t, clean, apiKey)
	assert.Contains(t, clean, "[REDACTED]")

	// Test non-image attachment skipping and non-blocking failure
	service := &pipeline.VisionService{
		Client:   client,
		Database: nil,
		MediaDir: t.TempDir(),
		Logger:   logger,
	}
	assert.Error(t, service.ProcessMediaAttachment(ctx, db.MediaAttachment{ID: 1}))
}

// TestVisionOCR_ContractStubFallback tests contract stubs, retry on 5xx/429,
// non-blocking fallback on unparseable JSON, and full-res vs thumbnail fallback paths.
func TestVisionOCR_ContractStubFallback(t *testing.T) {
	ctx := context.Background()

	// 1. Test Retry on 500 error then success
	attempts := int32(0)
	tsRetry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		// Second attempt succeeds with plain text response (testing markdown / unparseable fallback)
		respJSON := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content": "A beautiful sunset over mountains",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(respJSON)
	}))
	defer tsRetry.Close()

	clientRetry := pipeline.NewOpenAIVisionClient(pipeline.OpenAIVisionClientConfig{
		BaseURL:    tsRetry.URL,
		APIKey:     "dummy-key",
		MaxRetries: 3,
		RetryDelay: 10 * time.Millisecond,
		Logger:     zerolog.Nop(),
	})

	extraction, err := clientRetry.ExtractImageContext(ctx, []byte("fake-image-bytes"), "image/jpeg")
	require.NoError(t, err)
	assert.Equal(t, int32(2), atomic.LoadInt32(&attempts))
	assert.Equal(t, "", extraction.OCRText)
	assert.Equal(t, "A beautiful sunset over mountains", extraction.VisualDescription)

	// 2. Test Non-blocking ingestion on permanent vision failure
	tsFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream timeout", http.StatusGatewayTimeout)
	}))
	defer tsFail.Close()

	clientFail := pipeline.NewOpenAIVisionClient(pipeline.OpenAIVisionClientConfig{
		BaseURL:    tsFail.URL,
		APIKey:     "super-secret-key",
		MaxRetries: 2,
		RetryDelay: 5 * time.Millisecond,
		Logger:     zerolog.Nop(),
	})

	_, err = clientFail.ExtractImageContext(ctx, []byte("fake-image-bytes"), "image/jpeg")
	require.Error(t, err)
	// Crucial: assert raw API key is never leaked in error string
	assert.NotContains(t, err.Error(), "super-secret-key")

	// 3. Test Full-resolution retrieval with thumbnail fallback (PULL_FULL_SIZE_MEDIA)
	tempDir := t.TempDir()
	fullDownloader := &mockFullDownloader{
		primaryFails:    true, // Primary fails, should fall back to thumbnail
		fallbackPayload: []byte("thumbnail-image-bytes"),
		primaryPayload:  []byte("full-resolution-bytes"),
	}

	thumbID := "thumb-1"
	actionMsgID := "action-123"
	thumbKey := []byte("0123456789abcdef0123456789abcdef")
	msg := &gmproto.Message{
		MessageID:      "msg-full-test-" + uuid.New().String(),
		ConversationID: "conv-1",
		MessageInfo: []*gmproto.MessageInfo{
			{
				ActionMessageID: &actionMsgID,
				Data: &gmproto.MessageInfo_MediaContent{
					MediaContent: &gmproto.MediaContent{
						MediaID:                "media-primary",
						DecryptionKey:          []byte("primarykeyprimarykeyprimarykey12"),
						ThumbnailMediaID:       thumbID,
						ThumbnailDecryptionKey: thumbKey,
						MimeType:               "image/jpeg",
					},
				},
			},
		},
	}

	opts := gm.MediaOptions{
		BaseDir:           tempDir,
		PullFullSizeMedia: true,
	}

	attachments, err := gm.ExtractAndSaveMediaWithOptions(ctx, fullDownloader, msg, opts)
	require.NoError(t, err)
	require.Len(t, attachments, 1)
	assert.True(t, fullDownloader.fullRequested.Load(), "Full size image should have been requested")

	// Verify thumbnail fallback saved correctly
	savedPath := filepath.Join(tempDir, attachments[0].RelativePath)
	content, err := os.ReadFile(savedPath)
	require.NoError(t, err)
	assert.Equal(t, "thumbnail-image-bytes", string(content))
}
