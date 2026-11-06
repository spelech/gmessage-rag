package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Summarizer produces a concise title and summary for a conversation session transcript.
type Summarizer interface {
	GenerateSummary(ctx context.Context, transcript string) (title string, summary string, err error)
}

// Embedder generates a dense vector embedding for text.
type Embedder interface {
	GenerateEmbedding(ctx context.Context, text string) ([]float32, error)
}

// LLMClient combines summarization and dense vector embedding capabilities.
type LLMClient interface {
	Summarizer
	Embedder
}

// LLMClientConfig configures the OpenAI-compatible HTTP LLM client.
type LLMClientConfig struct {
	LLMBaseURL          string
	LLMAPIKey           string
	SummaryModel        string
	EmbeddingBaseURL    string
	EmbeddingAPIKey     string
	EmbeddingModel      string
	EmbeddingDimensions int
	HTTPClient          *http.Client
	// SummaryModelFunc and EmbeddingModelFunc, when set, return the model to
	// use for each call. They enable hot-reloadable model selection.
	SummaryModelFunc   func() string
	EmbeddingModelFunc func() string
}

type openAIClient struct {
	llmBaseURL          string
	llmAPIKey           string
	summaryModel        string
	embeddingBaseURL    string
	embeddingAPIKey     string
	embeddingModel      string
	embeddingDimensions int
	httpClient          *http.Client
	summaryModelFunc    func() string
	embeddingModelFunc  func() string
}

// NewOpenAIClient constructs an OpenAI-compatible LLM client implementing LLMClient.
func NewOpenAIClient(cfg LLMClientConfig) LLMClient {
	hc := cfg.HTTPClient
	if hc == nil {
		// Reasoning models can take longer before emitting a response.
		hc = &http.Client{Timeout: 180 * time.Second}
	}

	llmBase := strings.TrimRight(cfg.LLMBaseURL, "/")
	if llmBase == "" {
		llmBase = "https://openrouter.ai/api/v1"
	}

	embBase := strings.TrimRight(cfg.EmbeddingBaseURL, "/")
	if embBase == "" {
		embBase = "https://api.openai.com/v1"
	}

	summaryModel := cfg.SummaryModel
	if summaryModel == "" {
		summaryModel = "google/gemini-2.5-flash"
	}

	embModel := cfg.EmbeddingModel
	if embModel == "" {
		embModel = "text-embedding-3-small"
	}

	dims := cfg.EmbeddingDimensions
	if dims <= 0 {
		dims = 1536
	}

	return &openAIClient{
		llmBaseURL:          llmBase,
		llmAPIKey:           cfg.LLMAPIKey,
		summaryModel:        summaryModel,
		embeddingBaseURL:    embBase,
		embeddingAPIKey:     cfg.EmbeddingAPIKey,
		embeddingModel:      embModel,
		embeddingDimensions: dims,
		httpClient:          hc,
		summaryModelFunc:    cfg.SummaryModelFunc,
		embeddingModelFunc:  cfg.EmbeddingModelFunc,
	}
}

// resolveSummaryModel returns the hot-reloadable summary model, falling back to
// the statically configured value.
func (c *openAIClient) resolveSummaryModel() string {
	if c.summaryModelFunc != nil {
		if m := strings.TrimSpace(c.summaryModelFunc()); m != "" {
			return m
		}
	}
	return c.summaryModel
}

// resolveEmbeddingModel returns the hot-reloadable embedding model, falling back
// to the statically configured value.
func (c *openAIClient) resolveEmbeddingModel() string {
	if c.embeddingModelFunc != nil {
		if m := strings.TrimSpace(c.embeddingModelFunc()); m != "" {
			return m
		}
	}
	return c.embeddingModel
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float32       `json:"temperature,omitempty"`
}

type chatCompletionChoice struct {
	Index   int         `json:"index"`
	Message chatMessage `json:"message"`
}

