package pipeline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gmcrypto "go.mau.fi/mautrix-gmessages/pkg/libgm/crypto"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

// decryptingFixtureDownloader stands in for the libgm network edge using the
// real AES-GCM media cipher: it returns the same decrypted stream that
// libgm.Client.DownloadMedia produces, so the ingest path is exercised end to end.
type decryptingFixtureDownloader struct {
	ciphertext map[string][]byte
}

func (d *decryptingFixtureDownloader) DownloadMedia(mediaID string, key []byte) (io.ReadCloser, error) {
	ct, ok := d.ciphertext[mediaID]
	if !ok {
		return nil, fmt.Errorf("no media fixture %q", mediaID)
	}
	helper, err := gmcrypto.NewAESGCMHelper(key)
	if err != nil {
		return nil, fmt.Errorf("media cipher: %w", err)
	}
	return helper.DecryptStream(io.NopCloser(bytes.NewReader(ct))), nil
}

// [Requirement(SEC-01)]
// TestMediaIngestor_PersistsDecryptedMedia proves the ingestion caller now
// decrypts RCS media, stores it content-addressed, and records an unprocessed
// media_attachments row for the Vision service (SEC-01).
func TestMediaIngestor_PersistsDecryptedMedia(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://jules:@127.0.0.1:5432/gmessages?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	database, err := db.Connect(ctx, dbURL)
	if err != nil {
		t.Skipf("skipping test: database connection failed: %v", err)
		return
	}
	defer database.Close()

	require.NoError(t, database.Migrate(ctx))

	convID := fmt.Sprintf("conv-media-ingest-%d", time.Now().UnixNano())
	require.NoError(t, database.UpsertConversation(ctx, &db.Conversation{
		ID:   convID,
		Name: "Media Ingestion Test",
	}))

	msgID := fmt.Sprintf("msg-media-ingest-%d", time.Now().UnixNano())
	require.NoError(t, database.UpsertMessage(ctx, &db.Message{
		ID:             msgID,
		ConversationID: convID,
		SenderID:       "sender-1",
		SenderName:     "Alice",
		SentAt:         time.Now().UTC().Truncate(time.Microsecond),
		Body:           "here is the receipt",
	}))

	// Encrypt a real fixture with libgm's AES-GCM media cipher, exactly as an
	// attached RCS image arrives from the network.
	key := gmcrypto.GenerateKey(32)
	plaintext := []byte("synthetic receipt image bytes for SEC-01 ingestion")
	helper, err := gmcrypto.NewAESGCMHelper(key)
	require.NoError(t, err)
	ciphertext, err := helper.EncryptData(plaintext)
	require.NoError(t, err)

	downloadID := "media-fixture-" + convID
	downloader := &decryptingFixtureDownloader{ciphertext: map[string][]byte{downloadID: ciphertext}}

	mediaDir := t.TempDir()
	ingestor := &MediaIngestor{
		Downloader: downloader,
		Database:   database,
		MediaDir:   mediaDir,
		Logger:     zerolog.Nop(),
	}

	msg := &gmproto.Message{
		MessageID:      msgID,
		ConversationID: convID,
		MessageInfo: []*gmproto.MessageInfo{
			{
				Data: &gmproto.MessageInfo_MediaContent{
					MediaContent: &gmproto.MediaContent{
						MediaID:       downloadID,
						DecryptionKey: key,
						MimeType:      "image/png",
						MediaName:     "receipt.png",
					},
				},
			},
		},
	}

	persisted, err := ingestor.IngestMessage(ctx, msg)
	require.NoError(t, err)
	assert.Equal(t, 1, persisted, "one decrypted attachment must be persisted")

	// Observable outcome 1: an unprocessed media_attachments row exists for the
	// conversation, which is exactly what VisionService.ProcessUnprocessedAttachments scans.
	unprocessed, err := database.GetUnprocessedMediaAttachments(ctx, convID, 10)
	require.NoError(t, err)
	require.Len(t, unprocessed, 1)
	att := unprocessed[0]
	assert.Equal(t, msgID, att.MessageID)
	assert.Equal(t, "image/png", att.MimeType)
	assert.False(t, att.VisionProcessed)
	assert.Equal(t, int64(len(plaintext)), att.FileSizeBytes)

	// Observable outcome 2: the content-addressed file on disk holds the
	// decrypted plaintext, so the Vision service can read real image bytes.
	saved, err := os.ReadFile(filepath.Join(mediaDir, att.RelativePath))
	require.NoError(t, err)
	assert.Equal(t, plaintext, saved)

	expected := sha256.Sum256(plaintext)
	assert.Equal(t, hex.EncodeToString(expected[:]), att.SHA256Hash)
}
