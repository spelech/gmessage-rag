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
