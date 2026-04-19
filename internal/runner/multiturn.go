package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/wentbackward/resolver/internal/adapter"
	"github.com/wentbackward/resolver/internal/scenario"
	"github.com/wentbackward/resolver/internal/tokenizer"
	"github.com/wentbackward/resolver/internal/verdict"
)

// MultiTurnOpts extends ExecuteOpts with tier-2-specific knobs.
type MultiTurnOpts struct {
	ExecuteOpts
	// MaxTurns caps how many model turns a multi-turn scenario may take
	// before the runner gives up (prevents runaway loops).
	MaxTurns int
	// MockTools maps tool name → scripted response producer. When the model
	// calls a tool present in this registry, the runner injects the scripted
	// response; otherwise the runner emits a structured "unmocked tool"
	// error message.
	MockTools map[string]MockToolFunc
	// Tokenizer counts prompt/completion tokens when the endpoint doesn't
	// report them.
	Tokenizer tokenizer.Tokenizer
	// Fixtures provides scenario-declared fixture file lookup (used by
	// multi-turn mocks and Sweep B context assembly).
	Fixtures MockFixturesFS
}

// MockToolFunc produces the tool response body given the call arguments +
// scenario context. Returning "" means "no scripted response — fall back to
// a structured error".
type MockToolFunc func(call adapter.ToolCall, s *scenario.Scenario) string

// TurnMetric captures per-turn metrics for Tier 2 scenarios.
type TurnMetric struct {
	TurnIdx             int   `json:"turnIdx"`
	PromptTokens        int   `json:"promptTokens"`
	CompletionTokens    int   `json:"completionTokens"`
	CachedTokens        int   `json:"cachedTokens"`
	TTFTMs              int64 `json:"ttftMs"`
	TotalMs             int64 `json:"totalMs"`
	ContextWindowTokens int   `json:"contextWindowTokens"`
	ToolsCalledThisTurn int   `json:"toolsCalledThisTurn"`
}

// MultiTurnResult is the per-scenario record for Tier 2.
type MultiTurnResult struct {
	Tier      scenario.Tier      `json:"tier"`
	ID        string             `json:"id"`
	Score     verdict.Score      `json:"score"`
	Reason    string             `json:"reason"`
	ElapsedMs int64              `json:"elapsedMs"`
	Turns     []TurnMetric       `json:"turns"`
	ToolCalls []adapter.ToolCall `json:"toolCalls"`
	Content   string             `json:"content,omitempty"`
}

// RunMultiTurn executes a single multi-turn scenario. The runner holds the
// message history; the adapter only ever sees one HTTP call at a time.
func RunMultiTurn(ctx context.Context, ad adapter.Adapter, s *scenario.Scenario, opts MultiTurnOpts) MultiTurnResult {
	if opts.MaxTurns == 0 {
		opts.MaxTurns = 8
	}
	tok := opts.Tokenizer
	if tok == nil {
		tok = tokenizer.Default()
	}

	result := MultiTurnResult{Tier: s.Tier, ID: s.ID}

	// Seed message history with system prompt + the first user turn.
	msgs := []adapter.Message{{Role: "system", Content: opts.SystemPrompt}}
	userScript := firstUserScript(s)
	if userScript == "" {
		result.Score = verdict.ScoreError
		result.Reason = "scenario has no initial user turn"
		return result
	}
	msgs = append(msgs, adapter.Message{Role: "user", Content: userScript})

	tools := toAdapterTools(effectiveTools(s, opts.Tools))
	allCalls := []adapter.ToolCall{}
	var lastContent string
	runStart := time.Now()

	contextTokens := tok.Count(opts.SystemPrompt) + tok.Count(userScript)

	for turnIdx := 0; turnIdx < opts.MaxTurns; turnIdx++ {
		start := time.Now()
		resp, err := ad.Chat(ctx, adapter.ChatRequest{
			Model:       opts.Model,
			Messages:    msgs,
			Tools:       tools,
			Temperature: 0,
			MaxTokens:   1024,
			APIKey:      opts.APIKey,
			Timeout:     opts.Timeout,
		})
		if err != nil {
			result.Score = verdict.ScoreError
			result.Reason = err.Error()
			result.ElapsedMs = time.Since(runStart).Milliseconds()
			return result
		}
		// Record per-turn metrics.
		tm := TurnMetric{
			TurnIdx:          turnIdx,
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			CachedTokens:     resp.Usage.CachedTokens,
			TTFTMs:           resp.TTFTMs,
			TotalMs:          time.Since(start).Milliseconds(),
		}
		if tm.PromptTokens == 0 {
			tm.PromptTokens = contextTokens
		}
		if tm.CompletionTokens == 0 {
			tm.CompletionTokens = tok.Count(resp.Content)
		}
		tm.ToolsCalledThisTurn = len(resp.ToolCalls)
		tm.ContextWindowTokens = contextTokens + tm.CompletionTokens

		result.Turns = append(result.Turns, tm)
		allCalls = append(allCalls, resp.ToolCalls...)
		lastContent = resp.Content

		// Append the assistant turn to history.
		msgs = append(msgs, adapter.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})
		contextTokens = tm.ContextWindowTokens

		// If the model didn't call any tools, the turn is terminal.
		if len(resp.ToolCalls) == 0 {
			break
		}

		// Inject scripted tool responses.
		for _, tc := range resp.ToolCalls {
			var body string
			if opts.MockTools != nil {
				if f, ok := opts.MockTools[tc.Name]; ok {
					body = f(tc, s)
				}
			}
			if body == "" {
				body = fmt.Sprintf(`{"error":"tool %q not mocked in this scenario"}`, tc.Name)
			}
			msgs = append(msgs, adapter.Message{
				Role:       "tool",
				Name:       tc.Name,
				ToolCallID: tc.ID,
				Content:    body,
			})
			contextTokens += tok.Count(body)
		}
	}

	result.ElapsedMs = time.Since(runStart).Milliseconds()
	result.ToolCalls = allCalls
	result.Content = lastContent
	v := verdict.Evaluate(s, allCalls, lastContent)
	result.Score = v.Score
	result.Reason = v.Reason
	return result
}

// firstUserScript finds the first user-role turn and returns its content.
// Tier 1 single-turn scenarios use `.Query` — callers should convert that
// into a Turn before invoking RunMultiTurn (or, more commonly, use
// RunTier1).
func firstUserScript(s *scenario.Scenario) string {
	for _, t := range s.Turns {
		if t.Role == "user" {
			return t.Content
		}
	}
	return s.Query
}

// effectiveTools picks the scenario-specific tool list if set, else the
// default shared tools.
func effectiveTools(s *scenario.Scenario, shared []scenario.ToolDef) []scenario.ToolDef {
	if len(s.AvailableTools) > 0 {
		return s.AvailableTools
	}
	return shared
}
