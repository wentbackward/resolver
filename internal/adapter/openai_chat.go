package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
