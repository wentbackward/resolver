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

	// Classifier, if non-nil, is forwarded to verdict.Evaluate so that
	// ClassifierMatch matchers can call the local LLM. Nil means the
	// --no-classifier path: every ClassifierMatch arm is silently skipped.
	Classifier adapter.Adapter

	// DataDir is the base directory for resolving prompt_ref paths embedded
	// in ClassifierMatch matchers (e.g. cmd/resolver/data/). Defaults to "."
	// when empty.
	DataDir string
}

// Replayer short-circuits the adapter with canned responses keyed on the
// scenario id.
type Replayer interface {
	Lookup(scenarioID string) (adapter.ChatResponse, bool)
}

// ClassifierInputSnapshot pins the exact inputs to a classifier call so that
// post-hoc replay is bit-reproducible (OD-1 residual, B5). All four fields
// are required when a ClassifierMatch fires; the struct is absent (nil)
// when no classifier was involved in the verdict.
type ClassifierInputSnapshot struct {
	// ContentHash is sha256 of the MUT's content fed to the classifier.
	ContentHash string `json:"contentHash"`
	// PromptRef is the relative path to the matcher prompt file used.
	PromptRef string `json:"promptRef"`
	// PromptHash is sha256 of the prompt file contents at call time.
	PromptHash string `json:"promptHash"`
	// ClassifierParamsHash is sha256 of the canonical JSON of
	// {max_tokens, seed, temperature, top_p} as actually passed.
	ClassifierParamsHash string `json:"classifierParamsHash"`
}

// PerQuery is one scored query in the scorecard output. v2.1 adds Role
// alongside Tier — scenarios migrated to role-organised dirs carry Role;
// legacy unmigrated scenarios (none remain in v2.1 data/) carry Tier.
//
// B5 adds ClassifierScore/Reason/ElapsedMs/PromptRef twin-fields and the
// ClassifierInput snapshot. All classifier fields are omitempty so non-
// classifier runs produce identical scorecard JSON to pre-B5 outputs.
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

	// Classifier twin-fields (omitempty; absent when no ClassifierMatch fired).
	ClassifierScore     verdict.Score            `json:"classifierScore,omitempty"`
	ClassifierReason    string                   `json:"classifierReason,omitempty"`
	ClassifierElapsedMs int64                    `json:"classifierElapsedMs,omitempty"`
	ClassifierPromptRef string                   `json:"classifierPromptRef,omitempty"`
	ClassifierInput     *ClassifierInputSnapshot `json:"classifierInput,omitempty"`
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
		v := verdict.Evaluate(&s, calls, resp.Content, verdict.EvaluateOpts{
			Classifier: opts.Classifier,
			Ctx:        ctx,
			DataDir:    opts.DataDir,
		})
		pq.Score = v.Score
		pq.Reason = v.Reason
		pq.ToolCalls = emptyIfNil(calls)
		pq.Content = jsonNullOr(resp.Content)
		// Populate classifier twin-fields when a ClassifierMatch fired.
		if v.Classifier != nil {
			cm := v.Classifier
			pq.ClassifierScore = cm.Score
			pq.ClassifierReason = cm.Reason
			pq.ClassifierElapsedMs = cm.ElapsedMs
			pq.ClassifierPromptRef = cm.PromptRef
			pq.ClassifierInput = &ClassifierInputSnapshot{
				ContentHash:          cm.ContentHash,
				PromptRef:            cm.PromptRef,
				PromptHash:           cm.PromptHash,
				ClassifierParamsHash: cm.ClassifierParamsHash,
			}
		}
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
