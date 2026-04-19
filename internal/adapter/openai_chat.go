package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// OpenAIChat talks to an OpenAI-compatible /v1/chat/completions endpoint.
// v1 target is llm-proxy at spark-01:4000 routing to a vLLM backend.
type OpenAIChat struct {
	Endpoint   string
	HTTPClient *http.Client
}

// NewOpenAIChat returns an adapter with a stdlib http.Client configured for
// 180s request timeouts (matches spec §2).
func NewOpenAIChat(endpoint string) *OpenAIChat {
	return &OpenAIChat{
		Endpoint:   endpoint,
		HTTPClient: &http.Client{Timeout: 180 * time.Second},
	}
}

func (*OpenAIChat) Name() string { return "openai-chat" }

// Chat issues one request. It tolerates both string and object forms of
// tool_calls[*].function.arguments per spec §2 ("arguments may be a JSON
// string or a nested object").
func (a *OpenAIChat) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	payload := openaiRequest{
		Model:       req.Model,
		Messages:    marshalMessages(req.Messages),
		Tools:       marshalTools(req.Tools),
		ToolChoice:  "auto",
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	client := a.HTTPClient
	if req.Timeout > 0 {
		client = &http.Client{Timeout: req.Timeout}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.Endpoint, bytes.NewReader(body))
	if err != nil {
		return ChatResponse{}, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if req.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+req.APIKey)
	}

	start := time.Now()
	resp, err := client.Do(httpReq)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("read body: %w", err)
	}
	elapsed := time.Since(start).Milliseconds()
	if resp.StatusCode >= 400 {
		return ChatResponse{}, fmt.Errorf("http %d: %s", resp.StatusCode, truncate(string(raw), 512))
	}

	var parsed openaiResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return ChatResponse{}, fmt.Errorf("parse response: %w (body: %s)", err, truncate(string(raw), 256))
	}
	if len(parsed.Choices) == 0 {
		return ChatResponse{}, fmt.Errorf("empty choices")
	}
	msg := parsed.Choices[0].Message
	calls, err := decodeToolCalls(msg.ToolCalls)
	if err != nil {
		return ChatResponse{}, err
	}
	return ChatResponse{
		Content:   msg.Content,
		ToolCalls: calls,
		Usage: Usage{
			PromptTokens:     parsed.Usage.PromptTokens,
			CompletionTokens: parsed.Usage.CompletionTokens,
			CachedTokens:     parsed.Usage.PromptTokensDetails.CachedTokens,
		},
		ElapsedMs: elapsed,
		TTFTMs:    elapsed, // non-streaming client
	}, nil
}