type chatCompletionResponse struct {
	Choices []chatCompletionChoice `json:"choices"`
	Error   *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

type summaryJSONResponse struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

const summarySystemPrompt = `You are an assistant that summarizes contiguous messaging sessions.
Analyze the following conversation transcript and return a JSON object with exactly two string fields:
1. "title": A concise 3-6 word semantic topic for the conversation.
2. "summary": A concise bulleted or 1-3 sentence summary capturing key topics, decisions, and action items.
Respond ONLY with a valid JSON object matching {"title": "...", "summary": "..."}.`

// GenerateSummary queries the OpenAI-compatible chat endpoint to produce a title and summary.
func (c *openAIClient) GenerateSummary(ctx context.Context, transcript string) (string, string, error) {
	if strings.TrimSpace(transcript) == "" {
		return "", "", errors.New("transcript cannot be empty")
	}

	reqBody := chatCompletionRequest{
		Model: c.resolveSummaryModel(),
		Messages: []chatMessage{
			{Role: "system", Content: summarySystemPrompt},
			{Role: "user", Content: transcript},
		},
		Temperature: 0.2,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", "", fmt.Errorf("marshal chat request: %w", err)
	}

	reqURL := fmt.Sprintf("%s/chat/completions", c.llmBaseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", "", fmt.Errorf("create chat request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.llmAPIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.llmAPIKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", "", fmt.Errorf("exec chat request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("read chat response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("chat completion failed with status %d: %s", resp.StatusCode, SanitizeSecret(string(respBytes), c.llmAPIKey))
	}

	var chatResp chatCompletionResponse
	if err := json.Unmarshal(respBytes, &chatResp); err != nil {
		return "", "", fmt.Errorf("unmarshal chat response: %w", err)
	}

	if chatResp.Error != nil && chatResp.Error.Message != "" {
		return "", "", fmt.Errorf("chat completion api error: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return "", "", errors.New("chat completion returned no choices")
	}

	content := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	// Try parsing JSON first
	var sumJSON summaryJSONResponse
	// Clean markdown backticks if model wrapped JSON in ```json ... ```
	cleaned := strings.TrimPrefix(content, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	if err := json.Unmarshal([]byte(cleaned), &sumJSON); err == nil && sumJSON.Title != "" {
		return strings.TrimSpace(sumJSON.Title), strings.TrimSpace(sumJSON.Summary), nil
	}

	// Fallback heuristic if not valid JSON: split first line as title, rest as summary
	lines := strings.SplitN(content, "\n", 2)
	title := strings.TrimSpace(lines[0])
	summary := ""
	if len(lines) > 1 {
		summary = strings.TrimSpace(lines[1])
	}
	if summary == "" {
		summary = title
	}

	return title, summary, nil
}

type embeddingRequest struct {
	Model      string `json:"model"`
	Input      string `json:"input"`
	Dimensions int    `json:"dimensions,omitempty"`
}

type embeddingItem struct {
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

type embeddingResponse struct {
	Data  []embeddingItem `json:"data"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// GenerateEmbedding calls the OpenAI-compatible embedding endpoint.
func (c *openAIClient) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("text cannot be empty")
	}

	reqBody := embeddingRequest{
		Model:      c.resolveEmbeddingModel(),
		Input:      text,
		Dimensions: c.embeddingDimensions,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal embedding request: %w", err)
	}

	reqURL := fmt.Sprintf("%s/embeddings", c.embeddingBaseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create embedding request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.embeddingAPIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.embeddingAPIKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("exec embedding request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read embedding response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding failed with status %d: %s", resp.StatusCode, SanitizeSecret(string(respBytes), c.embeddingAPIKey))
	}

	var embResp embeddingResponse
	if err := json.Unmarshal(respBytes, &embResp); err != nil {
		return nil, fmt.Errorf("unmarshal embedding response: %w", err)
	}

	if embResp.Error != nil && embResp.Error.Message != "" {
		return nil, fmt.Errorf("embedding api error: %s", embResp.Error.Message)
	}

	if len(embResp.Data) == 0 {
		return nil, errors.New("embedding returned no data items")
	}

	vec := embResp.Data[0].Embedding
	if len(vec) == 0 {
		return nil, errors.New("embedding vector is empty")
	}

	return vec, nil
}
