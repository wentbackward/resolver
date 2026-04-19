// Package adapter abstracts over LLM chat endpoints. v1 ships openai-chat
// only; anthropic / openclaw / hf-serverless are explicit v2.
//
// The plan describes the adapter contract as
//
//	Run(ctx context.Context, s *scenario.Scenario, opts RunOpts) (*RunResult, error)
//
// The v1 implementation keeps that high-level contract available on the
// concrete adapter types, but the interface itself is Turn-level (Chat). That
// lets the runner orchestrate multi-turn conversations while the adapter
// stays a pure HTTP shim — which simplifies adding the v2 adapters
// (openclaw's agent loop does its own turn management).
package adapter

import (
	"context"
	"time"
)

// Adapter is a turn-level chat abstraction. v1 sole implementation is
// OpenAIChat.
type Adapter interface {
	// Chat issues a single chat completion. Implementations must honor
	// temperature=0, the request timeout, and tool_choice=auto.
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)

	// Name identifies the adapter for manifest reporting.
	Name() string
}

// ChatRequest is the provider-neutral request shape (v1 = openai-compat).
type ChatRequest struct {
	Model       string
	Messages    []Message
	Tools       []Tool
	Temperature float64
	MaxTokens   int
	APIKey      string
	Timeout     time.Duration
}

// ChatResponse is the provider-neutral response shape.
type ChatResponse struct {
	Content   string
	ToolCalls []ToolCall
	Usage     Usage
	// ElapsedMs is wall-clock for the round-trip (request issue to full
	// response received).
	ElapsedMs int64
	// TTFTMs is time to first token when streaming; for non-streamed
	// endpoints, equals ElapsedMs.
	TTFTMs int64
}

// Message is a single conversation message. Tool responses use Role="tool"
// and ToolCallID to correlate with a prior tool call.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall is one function invocation. Arguments is the parsed JSON object
// (or whatever the fallback parser could extract from text).
//
// ID and RawArguments are kept for debugging/replay but not serialized into
// the scorecard — spec §7 only lists `name` and `arguments` per tool call,
// so omitting them preserves byte-exact golden parity.
type ToolCall struct {
	ID        string         `json:"-"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`

	// RawArguments preserves the literal string form for debugging.
	RawArguments string `json:"-"`
}

// Tool is a function definition sent to the model.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Usage accumulates token counts (best-effort — some endpoints don't report
// cached tokens).
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	CachedTokens     int
}
