package gm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/spelech/gmessage-rag/internal/db"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

// DefaultMediaDir is the default directory to persist content-addressed media.
const DefaultMediaDir = "/data/media"

// MediaDownloader abstracts libgm media download for testability.
type MediaDownloader interface {
	DownloadMedia(mediaID string, key []byte) (io.ReadCloser, error)
}

// FullSizeImageDownloader abstracts requesting full-resolution media from libgm.
// Requirement ID: SEC-02
type FullSizeImageDownloader interface {
	GetFullSizeImage(ctx context.Context, messageID, actionMessageID string) (*gmproto.GetFullSizeImageResponse, error)
}

// MediaOptions specifies options for media extraction and persistence.
type MediaOptions struct {
	BaseDir           string
	PullFullSizeMedia bool
}

// ContentAddressedPath computes the two-level subdirectory path for a SHA-256 hash.
// Layout: <baseDir>/<hash[0:2]>/<hash[2:4]>/<hash><ext>
func ContentAddressedPath(baseDir, sha256Hash, ext string) string {
	if len(sha256Hash) < 4 {
		return filepath.Join(baseDir, sha256Hash+ext)
	}
	prefix1 := sha256Hash[0:2]
	prefix2 := sha256Hash[2:4]
	fileName := sha256Hash + ext
	return filepath.Join(baseDir, prefix1, prefix2, fileName)
}

// RelativeMediaPath computes the relative path relative to baseDir.
// Layout: <hash[0:2]>/<hash[2:4]>/<hash><ext>
func RelativeMediaPath(sha256Hash, ext string) string {
	if len(sha256Hash) < 4 {
		return sha256Hash + ext
	}
	prefix1 := sha256Hash[0:2]
	prefix2 := sha256Hash[2:4]
	fileName := sha256Hash + ext
	return filepath.Join(prefix1, prefix2, fileName)
}

// DetermineExtension derives the file extension from MIME type or filename.
func DetermineExtension(mimeType, fileName string) string {
	if fileName != "" {
		ext := filepath.Ext(fileName)
		if ext != "" {
			return strings.ToLower(ext)
		}
	}

	if mimeType != "" {
		// Clean parameters like "image/jpeg; charset=utf-8"
		mediaType, _, err := mime.ParseMediaType(mimeType)
		if err == nil {
			mimeType = mediaType
		}

		switch mimeType {
		case "image/jpeg", "image/jpg":
			return ".jpg"
		case "image/png":
			return ".png"
		case "image/gif":
			return ".gif"
		case "image/webp":
			return ".webp"
		case "image/heic":
			return ".heic"
		case "video/mp4":
			return ".mp4"
		case "video/3gpp":
			return ".3gp"
		case "video/quicktime":
			return ".mov"
		case "audio/aac", "audio/mp4":
			return ".m4a"
		case "audio/amr":
			return ".amr"
		case "audio/mpeg", "audio/mp3":
			return ".mp3"
		case "audio/ogg":
			return ".ogg"
		case "text/vcard", "text/x-vcard":
			return ".vcf"
		case "application/pdf":
			return ".pdf"
		}

		exts, err := mime.ExtensionsByType(mimeType)
		if err == nil && len(exts) > 0 {
			return exts[0]
		}
	}

	return ".bin"
}

// SaveAttachment streams media from reader to a temp file in baseDir, computes SHA-256,
// moves to content-addressed path with 0700 directory and POSIX 0600 file permissions.
func SaveAttachment(reader io.Reader, baseDir, ext string) (string, string, int64, error) {
	if baseDir == "" {
		baseDir = DefaultMediaDir
	}

	if err := os.MkdirAll(baseDir, 0700); err != nil {
		return "", "", 0, fmt.Errorf("create base media dir %s: %w", baseDir, err)
	}

	tmpFile, err := os.CreateTemp(baseDir, "media-download-*.tmp")
	if err != nil {
		return "", "", 0, fmt.Errorf("create temp media file: %w", err)
	}
	tmpPath := tmpFile.Name()

	hasher := sha256.New()
	multiWriter := io.MultiWriter(tmpFile, hasher)

	written, copyErr := io.Copy(multiWriter, reader)
	tmpCloseErr := tmpFile.Close()

	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return "", "", 0, fmt.Errorf("copy media data: %w", copyErr)
	}
	if tmpCloseErr != nil {
		_ = os.Remove(tmpPath)
		return "", "", 0, fmt.Errorf("close temp file: %w", tmpCloseErr)
	}

	hashBytes := hasher.Sum(nil)
	sha256Hex := hex.EncodeToString(hashBytes)

	targetPath := ContentAddressedPath(baseDir, sha256Hex, ext)
	targetRelPath := RelativeMediaPath(sha256Hex, ext)

	targetDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		_ = os.Remove(tmpPath)
		return "", "", 0, fmt.Errorf("create content-addressed dir %s: %w", targetDir, err)
	}

	// Idempotent duplicate avoidance: if target file already exists, discard temp file
	if _, err := os.Stat(targetPath); err == nil {
		_ = os.Remove(tmpPath)
	} else {
		if err := os.Rename(tmpPath, targetPath); err != nil {
			_ = os.Remove(tmpPath)
			return "", "", 0, fmt.Errorf("rename temp media file to target %s: %w", targetPath, err)
		}
		// Enforce POSIX 0600 file permissions
		_ = os.Chmod(targetPath, 0600)
	}

	return targetRelPath, sha256Hex, written, nil
}

