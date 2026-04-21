package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/wentbackward/resolver/internal/adapter"
	"github.com/wentbackward/resolver/internal/scenario"
	"github.com/wentbackward/resolver/internal/verdict"
)

// ExecuteOpts carries shared runtime configuration.
type ExecuteOpts struct {
	SystemPrompt string
	Tools        []scenario.ToolDef
	Model        string
	APIKey       string
	Timeout      time.Duration

	// Replayer, if non-nil, intercepts every call. Used for golden tests and
	// the --replay CLI flag.
	Replayer Replayer
}

// Replayer short-circuits the adapter with canned responses keyed on the
// scenario id.
type Replayer interface {
	Lookup(scenarioID string) (adapter.ChatResponse, bool)
}

// PerQuery is one scored query in the scorecard output. v2.1 adds Role
// alongside Tier — scenarios migrated to role-organised dirs carry Role;
// legacy unmigrated scenarios (none remain in v2.1 data/) carry Tier.
type PerQuery struct {
	Tier         scenario.Tier      `json:"tier,omitempty"`
	Role         scenario.Role      `json:"role,omitempty"`
	ID           string             `json:"id"`
	Query        string             `json:"query"`
	ExpectedTool string             `json:"expectedTool"`
	Score        verdict.Score      `json:"score"`
	Reason       string             `json:"reason"`
	ElapsedMs    int64              `json:"elapsedMs"`
	ToolCalls    []adapter.ToolCall `json:"toolCalls"`
	Content      any                `json:"content"`
}

// RunTier1 executes all Tier 1 scenarios serially per spec §2/§9.
func RunTier1(ctx context.Context, ad adapter.Adapter, scenarios []scenario.Scenario, opts ExecuteOpts) []PerQuery {
	out := make([]PerQuery, 0, len(scenarios))
	tools := toAdapterTools(opts.Tools)
	sysMsg := adapter.Message{Role: "system", Content: opts.SystemPrompt}
	for _, s := range scenarios {
		pq := PerQuery{
			Tier:         s.Tier,
			Role:         s.Role,
			ID:           s.ID,
			Query:        s.Query,
			ExpectedTool: s.ExpectedTool,
		}

		// Hard-fail guard: scenarios carrying fixtures/needle require a context-assembly
		// runner (v2.2). RunTier1 does not assemble context; refuse to execute silently-degraded.
		if s.Needle != nil || len(s.Fixtures) > 0 {
			pq.Score = verdict.ScoreError
			pq.Reason = "scenario declares fixtures/needle but role path does not assemble context (v2.2 carry-over)"
			out = append(out, pq)
			continue
		}

		var (
			resp adapter.ChatResponse
			err  error
		)
		if opts.Replayer != nil {
			if got, ok := opts.Replayer.Lookup(s.ID); ok {
				resp = got
			} else {
				err = fmt.Errorf("no replay entry for scenario %s", s.ID)
			}
			// Replay: use the captured elapsedMs so the scorecard is a pure
			// function of the replay file (byte-exact golden diffs work).
			pq.ElapsedMs = resp.ElapsedMs
		} else {
			start := time.Now()
			resp, err = ad.Chat(ctx, adapter.ChatRequest{
				Model:       opts.Model,
				Messages:    []adapter.Message{sysMsg, {Role: "user", Content: s.Query}},
				Tools:       tools,
				Temperature: 0,
				MaxTokens:   1024,
				APIKey:      opts.APIKey,
				Timeout:     opts.Timeout,
			})
			pq.ElapsedMs = time.Since(start).Milliseconds()
		}

		if err != nil {
			pq.Score = verdict.ScoreError
			pq.Reason = err.Error()
			pq.ToolCalls = []adapter.ToolCall{}
			pq.Content = jsonNullOr(resp.Content)
			out = append(out, pq)
			continue
		}

		calls := resp.ToolCalls
		if len(calls) == 0 && resp.Content != "" {
			calls = ParseFallbackToolCalls(resp.Content)
		}
		v := verdict.Evaluate(&s, calls, resp.Content)
		pq.Score = v.Score
		pq.Reason = v.Reason
		pq.ToolCalls = emptyIfNil(calls)
		pq.Content = jsonNullOr(resp.Content)
		out = append(out, pq)
	}
	return out
}

func toAdapterTools(ts []scenario.ToolDef) []adapter.Tool {
	out := make([]adapter.Tool, len(ts))
	for i, t := range ts {
		out[i] = adapter.Tool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		}
	}
	return out
}

func emptyIfNil(c []adapter.ToolCall) []adapter.ToolCall {
	if c == nil {
		return []adapter.ToolCall{}
	}
	return c
}

// jsonNullOr returns nil (serializes as JSON null) when content is empty, to
// match spec §7 shape where "content" is usually null.
func jsonNullOr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
