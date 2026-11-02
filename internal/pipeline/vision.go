package pipeline

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/spelech/gmessage-rag/internal/db"
)

// VisionExtraction contains the extracted OCR text and visual description.
type VisionExtraction struct {
	OCRText           string `json:"ocr_text"`
	VisualDescription string `json:"visual_description"`
}

// VisionClient abstracts inference against an OpenAI-compatible vision model.
type VisionClient interface {
	ExtractImageContext(ctx context.Context, imageData []byte, mimeType string) (*VisionExtraction, error)
}

// OpenAIVisionClientConfig configures an OpenAIVisionClient.
type OpenAIVisionClientConfig struct {
	BaseURL    string
	APIKey     string
	Model      string
	HTTPClient *http.Client
	MaxRetries int
	RetryDelay time.Duration
	Logger     zerolog.Logger
}

// OpenAIVisionClient implements VisionClient via OpenAI-compatible Chat Completions API.
type OpenAIVisionClient struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
	maxRetries int
	retryDelay time.Duration
	logger     zerolog.Logger
}

// NewOpenAIVisionClient constructs a new OpenAIVisionClient.
func NewOpenAIVisionClient(cfg OpenAIVisionClientConfig) *OpenAIVisionClient {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = "google/gemini-2.5-flash"
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	maxRetries := cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}
	retryDelay := cfg.RetryDelay
	if retryDelay <= 0 {
		retryDelay = 500 * time.Millisecond
	}

	return &OpenAIVisionClient{
		baseURL:    baseURL,
		apiKey:     cfg.APIKey,
		model:      model,
		httpClient: client,
		maxRetries: maxRetries,
		retryDelay: retryDelay,
		logger:     cfg.Logger.With().Str("component", "vision_client").Logger(),
	}
}

// SanitizeSecret ensures API keys or credentials are never exposed in log outputs or error messages.
func SanitizeSecret(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "[REDACTED]")
}

type openAIChatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type openAIContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *openAIImageURL `json:"image_url,omitempty"`
}

type openAIImageURL struct {
	URL string `json:"url"`
}

type openAIChatRequest struct {
	Model       string              `json:"model"`
	Messages    []openAIChatMessage `json:"messages"`
	Temperature float32             `json:"temperature"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

const visionPrompt = `You are a high-precision vision OCR and image description engine. Analyze this image carefully.
Respond strictly with a valid JSON object conforming to this schema:
{
  "ocr_text": "all verbatim text visible in the image, signs, screenshots, documents, receipts, or text bubbles. If no text exists, return empty string \"\".",
  "visual_description": "a concise 1-2 sentence description summarizing the visual scene, subject matter, and key details."
}
Do not wrap your response in markdown code blocks like ` + "```json" + `. Return ONLY raw JSON.`

// ExtractImageContext sends an image to the OpenAI-compatible vision model and extracts OCR and description.
func (c *OpenAIVisionClient) ExtractImageContext(ctx context.Context, imageData []byte, mimeType string) (*VisionExtraction, error) {
	if len(imageData) == 0 {
		return nil, errors.New("image data cannot be empty")
	}
	if mimeType == "" {
		mimeType = "image/jpeg"
	}

	b64Image := base64.StdEncoding.EncodeToString(imageData)
	dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, b64Image)

	reqPayload := openAIChatRequest{
		Model: c.model,
		Messages: []openAIChatMessage{
			{
				Role: "user",
				Content: []openAIContentPart{
					{
						Type: "text",
						Text: visionPrompt,
					},
					{
						Type: "image_url",
						ImageURL: &openAIImageURL{
							URL: dataURI,
						},
					},
				},
			},
		},
		Temperature: 0.1,
	}

	reqBody, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, fmt.Errorf("marshal vision request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/chat/completions", c.baseURL)

	var lastErr error
	for attempt := 1; attempt <= c.maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if c.apiKey != "" {
			httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
		}

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			lastErr = fmt.Errorf("attempt %d: HTTP request failed: %s", attempt, SanitizeSecret(err.Error(), c.apiKey))
			c.logger.Warn().Err(lastErr).Int("attempt", attempt).Msg("Vision model request network error")
			time.Sleep(c.retryDelay * time.Duration(attempt))
			continue
		}

		bodyBytes, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if readErr != nil {
			lastErr = fmt.Errorf("attempt %d: read response: %s", attempt, SanitizeSecret(readErr.Error(), c.apiKey))
			time.Sleep(c.retryDelay * time.Duration(attempt))
			continue
		}

		if resp.StatusCode != http.StatusOK {
			errMsg := SanitizeSecret(string(bodyBytes), c.apiKey)
			lastErr = fmt.Errorf("attempt %d: vision API returned status %d: %s", attempt, resp.StatusCode, errMsg)
			// Retry on 429 rate limit or 5xx server errors
			if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
				c.logger.Warn().Int("status", resp.StatusCode).Int("attempt", attempt).Msg("Retrying vision model request")
				time.Sleep(c.retryDelay * time.Duration(attempt))
				continue
			}
			// Non-retryable status (e.g. 400, 401, 403, 404)
			return nil, lastErr
		}

		var chatResp openAIChatResponse
		if err := json.Unmarshal(bodyBytes, &chatResp); err != nil {
			lastErr = fmt.Errorf("attempt %d: unmarshal chat completion: %w", attempt, err)
			time.Sleep(c.retryDelay * time.Duration(attempt))
			continue
		}

		if len(chatResp.Choices) == 0 {
			lastErr = fmt.Errorf("attempt %d: empty choices returned from vision model", attempt)
			time.Sleep(c.retryDelay * time.Duration(attempt))
			continue
		}

		rawContent := strings.TrimSpace(chatResp.Choices[0].Message.Content)
		// Clean markdown code blocks if present
		cleanContent := strings.TrimPrefix(rawContent, "```json")
		cleanContent = strings.TrimPrefix(cleanContent, "```")
		cleanContent = strings.TrimSuffix(cleanContent, "```")
		cleanContent = strings.TrimSpace(cleanContent)

		var extraction VisionExtraction
		if err := json.Unmarshal([]byte(cleanContent), &extraction); err != nil {
			// If JSON parsing fails, fall back to setting visual description to full response
			extraction = VisionExtraction{
				OCRText:           "",
				VisualDescription: rawContent,
			}
		}

		return &extraction, nil
	}

	return nil, fmt.Errorf("vision model failed after %d attempts: %w", c.maxRetries, lastErr)
}

