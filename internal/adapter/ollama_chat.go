package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// defaultOllamaEndpoint is the canonical local ollama OpenAI-compat path.
const defaultOllamaEndpoint = "http://localhost:11434/v1/chat/completions"

// ollamaMaxRetries is the number of retry attempts on 503 / transient network
// errors. Each attempt waits retryBackoff * 2^(attempt-1) before retrying.
const ollamaMaxRetries = 3

// ollamaRetryBase is the initial backoff duration.
const ollamaRetryBase = 250 * time.Millisecond

// OllamaChat talks to an ollama /v1/chat/completions endpoint (OpenAI-compat
// surface). Prefer this adapter over OpenAIChat when targeting ollama directly:
// it adds retry/backoff on 503 and transient network errors which are common
// when ollama is loading a model, and it never touches openai-chat internals.
type OllamaChat struct {
	Endpoint   string
	HTTPClient *http.Client
}

// NewOllamaChat returns an OllamaChat adapter pointed at endpoint (defaults to
// http://localhost:11434/v1/chat/completions when endpoint is empty).
func NewOllamaChat(endpoint string) *OllamaChat {
	if endpoint == "" {
		endpoint = defaultOllamaEndpoint
	}
	return &OllamaChat{
		Endpoint:   endpoint,
		HTTPClient: &http.Client{Timeout: 180 * time.Second},
	}
}

func (*OllamaChat) Name() string { return "ollama-chat" }

// Chat issues one OpenAI-compat chat completion request to the ollama endpoint.
// Retries up to ollamaMaxRetries times on HTTP 503 or connection errors, with
// exponential backoff starting at ollamaRetryBase. A per-request timeout in
// req.Timeout overrides the adapter's default 180 s client timeout (used by
// B3's 5 s classifier timeout and B4's 2 s preflight health check).
func (a *OllamaChat) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	payload := openaiRequest{
		Model:       req.Model,
		Messages:    marshalMessages(req.Messages),
		Tools:       marshalTools(req.Tools),
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}
	// ollama /v1/chat/completions ignores tool_choice; omit to avoid
	// spurious warnings in older ollama builds.
	if len(req.Tools) > 0 {
		payload.ToolChoice = "auto"
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("ollama marshal request: %w", err)
	}

	client := a.HTTPClient
	if req.Timeout > 0 {
		client = &http.Client{Timeout: req.Timeout}
	}

	var (
		lastErr  error
		backoff  = ollamaRetryBase
	)
	for attempt := 0; attempt <= ollamaMaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ChatResponse{}, ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}

		resp, elapsed, err := a.doRequest(ctx, client, body, req.APIKey)
		if err != nil {
			// Transient network error — retry.
			lastErr = fmt.Errorf("ollama http: %w", err)
			continue
		}
		if resp.StatusCode == http.StatusServiceUnavailable {
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("ollama http 503: %s", truncate(string(raw), 256))
			continue
		}
		defer resp.Body.Close()

		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			return ChatResponse{}, fmt.Errorf("ollama read body: %w", err)
		}
		if resp.StatusCode >= 400 {
			return ChatResponse{}, fmt.Errorf("ollama http %d: %s", resp.StatusCode, truncate(string(raw), 512))
		}

		var parsed openaiResponse
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return ChatResponse{}, fmt.Errorf("ollama parse response: %w (body: %s)", err, truncate(string(raw), 256))
		}
		if len(parsed.Choices) == 0 {
			return ChatResponse{}, fmt.Errorf("ollama: empty choices")
		}
		msg := parsed.Choices[0].Message

		// Strip leading/trailing whitespace from content (ollama sometimes
		// emits a trailing newline in text-only responses).
		content := strings.TrimSpace(msg.Content)

		calls, err := decodeToolCalls(msg.ToolCalls)
		if err != nil {
			return ChatResponse{}, fmt.Errorf("ollama tool calls: %w", err)
		}
		return ChatResponse{
			Content:   content,
			ToolCalls: calls,
			Usage: Usage{
				PromptTokens:     parsed.Usage.PromptTokens,
				CompletionTokens: parsed.Usage.CompletionTokens,
				CachedTokens:     parsed.Usage.PromptTokensDetails.CachedTokens,
			},
			ElapsedMs: elapsed,
			TTFTMs:    elapsed,
		}, nil
	}
	return ChatResponse{}, fmt.Errorf("ollama: %d retries exhausted: %w", ollamaMaxRetries, lastErr)
}

// doRequest issues one HTTP POST and returns the response + wall-clock ms.
// The caller is responsible for closing resp.Body on a nil error return.
func (a *OllamaChat) doRequest(ctx context.Context, client *http.Client, body []byte, apiKey string) (*http.Response, int64, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}
	start := time.Now()
	resp, err := client.Do(httpReq)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return nil, elapsed, err
	}
	return resp, elapsed, nil
}