// ExtractAndSaveMedia inspects a message's attachments, downloads & decrypts them,
// saves them content-addressed by SHA-256 hash, and returns db.MediaAttachment records.
func ExtractAndSaveMedia(ctx context.Context, downloader MediaDownloader, msg *gmproto.Message, baseDir string) ([]*db.MediaAttachment, error) {
	return ExtractAndSaveMediaWithOptions(ctx, downloader, msg, MediaOptions{
		BaseDir:           baseDir,
		PullFullSizeMedia: false,
	})
}

// ExtractAndSaveMediaWithOptions inspects message attachments and downloads them,
// attempting full-resolution retrieval when PullFullSizeMedia is enabled.
// Requirement ID: SEC-01, SEC-02
func ExtractAndSaveMediaWithOptions(ctx context.Context, downloader MediaDownloader, msg *gmproto.Message, opts MediaOptions) ([]*db.MediaAttachment, error) {
	if msg == nil {
		return nil, errors.New("message cannot be nil")
	}
	if downloader == nil {
		return nil, errors.New("downloader cannot be nil")
	}
	baseDir := opts.BaseDir
	if baseDir == "" {
		baseDir = DefaultMediaDir
	}

	var attachments []*db.MediaAttachment

	for _, info := range msg.GetMessageInfo() {
		mediaContent := info.GetMediaContent()
		if mediaContent == nil {
			continue
		}

		// When PullFullSizeMedia is enabled, attempt full-resolution image retrieval
		if opts.PullFullSizeMedia && info.GetActionMessageID() != "" {
			if fullDownloader, ok := downloader.(FullSizeImageDownloader); ok {
				_, _ = fullDownloader.GetFullSizeImage(ctx, msg.GetMessageID(), info.GetActionMessageID())
			}
		}

		mediaID := mediaContent.GetMediaID()
		decryptionKey := mediaContent.GetDecryptionKey()
		if mediaID == "" || len(decryptionKey) == 0 {
			// Check if thumbnail exists as fallback
			if mediaContent.GetThumbnailMediaID() != "" && len(mediaContent.GetThumbnailDecryptionKey()) > 0 {
				mediaID = mediaContent.GetThumbnailMediaID()
				decryptionKey = mediaContent.GetThumbnailDecryptionKey()
			} else {
				continue
			}
		}

		mimeType := mediaContent.GetMimeType()
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		ext := DetermineExtension(mimeType, mediaContent.GetMediaName())

		reader, err := downloader.DownloadMedia(mediaID, decryptionKey)
		if err != nil {
			return nil, fmt.Errorf("download media %s: %w", mediaID, err)
		}

		targetRelPath, sha256Hex, written, saveErr := SaveAttachment(reader, baseDir, ext)
		_ = reader.Close()
		if saveErr != nil {
			return nil, fmt.Errorf("save attachment %s: %w", mediaID, saveErr)
		}

		var width, height *int
		if dim := mediaContent.GetDimensions(); dim != nil {
			w := int(dim.GetWidth())
			h := int(dim.GetHeight())
			width = &w
			height = &h
		}

		att := &db.MediaAttachment{
			MessageID:       msg.GetMessageID(),
			ConversationID:  msg.GetConversationID(),
			SHA256Hash:      sha256Hex,
			RelativePath:    targetRelPath,
			MimeType:        mimeType,
			FileSizeBytes:   written,
			Width:           width,
			Height:          height,
			VisionProcessed: false,
		}

		attachments = append(attachments, att)
	}

	return attachments, nil
}
