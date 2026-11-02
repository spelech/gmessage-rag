package gm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"

	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

type mockDownloader struct {
	payloads map[string][]byte
}

func (m *mockDownloader) DownloadMedia(mediaID string, key []byte) (io.ReadCloser, error) {
	data, ok := m.payloads[mediaID]
	if !ok {
		return io.NopCloser(bytes.NewReader([]byte("sample-decrypted-media-bytes"))), nil
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

type mockFullSizeDownloader struct {
	mockDownloader
	fullSizeCalls    []fullSizeCall
	fullSizeError    error
	fullSizeResponse *gmproto.GetFullSizeImageResponse
}

type fullSizeCall struct {
	messageID       string
	actionMessageID string
}

func (m *mockFullSizeDownloader) GetFullSizeImage(ctx context.Context, messageID, actionMessageID string) (*gmproto.GetFullSizeImageResponse, error) {
	m.fullSizeCalls = append(m.fullSizeCalls, fullSizeCall{
		messageID:       messageID,
		actionMessageID: actionMessageID,
	})
	if m.fullSizeError != nil {
		return nil, m.fullSizeError
	}
	if m.fullSizeResponse != nil {
		return m.fullSizeResponse, nil
	}
	return &gmproto.GetFullSizeImageResponse{}, nil
}

func TestContentAddressedPath_Layout(t *testing.T) {
	baseDir := "/tmp/media-test"
	sampleBytes := []byte("hello-gmessage-world")
	hashBytes := sha256.Sum256(sampleBytes)
	hashHex := hex.EncodeToString(hashBytes[:])

	path := ContentAddressedPath(baseDir, hashHex, ".jpg")
	relPath := RelativeMediaPath(hashHex, ".jpg")

	expectedRel := filepath.Join(hashHex[0:2], hashHex[2:4], hashHex+".jpg")
	if relPath != expectedRel {
		t.Fatalf("expected relPath %s, got %s", expectedRel, relPath)
	}

	expectedFull := filepath.Join(baseDir, expectedRel)
	if path != expectedFull {
		t.Fatalf("expected full path %s, got %s", expectedFull, path)
	}
}

func TestDetermineExtension(t *testing.T) {
	tests := []struct {
		mimeType string
		fileName string
		expected string
	}{
		{"image/jpeg", "", ".jpg"},
		{"image/png", "", ".png"},
		{"image/webp", "", ".webp"},
		{"video/mp4", "", ".mp4"},
		{"audio/aac", "", ".m4a"},
		{"text/vcard", "", ".vcf"},
		{"application/octet-stream", "document.pdf", ".pdf"},
		{"unknown/binary", "", ".bin"},
	}

	for _, tt := range tests {
		got := DetermineExtension(tt.mimeType, tt.fileName)
		if got != tt.expected {
			t.Errorf("DetermineExtension(%q, %q) = %q, expected %q", tt.mimeType, tt.fileName, got, tt.expected)
		}
	}
}

func TestExtractAndSaveMedia_IdempotencyAndPermissions(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gm-media-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mockData := []byte("secret-rcs-photo-payload-bytes")
	downloader := &mockDownloader{
		payloads: map[string][]byte{
			"media-id-123": mockData,
		},
	}

	width := int64(1920)
	height := int64(1080)

	msg := &gmproto.Message{
		MessageID:      "msg-with-attachment-001",
		ConversationID: "conv-101",
		MessageInfo: []*gmproto.MessageInfo{
			{
				Data: &gmproto.MessageInfo_MediaContent{
					MediaContent: &gmproto.MediaContent{
						MediaID:       "media-id-123",
						DecryptionKey: []byte("sample-aes-key-16b"),
						MimeType:      "image/jpeg",
						MediaName:     "photo.jpg",
						Dimensions: &gmproto.Dimensions{
							Width:  width,
							Height: height,
						},
					},
				},
			},
		},
	}

	ctx := context.Background()

	// 1. First extraction & write
	attachments, err := ExtractAndSaveMedia(ctx, downloader, msg, tempDir)
	if err != nil {
		t.Fatalf("first extraction failed: %v", err)
	}
	if len(attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(attachments))
	}

	att := attachments[0]
	if att.MessageID != "msg-with-attachment-001" || att.ConversationID != "conv-101" {
		t.Errorf("unexpected attachment IDs: %+v", att)
	}
	if att.MimeType != "image/jpeg" {
		t.Errorf("expected image/jpeg, got %s", att.MimeType)
	}

	expectedHash := sha256.Sum256(mockData)
	expectedHex := hex.EncodeToString(expectedHash[:])
	if att.SHA256Hash != expectedHex {
		t.Fatalf("expected hash %s, got %s", expectedHex, att.SHA256Hash)
	}

	filePath := filepath.Join(tempDir, att.RelativePath)
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("target media file does not exist: %v", err)
	}
	if fileInfo.Size() != int64(len(mockData)) {
		t.Errorf("expected size %d, got %d", len(mockData), fileInfo.Size())
	}

	// 2. Second extraction (assert idempotency, does not fail or corrupt)
	attachments2, err := ExtractAndSaveMedia(ctx, downloader, msg, tempDir)
	if err != nil {
		t.Fatalf("second extraction failed: %v", err)
	}
	if len(attachments2) != 1 {
		t.Fatalf("expected 1 attachment on duplicate call, got %d", len(attachments2))
	}
	if attachments2[0].SHA256Hash != expectedHex {
		t.Fatalf("expected duplicate hash %s, got %s", expectedHex, attachments2[0].SHA256Hash)
	}
}

