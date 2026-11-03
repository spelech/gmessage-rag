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

	"github.com/rs/zerolog"
)

// DossierModelConfig configures the OpenAI-compatible dossier reconciliation client.
type DossierModelConfig struct {
	BaseURL    string
	APIKey     string
	Model      string
	HTTPClient *http.Client
	Logger     zerolog.Logger
}

type openAIDossierModel struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
	logger     zerolog.Logger
}

const dossierSystemPrompt = `You are the Living Dossier state reconciliation engine for a personal messaging archive.
You receive the PRIOR dossier state and one or more NEW conversation sessions with absolute dates.
Reconcile them into an updated dossier. Follow these rules exactly:
1. Return a JSON object with exactly three fields: "markdown", "structured_state", and "supersedes".
2. "markdown": a concise human-readable narrative of the contact profile and recent developments.
3. "structured_state": an object that may contain "profile" (object), "upcoming_events" (array),
   "past_events" (array), "people" (array), and "open_loops" (array).
4. Each event/loop entry must have an "id", a "title", and either an explicit ISO "date" (YYYY-MM-DD)
   or a "relative_date" phrase copied verbatim from the conversation (for example "tomorrow").
5. "supersedes": an array of entry "id" values whose facts are contradicted by newer sessions.
6. New facts supersede old facts; retain historical context rather than deleting it.
Respond ONLY with the raw JSON object; do not wrap it in markdown code fences.`

type dossierChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type dossierChatRequest struct {
	Model       string               `json:"model"`
	Messages    []dossierChatMessage `json:"messages"`
	Temperature float32              `json:"temperature"`
}

type dossierChatResponse struct {
	Choices []struct {
		Message dossierChatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

type dossierModelPayload struct {
	Markdown        string          `json:"markdown"`
	StructuredState json.RawMessage `json:"structured_state"`
	Supersedes      []string        `json:"supersedes"`
}

// NewOpenAIDossierModel constructs an OpenAI-compatible DossierModel implementing DossierModel.
func NewOpenAIDossierModel(cfg DossierModelConfig) DossierModel {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = "anthropic/claude-3.7-sonnet"
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second}
	}

	return &openAIDossierModel{
		baseURL:    baseURL,
		apiKey:     cfg.APIKey,
		model:      model,
		httpClient: client,
		logger:     cfg.Logger.With().Str("component", "dossier_model").Logger(),
	}
}

// Reconcile calls the OpenAI-compatible chat endpoint and parses the reconciled dossier.
func (m *openAIDossierModel) Reconcile(ctx context.Context, req DossierReconcileRequest) (*DossierReconcileResult, error) {
	priorStateJSON, err := json.Marshal(req.PriorState)
	if err != nil {
		return nil, fmt.Errorf("marshal prior state: %w", err)
	}

	var user strings.Builder
	fmt.Fprintf(&user, "Contact: %s\n", strings.TrimSpace(req.ContactName))
	fmt.Fprintf(&user, "Reference date (anchor relative phrases to this): %s\n\n", req.AnchorDate.UTC().Format("2006-01-02"))
	fmt.Fprintf(&user, "PRIOR MARKDOWN:\n%s\n\n", strings.TrimSpace(req.PriorMarkdown))
	fmt.Fprintf(&user, "PRIOR STRUCTURED STATE:\n%s\n\n", string(priorStateJSON))
	fmt.Fprintf(&user, "NEW SESSIONS:\n%s\n", req.SessionDigest)

	payload := dossierChatRequest{
		Model: m.model,
		Messages: []dossierChatMessage{
			{Role: "system", Content: dossierSystemPrompt},
			{Role: "user", Content: user.String()},
		},
		Temperature: 0.1,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal dossier request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/chat/completions", m.baseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create dossier request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if m.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+m.apiKey)
	}

	resp, err := m.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("exec dossier request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read dossier response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("dossier completion failed with status %d: %s", resp.StatusCode, SanitizeSecret(string(respBytes), m.apiKey))
	}

	var chatResp dossierChatResponse
	if err := json.Unmarshal(respBytes, &chatResp); err != nil {
		return nil, fmt.Errorf("unmarshal dossier response: %w", err)
	}
	if chatResp.Error != nil && chatResp.Error.Message != "" {
		return nil, fmt.Errorf("dossier completion api error: %s", chatResp.Error.Message)
	}
	if len(chatResp.Choices) == 0 {
		return nil, errors.New("dossier completion returned no choices")
	}

	content := cleanJSONFence(chatResp.Choices[0].Message.Content)

	var parsed dossierModelPayload
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal dossier payload: %w", err)
	}

	state := map[string]any{}
	if len(parsed.StructuredState) > 0 {
		if err := json.Unmarshal(parsed.StructuredState, &state); err != nil {
			return nil, fmt.Errorf("unmarshal structured_state: %w", err)
		}
	}

	return &DossierReconcileResult{
		Markdown:        strings.TrimSpace(parsed.Markdown),
		StructuredState: state,
		Supersedes:      parsed.Supersedes,
	}, nil
}

// cleanJSONFence strips a leading/trailing markdown code fence from model output.
func cleanJSONFence(content string) string {
	cleaned := strings.TrimSpace(content)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	return strings.TrimSpace(cleaned)
}
