package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// AvailableModel describes one model advertised by the LLM gateway.
type AvailableModel struct {
	ID             string `json:"id"`
	SupportsVision bool   `json:"supports_vision"`
	Mode           string `json:"mode,omitempty"`
	Provider       string `json:"provider,omitempty"`
}

// ListAvailableModels queries the OpenAI-compatible gateway for the models it
// advertises. It reads capabilities from /model/info when available and falls
// back to the plain id list from /v1/models.
func ListAvailableModels(ctx context.Context, baseURL, apiKey string) ([]AvailableModel, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, errors.New("llm base url cannot be empty")
	}

	client := &http.Client{Timeout: 15 * time.Second}

	models := map[string]AvailableModel{}
	if info, err := fetchModelInfo(ctx, client, base, apiKey); err == nil {
		for _, m := range info {
			models[m.ID] = m
		}
	}

	ids, err := fetchModelIDs(ctx, client, base, apiKey)
	if err != nil {
		if len(models) == 0 {
			return nil, err
		}
	}
	for _, id := range ids {
		if _, ok := models[id]; !ok {
			models[id] = AvailableModel{ID: id}
		}
	}

	out := make([]AvailableModel, 0, len(models))
	for _, m := range models {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func fetchModelIDs(ctx context.Context, client *http.Client, base, apiKey string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list models returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read models response: %w", err)
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse models response: %w", err)
	}
	ids := make([]string, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		if id := strings.TrimSpace(m.ID); id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func fetchModelInfo(ctx context.Context, client *http.Client, base, apiKey string) ([]AvailableModel, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/model/info", nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("model info: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("model info returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read model info response: %w", err)
	}
	var parsed struct {
		Data []struct {
			ModelName string `json:"model_name"`
			ModelInfo struct {
				SupportsVision *bool  `json:"supports_vision"`
				Mode           string `json:"mode"`
				Provider       string `json:"litellm_provider"`
			} `json:"model_info"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse model info response: %w", err)
	}
	out := make([]AvailableModel, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		id := strings.TrimSpace(m.ModelName)
		if id == "" {
			continue
		}
		vision := false
		if m.ModelInfo.SupportsVision != nil {
			vision = *m.ModelInfo.SupportsVision
		}
		out = append(out, AvailableModel{
			ID:             id,
			SupportsVision: vision,
			Mode:           m.ModelInfo.Mode,
			Provider:       m.ModelInfo.Provider,
		})
	}
	return out, nil
}