// Requirement ID: SEC-02
func TestMediaDownload_FullSizeOption(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gm-fullsize-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	fullImageBytes := []byte("high-resolution-raw-camera-pixels-12345")
	actionMsgID := "action-msg-part-456"

	downloader := &mockFullSizeDownloader{
		mockDownloader: mockDownloader{
			payloads: map[string][]byte{
				"media-id-full-789": fullImageBytes,
			},
		},
	}

	msg := &gmproto.Message{
		MessageID:      "msg-full-001",
		ConversationID: "conv-202",
		MessageInfo: []*gmproto.MessageInfo{
			{
				ActionMessageID: &actionMsgID,
				Data: &gmproto.MessageInfo_MediaContent{
					MediaContent: &gmproto.MediaContent{
						MediaID:       "media-id-full-789",
						DecryptionKey: []byte("sample-aes-key-16b"),
						MimeType:      "image/png",
						MediaName:     "photo_hd.png",
					},
				},
			},
		},
	}

	ctx := context.Background()

	// 1. Nominal path: PullFullSizeMedia=true invokes GetFullSizeImage and downloads media
	opts := MediaOptions{
		BaseDir:           tempDir,
		PullFullSizeMedia: true,
	}
	attachments, err := ExtractAndSaveMediaWithOptions(ctx, downloader, msg, opts)
	if err != nil {
		t.Fatalf("ExtractAndSaveMediaWithOptions failed: %v", err)
	}
	if len(attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(attachments))
	}
	if len(downloader.fullSizeCalls) != 1 {
		t.Fatalf("expected 1 call to GetFullSizeImage, got %d", len(downloader.fullSizeCalls))
	}
	if downloader.fullSizeCalls[0].messageID != "msg-full-001" || downloader.fullSizeCalls[0].actionMessageID != actionMsgID {
		t.Errorf("unexpected GetFullSizeImage call arguments: %+v", downloader.fullSizeCalls[0])
	}

	savedFile := filepath.Join(tempDir, attachments[0].RelativePath)
	info, err := os.Stat(savedFile)
	if err != nil {
		t.Fatalf("saved file does not exist: %v", err)
	}
	if info.Size() != int64(len(fullImageBytes)) {
		t.Errorf("expected size %d, got %d", len(fullImageBytes), info.Size())
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected file mode 0600, got %o", info.Mode().Perm())
	}
	parentDir := filepath.Dir(savedFile)
	dirInfo, err := os.Stat(parentDir)
	if err != nil {
		t.Fatalf("parent dir does not exist: %v", err)
	}
	if dirInfo.Mode().Perm() != 0700 {
		t.Errorf("expected dir mode 0700, got %o", dirInfo.Mode().Perm())
	}

	// 2. Disabled path: PullFullSizeMedia=false does NOT call GetFullSizeImage
	downloader.fullSizeCalls = nil
	optsDisabled := MediaOptions{
		BaseDir:           tempDir,
		PullFullSizeMedia: false,
	}
	attachmentsDisabled, err := ExtractAndSaveMediaWithOptions(ctx, downloader, msg, optsDisabled)
	if err != nil {
		t.Fatalf("ExtractAndSaveMediaWithOptions failed with false: %v", err)
	}
	if len(attachmentsDisabled) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(attachmentsDisabled))
	}
	if len(downloader.fullSizeCalls) != 0 {
		t.Fatalf("expected 0 calls to GetFullSizeImage when PullFullSizeMedia=false, got %d", len(downloader.fullSizeCalls))
	}

	// 3. Fallback path: GetFullSizeImage fails, but extraction seamlessly falls back to standard DownloadMedia
	downloader.fullSizeCalls = nil
	downloader.fullSizeError = os.ErrNotExist
	attachmentsFallback, err := ExtractAndSaveMediaWithOptions(ctx, downloader, msg, opts)
	if err != nil {
		t.Fatalf("expected seamless fallback to DownloadMedia, but failed: %v", err)
	}
	if len(attachmentsFallback) != 1 {
		t.Fatalf("expected 1 attachment on fallback, got %d", len(attachmentsFallback))
	}
	if len(downloader.fullSizeCalls) != 1 {
		t.Fatalf("expected 1 call attempt to GetFullSizeImage, got %d", len(downloader.fullSizeCalls))
	}

	// 4. Non-applicable path: PullFullSizeMedia=true but downloader is standard MediaDownloader (not FullSizeImageDownloader)
	stdDownloader := &mockDownloader{
		payloads: map[string][]byte{
			"media-id-full-789": fullImageBytes,
		},
	}
	attachmentsNonApplicable, err := ExtractAndSaveMediaWithOptions(ctx, stdDownloader, msg, opts)
	if err != nil {
		t.Fatalf("expected successful download when downloader does not implement full-size: %v", err)
	}
	if len(attachmentsNonApplicable) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(attachmentsNonApplicable))
	}

	// 5. Non-applicable path: message lacks ActionMessageID
	msgNoAction := &gmproto.Message{
		MessageID:      "msg-full-002",
		ConversationID: "conv-202",
		MessageInfo: []*gmproto.MessageInfo{
			{
				Data: &gmproto.MessageInfo_MediaContent{
					MediaContent: &gmproto.MediaContent{
						MediaID:       "media-id-full-789",
						DecryptionKey: []byte("sample-aes-key-16b"),
						MimeType:      "image/png",
					},
				},
			},
		},
	}
	downloader.fullSizeCalls = nil
	downloader.fullSizeError = nil
	attachmentsNoAction, err := ExtractAndSaveMediaWithOptions(ctx, downloader, msgNoAction, opts)
	if err != nil {
		t.Fatalf("expected successful download without action message ID: %v", err)
	}
	if len(attachmentsNoAction) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(attachmentsNoAction))
	}
	if len(downloader.fullSizeCalls) != 0 {
		t.Errorf("expected 0 calls to GetFullSizeImage when ActionMessageID is missing, got %d", len(downloader.fullSizeCalls))
	}
}

// Requirement ID: SEC-01
func TestSaveAttachment_DirectValidation(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gm-saveatt-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	data := []byte("unique-content-to-be-saved-and-verified-content-addressed")
	relPath, hashHex, written, err := SaveAttachment(bytes.NewReader(data), tempDir, ".webp")
	if err != nil {
		t.Fatalf("SaveAttachment failed: %v", err)
	}

	expectedHash := sha256.Sum256(data)
	expectedHex := hex.EncodeToString(expectedHash[:])
	if hashHex != expectedHex {
		t.Errorf("expected hash %s, got %s", expectedHex, hashHex)
	}
	if written != int64(len(data)) {
		t.Errorf("expected written %d, got %d", len(data), written)
	}

	fullPath := filepath.Join(tempDir, relPath)
	info, err := os.Stat(fullPath)
	if err != nil {
		t.Fatalf("file not found: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected 0600 file permissions, got %o", info.Mode().Perm())
	}
	parentInfo, err := os.Stat(filepath.Dir(fullPath))
	if err != nil {
		t.Fatalf("parent dir not found: %v", err)
	}
	if parentInfo.Mode().Perm() != 0700 {
		t.Errorf("expected 0700 dir permissions, got %o", parentInfo.Mode().Perm())
	}
}