type openaiRequest struct {
	Model       string          `json:"model"`
	Messages    []openaiMessage `json:"messages"`
	Tools       []openaiTool    `json:"tools,omitempty"`
	ToolChoice  string          `json:"tool_choice,omitempty"`
	Temperature float64         `json:"temperature"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
}

type openaiMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCalls  []openaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	Name       string           `json:"name,omitempty"`
}

type openaiTool struct {
	Type     string       `json:"type"`
	Function openaiFunDef `json:"function"`
}

type openaiFunDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type openaiToolCall struct {
	ID       string              `json:"id,omitempty"`
	Type     string              `json:"type,omitempty"`
	Function openaiToolCallFunc  `json:"function"`
}

type openaiToolCallFunc struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type openaiResponse struct {
	Choices []struct {
		Message struct {
			Role      string           `json:"role"`
			Content   string           `json:"content"`
			ToolCalls []openaiToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens         int `json:"prompt_tokens"`
		CompletionTokens     int `json:"completion_tokens"`
		TotalTokens          int `json:"total_tokens"`
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

func marshalMessages(msgs []Message) []openaiMessage {
	out := make([]openaiMessage, len(msgs))
	for i, m := range msgs {
		out[i] = openaiMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		}
		if len(m.ToolCalls) > 0 {
			for _, c := range m.ToolCalls {
				// OpenAI's /v1/chat/completions requires tool_call arguments
				// to be a JSON-encoded *string* when echoing assistant turns
				// back to the model — not a raw object. Double-encode:
				// map → JSON bytes → JSON-encoded string.
				argsObj, _ := json.Marshal(c.Arguments)
				argsStr, _ := json.Marshal(string(argsObj))
				out[i].ToolCalls = append(out[i].ToolCalls, openaiToolCall{
					ID:   c.ID,
					Type: "function",
					Function: openaiToolCallFunc{
						Name:      c.Name,
						Arguments: argsStr,
					},
				})
			}
		}
	}
	return out
}

func marshalTools(tools []Tool) []openaiTool {
	out := make([]openaiTool, len(tools))
	for i, t := range tools {
		out[i] = openaiTool{
			Type: "function",
			Function: openaiFunDef{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		}
	}
	return out
}

// decodeToolCalls handles the string-OR-object arguments variance per spec §2.
func decodeToolCalls(raw []openaiToolCall) ([]ToolCall, error) {
	out := make([]ToolCall, 0, len(raw))
	for _, tc := range raw {
		args, rawStr, err := decodeArguments(tc.Function.Arguments)
		if err != nil {
			return nil, fmt.Errorf("tool_call %s arguments: %w", tc.Function.Name, err)
		}
		out = append(out, ToolCall{
			ID:           tc.ID,
			Name:         tc.Function.Name,
			Arguments:    args,
			RawArguments: rawStr,
		})
	}
	return out, nil
}

// decodeArguments parses either a JSON object (`{"a": 1}`), or a JSON string
// that itself contains JSON (`"{\"a\": 1}"`), or a plain string fallback.
func decodeArguments(raw json.RawMessage) (map[string]any, string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}, "", nil
	}
	// First try object directly.
	var direct map[string]any
	if err := json.Unmarshal(raw, &direct); err == nil {
		return direct, string(raw), nil
	}
	// Try string-wrapped JSON.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		// s is a plain string. Try to parse as JSON object.
		if s == "" {
			return map[string]any{}, "", nil
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(s), &obj); err == nil {
			return obj, s, nil
		}
		// Not a JSON object — keep raw string, empty args map.
		return map[string]any{}, s, nil
	}
	return nil, string(raw), fmt.Errorf("arguments neither object nor string: %s", truncate(string(raw), 128))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ResolveRealModel probes the endpoint's {origin}/v1/models list and returns
// the backing model id for `forModel` (the virtual model the benchmark is
// targeting). Used to populate manifest.resolvedRealModel so cross-run
// comparisons know what was actually measured (the virtual model name is a
// proxy-side routing artefact).
//
// The probe looks for an entry whose `id` matches `forModel`, then prefers
// its `root` field (the backing model) over its `id`. If no entry matches
// — e.g. because the endpoint's /v1/models doesn't know about the virtual,
// or returns an atypical shape — falls back to `data[0]` so we still get
// something, or to "unknown".
//
// Returns the literal string "unknown" on any failure (transport, non-2xx,
// malformed body, empty data array) and logs a single-line warning to
// stderr. Never a fatal error for the caller — per v2 plan principle #4.
//
// Context deadline: 5s regardless of the caller's timeout, so a slow
// probe can't hold up a benchmark run.
func (a *OpenAIChat) ResolveRealModel(ctx context.Context, forModel string) string {
	origin, ok := endpointOrigin(a.Endpoint)
	if !ok {
		fmt.Fprintf(os.Stderr, "warn: cannot derive origin from endpoint %q; resolvedRealModel=unknown\n", a.Endpoint)
		return "unknown"
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, origin+"/v1/models", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: /v1/models probe request: %v; resolvedRealModel=unknown\n", err)
		return "unknown"
	}
	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: /v1/models probe: %v; resolvedRealModel=unknown\n", err)
		return "unknown"
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		fmt.Fprintf(os.Stderr, "warn: /v1/models probe http %d; resolvedRealModel=unknown\n", resp.StatusCode)
		return "unknown"
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: /v1/models probe read: %v; resolvedRealModel=unknown\n", err)
		return "unknown"
	}
	var parsed struct {
		Data []struct {
			ID   string `json:"id"`
			Root string `json:"root"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || len(parsed.Data) == 0 {
		fmt.Fprintf(os.Stderr, "warn: /v1/models probe parse (body=%s); resolvedRealModel=unknown\n", truncate(string(body), 120))
		return "unknown"
	}
	pick := func(e struct {
		ID   string `json:"id"`
		Root string `json:"root"`
	}) string {
		if e.Root != "" {
			return e.Root
		}
		return e.ID
	}
	// Prefer an entry whose id matches the model we asked about, but only
	// if forModel was actually supplied (empty forModel → treat as "no
	// filter", use data[0]).
	if forModel != "" {
		for _, e := range parsed.Data {
			if e.ID == forModel {
				if got := pick(e); got != "" {
					return got
				}
			}
		}
		fmt.Fprintf(os.Stderr, "warn: /v1/models did not list %q; falling back to data[0]\n", forModel)
	}
	if got := pick(parsed.Data[0]); got != "" {
		return got
	}
	return "unknown"
}

// endpointOrigin strips the /v1/chat/completions (or similar path) from
// the full endpoint URL so /v1/models can be appended. Falls back to the
// full endpoint if parsing fails, which is always safe — the probe will
// then 404 and we return "unknown".
func endpointOrigin(endpoint string) (string, bool) {
	// Simplest reliable split: find the last "/v1/" and strip from there.
	idx := strings.Index(endpoint, "/v1/")
	if idx < 0 {
		return endpoint, true
	}
	return endpoint[:idx], true
}