// VisionService coordinates processing unprocessed media attachments with VisionClient and DB.
type VisionService struct {
	Client   VisionClient
	Database *db.DB
	MediaDir string
	Logger   zerolog.Logger
}

// ProcessMediaAttachment processes a single media attachment record:
// reads image file, calls VisionClient, updates DB, sets vision_processed = true.
// Failure is non-blocking: logs error and returns error without panicking.
func (s *VisionService) ProcessMediaAttachment(ctx context.Context, media db.MediaAttachment) error {
	if s.Client == nil {
		return errors.New("vision client is nil")
	}
	if s.Database == nil {
		return errors.New("database is nil")
	}

	filePath := filepath.Join(s.MediaDir, media.RelativePath)
	fileBytes, err := os.ReadFile(filePath)
	if err != nil {
		s.Logger.Warn().Err(err).Int64("media_id", media.ID).Str("path", filePath).Msg("Failed to read media file for vision processing")
		return fmt.Errorf("read media file %s: %w", filePath, err)
	}

	extraction, err := s.Client.ExtractImageContext(ctx, fileBytes, media.MimeType)
	if err != nil {
		s.Logger.Warn().Err(err).Int64("media_id", media.ID).Msg("Vision extraction failed for attachment")
		return fmt.Errorf("vision extraction: %w", err)
	}

	ocrText := extraction.OCRText
	visualDesc := extraction.VisualDescription

	updateErr := s.Database.UpdateMediaAttachmentVision(ctx, media.ID, &ocrText, &visualDesc, true)
	if updateErr != nil {
		s.Logger.Error().Err(updateErr).Int64("media_id", media.ID).Msg("Failed to persist vision OCR extraction to database")
		return fmt.Errorf("update vision db: %w", updateErr)
	}

	s.Logger.Info().Int64("media_id", media.ID).Str("message_id", media.MessageID).Msg("Successfully processed vision OCR for attachment")
	return nil
}

// ProcessUnprocessedAttachments queries pending attachments for conversation (or all if empty) and processes them.
func (s *VisionService) ProcessUnprocessedAttachments(ctx context.Context, conversationID string, limit int) (int, error) {
	if s.Database == nil {
		return 0, errors.New("database is nil")
	}

	unprocessed, err := s.Database.GetUnprocessedMediaAttachments(ctx, conversationID, limit)
	if err != nil {
		return 0, fmt.Errorf("get unprocessed attachments: %w", err)
	}

	processedCount := 0
	for _, att := range unprocessed {
		select {
		case <-ctx.Done():
			return processedCount, ctx.Err()
		default:
		}

		// Only process image attachments
		if !strings.HasPrefix(strings.ToLower(att.MimeType), "image/") {
			// Mark non-image attachments as processed so they don't get retried indefinitely
			_ = s.Database.UpdateMediaAttachmentVision(ctx, att.ID, nil, nil, true)
			continue
		}

		if err := s.ProcessMediaAttachment(ctx, att); err != nil {
			s.Logger.Warn().Err(err).Int64("media_id", att.ID).Msg("Non-blocking vision processing failure")
			// Non-blocking: continue processing other attachments
			continue
		}
		processedCount++
	}

	return processedCount, nil
}
