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

// ExtractAndSaveMedia inspects a message's attachments, downloads & decrypts them,
// saves them content-addressed by SHA-256 hash, and returns db.MediaAttachment records.
func ExtractAndSaveMedia(ctx context.Context, downloader MediaDownloader, msg *gmproto.Message, baseDir string) ([]*db.MediaAttachment, error) {
	if msg == nil {
		return nil, errors.New("message cannot be nil")
	}
	if downloader == nil {
		return nil, errors.New("downloader cannot be nil")
	}
	if baseDir == "" {
		baseDir = DefaultMediaDir
	}

	var attachments []*db.MediaAttachment

	for _, info := range msg.GetMessageInfo() {
		mediaContent := info.GetMediaContent()
		if mediaContent == nil {
			continue
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

		// Download to a temporary file in baseDir while computing SHA-256
		if err := os.MkdirAll(baseDir, 0700); err != nil {
			reader.Close()
			return nil, fmt.Errorf("create base media dir %s: %w", baseDir, err)
		}

		tmpFile, err := os.CreateTemp(baseDir, "media-download-*.tmp")
		if err != nil {
			reader.Close()
			return nil, fmt.Errorf("create temp media file: %w", err)
		}
		tmpPath := tmpFile.Name()

		hasher := sha256.New()
		multiWriter := io.MultiWriter(tmpFile, hasher)

		written, copyErr := io.Copy(multiWriter, reader)
		reader.Close()
		tmpCloseErr := tmpFile.Close()

		if copyErr != nil {
			os.Remove(tmpPath)
			return nil, fmt.Errorf("copy media data %s: %w", mediaID, copyErr)
		}
		if tmpCloseErr != nil {
			os.Remove(tmpPath)
			return nil, fmt.Errorf("close temp file: %w", tmpCloseErr)
		}

		hashBytes := hasher.Sum(nil)
		sha256Hex := hex.EncodeToString(hashBytes)

		targetPath := ContentAddressedPath(baseDir, sha256Hex, ext)
		targetRelPath := RelativeMediaPath(sha256Hex, ext)

		// Ensure target directory exists
		targetDir := filepath.Dir(targetPath)
		if err := os.MkdirAll(targetDir, 0700); err != nil {
			os.Remove(tmpPath)
			return nil, fmt.Errorf("create content-addressed dir %s: %w", targetDir, err)
		}

		// If target file already exists, discard temp file (idempotent duplicate avoidance)
		if _, err := os.Stat(targetPath); err == nil {
			os.Remove(tmpPath)
		} else {
			if err := os.Rename(tmpPath, targetPath); err != nil {
				os.Remove(tmpPath)
				return nil, fmt.Errorf("rename temp media file to target %s: %w", targetPath, err)
			}
			// Enforce POSIX 0600 file permissions
			_ = os.Chmod(targetPath, 0600)
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
