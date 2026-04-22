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

// defaultOllamaNativeEndpoint is ollama's native chat API path.
const defaultOllamaNativeEndpoint = "http://localhost:11434/api/chat"

// OllamaNative talks to ollama's native /api/chat endpoint. Prefer this over
// OllamaChat (which hits ollama's /v1/chat/completions OpenAI-compat shim)
// when reliability matters — the compat shim has been observed to flip
// answers on borderline inputs at temperature=0, while the native API
// honours sampling parameters more faithfully.
//
// Designed for the judge path where determinism is load-bearing.
// Does not support tool calls — the judge responds only with plain text.
type OllamaNative struct {
	Endpoint   string
	HTTPClient *http.Client
}

// NewOllamaNative returns an OllamaNative adapter pointed at endpoint
// (defaults to http://localhost:11434/api/chat when endpoint is empty).
func NewOllamaNative(endpoint string) *OllamaNative {
	if endpoint == "" {
		endpoint = defaultOllamaNativeEndpoint
	}
	return &OllamaNative{
		Endpoint:   endpoint,
		HTTPClient: &http.Client{Timeout: 180 * time.Second},
	}
}

func (*OllamaNative) Name() string { return "ollama-native" }

// ollamaNativeRequest mirrors ollama's /api/chat request shape.
type ollamaNativeRequest struct {
	Model    string              `json:"model"`
	Messages []ollamaNativeMessage `json:"messages"`
	Stream   bool                `json:"stream"`
	Options  ollamaNativeOptions `json:"options,omitempty"`
}

type ollamaNativeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ollamaNativeOptions uses snake_case because ollama's native API takes
// sampling params under "options", not alongside "temperature" at the top
// level (that's the OpenAI-compat shape).
type ollamaNativeOptions struct {
	Temperature float64 `json:"temperature,omitempty"`
	NumPredict  int     `json:"num_predict,omitempty"` // ollama's equivalent of max_tokens
	Seed        int     `json:"seed,omitempty"`
}

// ollamaNativeResponse mirrors ollama's /api/chat response shape when
// stream=false. Usage data is in fields like prompt_eval_count and
// eval_count — we capture what maps cleanly onto our Usage struct.
type ollamaNativeResponse struct {
	Model           string              `json:"model"`
	Message         ollamaNativeMessage `json:"message"`
	Done            bool                `json:"done"`
	PromptEvalCount int                 `json:"prompt_eval_count"`
	EvalCount       int                 `json:"eval_count"`
}

// Chat issues one native ollama chat request. Retries up to ollamaMaxRetries
// times on HTTP 503 or connection errors (same behaviour as OllamaChat).
func (a *OllamaNative) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	msgs := make([]ollamaNativeMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, ollamaNativeMessage{Role: m.Role, Content: m.Content})
	}
	payload := ollamaNativeRequest{
		Model:    req.Model,
		Messages: msgs,
		Stream:   false,
		Options: ollamaNativeOptions{
			Temperature: req.Temperature,
			NumPredict:  req.MaxTokens,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("ollama-native marshal request: %w", err)
	}

	client := a.HTTPClient
	if req.Timeout > 0 {
		client = &http.Client{Timeout: req.Timeout}
	}

	var (
		lastErr error
		backoff = ollamaRetryBase
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

		resp, elapsed, err := a.doRequest(ctx, client, body)
		if err != nil {
			lastErr = fmt.Errorf("ollama-native http: %w", err)
			continue
		}
		if resp.StatusCode == http.StatusServiceUnavailable {
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("ollama-native http 503: %s", truncate(string(raw), 256))
			continue
		}
		defer resp.Body.Close()

		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			return ChatResponse{}, fmt.Errorf("ollama-native read body: %w", err)
		}
		if resp.StatusCode >= 400 {
			return ChatResponse{}, fmt.Errorf("ollama-native http %d: %s", resp.StatusCode, truncate(string(raw), 512))
		}

		var parsed ollamaNativeResponse
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return ChatResponse{}, fmt.Errorf("ollama-native parse response: %w (body: %s)", err, truncate(string(raw), 256))
		}

		content := strings.TrimSpace(parsed.Message.Content)
		return ChatResponse{
			Content:   content,
			ToolCalls: nil,
			Usage: Usage{
				PromptTokens:     parsed.PromptEvalCount,
				CompletionTokens: parsed.EvalCount,
			},
			ElapsedMs: elapsed,
			TTFTMs:    elapsed,
		}, nil
	}
	return ChatResponse{}, fmt.Errorf("ollama-native: %d retries exhausted: %w", ollamaMaxRetries, lastErr)
}

// doRequest issues one HTTP POST. Caller closes resp.Body on nil error.
func (a *OllamaNative) doRequest(ctx context.Context, client *http.Client, body []byte) (*http.Response, int64, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	start := time.Now()
	resp, err := client.Do(httpReq)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return nil, elapsed, err
	}
	return resp, elapsed, nil
}
