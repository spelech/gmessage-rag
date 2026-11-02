package pipeline_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/spelech/gmessage-rag/internal/pipeline"
)

// [Requirement(PIPE-06)] [Requirement(DB-03)]
func TestSessionSummary_ContractStub(t *testing.T) {
	t.Run("Nominal_SummaryAndEmbedding", func(t *testing.T) {
		var chatCalls int32
		var embCalls int32

		stubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")

			switch r.URL.Path {
			case "/chat/completions":
				atomic.AddInt32(&chatCalls, 1)
				if r.Header.Get("Authorization") != "Bearer test-api-key" {
					http.Error(w, `{"error":{"message":"unauthorized"}}`, http.StatusUnauthorized)
					return
				}
				resp := map[string]any{
					"choices": []map[string]any{
						{
							"index": 0,
							"message": map[string]string{
								"role":    "assistant",
								"content": `{"title": "Dinner Reservation", "summary": "Agreed to meet at 7pm for Italian."}`,
							},
						},
					},
				}
				_ = json.NewEncoder(w).Encode(resp)

			case "/embeddings":
				atomic.AddInt32(&embCalls, 1)
				if r.Header.Get("Authorization") != "Bearer test-emb-key" {
					http.Error(w, `{"error":{"message":"unauthorized"}}`, http.StatusUnauthorized)
					return
				}
				dummyVec := make([]float32, 1536)
				dummyVec[0] = 0.5
				resp := map[string]any{
					"data": []map[string]any{
						{
							"index":     0,
							"embedding": dummyVec,
						},
					},
				}
				_ = json.NewEncoder(w).Encode(resp)

			default:
				http.NotFound(w, r)
			}
		}))
		defer stubServer.Close()

		client := pipeline.NewOpenAIClient(pipeline.LLMClientConfig{
			LLMBaseURL:          stubServer.URL,
			LLMAPIKey:           "test-api-key",
			SummaryModel:        "test-summary-model",
			EmbeddingBaseURL:    stubServer.URL,
			EmbeddingAPIKey:     "test-emb-key",
			EmbeddingModel:      "test-emb-model",
			EmbeddingDimensions: 1536,
		})

		ctx := context.Background()

		// Test Summary
		title, summary, err := client.GenerateSummary(ctx, "[2026-11-02 12:00:00] Alice: Dinner at 7?")
		if err != nil {
			t.Fatalf("expected nil error on GenerateSummary, got %v", err)
		}
		if title != "Dinner Reservation" {
			t.Errorf("expected title 'Dinner Reservation', got %q", title)
		}
		if summary != "Agreed to meet at 7pm for Italian." {
			t.Errorf("expected summary 'Agreed to meet at 7pm for Italian.', got %q", summary)
		}

		// Test Embedding
		vec, err := client.GenerateEmbedding(ctx, "Dinner Reservation: Agreed to meet at 7pm for Italian.")
		if err != nil {
			t.Fatalf("expected nil error on GenerateEmbedding, got %v", err)
		}
		if len(vec) != 1536 {
			t.Fatalf("expected 1536-dim vector, got %d", len(vec))
		}
		if vec[0] != 0.5 {
			t.Errorf("expected vec[0] == 0.5, got %f", vec[0])
		}

		if atomic.LoadInt32(&chatCalls) != 1 {
			t.Errorf("expected 1 chat call, got %d", atomic.LoadInt32(&chatCalls))
		}
		if atomic.LoadInt32(&embCalls) != 1 {
			t.Errorf("expected 1 embedding call, got %d", atomic.LoadInt32(&embCalls))
		}
	})

	t.Run("MarkdownWrappedJSON_And_FallbackParsing", func(t *testing.T) {
		stubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			resp := map[string]any{
				"choices": []map[string]any{
					{
						"index": 0,
						"message": map[string]string{
							"role":    "assistant",
							"content": "```json\n{\"title\": \"Weekend Trip\", \"summary\": \"Discussed flights to Denver.\"}\n```",
						},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
		}))
		defer stubServer.Close()

		client := pipeline.NewOpenAIClient(pipeline.LLMClientConfig{
			LLMBaseURL: stubServer.URL,
		})

		title, summary, err := client.GenerateSummary(context.Background(), "transcript text")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if title != "Weekend Trip" || summary != "Discussed flights to Denver." {
			t.Errorf("unexpected title/summary: %q / %q", title, summary)
		}
	})

	t.Run("PlaintextFallbackParsing", func(t *testing.T) {
		stubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			resp := map[string]any{
				"choices": []map[string]any{
					{
						"index": 0,
						"message": map[string]string{
							"role":    "assistant",
							"content": "Movie Night Plans\nDecided to watch Interstellar on Friday.",
						},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
		}))
		defer stubServer.Close()

		client := pipeline.NewOpenAIClient(pipeline.LLMClientConfig{
			LLMBaseURL: stubServer.URL,
		})

		title, summary, err := client.GenerateSummary(context.Background(), "transcript text")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if title != "Movie Night Plans" || summary != "Decided to watch Interstellar on Friday." {
			t.Errorf("unexpected fallback parse: title=%q summary=%q", title, summary)
		}
	})

	t.Run("ErrorHandling_HTTPError_EmptyInputs_And_CancelledContext", func(t *testing.T) {
		stubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"error":{"message":"Rate limit exceeded"}}`, http.StatusTooManyRequests)
		}))
		defer stubServer.Close()

		client := pipeline.NewOpenAIClient(pipeline.LLMClientConfig{
			LLMBaseURL:       stubServer.URL,
			EmbeddingBaseURL: stubServer.URL,
		})

		// Empty transcript check
		_, _, err := client.GenerateSummary(context.Background(), "")
		if err == nil {
			t.Error("expected error for empty transcript, got nil")
		}

		// Empty embedding input check
		_, err = client.GenerateEmbedding(context.Background(), "   ")
		if err == nil {
			t.Error("expected error for empty embedding text, got nil")
		}

		// HTTP 429 Error check
		_, _, err = client.GenerateSummary(context.Background(), "some transcript")
		if err == nil {
			t.Error("expected error on HTTP 429, got nil")
		}

		_, err = client.GenerateEmbedding(context.Background(), "some transcript")
		if err == nil {
			t.Error("expected error on HTTP 429 embedding, got nil")
		}

		// Cancelled context check
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, _, err = client.GenerateSummary(ctx, "transcript")
		if err == nil {
			t.Error("expected error with cancelled context, got nil")
		}

		_, err = client.GenerateEmbedding(ctx, "transcript")
		if err == nil {
			t.Error("expected error with cancelled context, got nil")
		}
	})

	t.Run("EmptyChoicesOrInvalidData", func(t *testing.T) {
		stubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/chat/completions" {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{}})
			} else {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
			}
		}))
		defer stubServer.Close()

		client := pipeline.NewOpenAIClient(pipeline.LLMClientConfig{
			LLMBaseURL:       stubServer.URL,
			EmbeddingBaseURL: stubServer.URL,
		})

		_, _, err := client.GenerateSummary(context.Background(), "test transcript")
		if err == nil {
			t.Error("expected error when choices are empty, got nil")
		}

		_, err = client.GenerateEmbedding(context.Background(), "test text")
		if err == nil {
			t.Error("expected error when embedding data is empty, got nil")
		}
	})
}
