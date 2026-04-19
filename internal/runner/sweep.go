package runner

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gresham/resolver/internal/adapter"
	"github.com/gresham/resolver/internal/decoys"
	"github.com/gresham/resolver/internal/scenario"
	"github.com/gresham/resolver/internal/tokenizer"
	"github.com/gresham/resolver/internal/verdict"
)

// SweepKind enumerates the v1 sweeps.
type SweepKind string

const (
	SweepToolCount   SweepKind = "tool-count"
	SweepContextSize SweepKind = "context-size"
)

// SweepConfig defines a sweep run.
type SweepConfig struct {
	Kind      SweepKind
	Scenario  scenario.Scenario
	Axis      []int
	Seeds     int
	Parallel  bool
	Tokenizer tokenizer.Tokenizer
}

// SweepRow is one datapoint in the sweep CSV.
type SweepRow struct {
	AxisValue              int
	Seed                   int
	Score                  verdict.Score
	ToolsCalled            int
	WrongToolCount         int
	HallucinatedToolCount  int
	NeedleFound            bool
	Accuracy               float64
	ContextTokens          int
	ElapsedMs              int64
	Completed              bool
}

// RunSweep orchestrates an axis × seed grid.
func RunSweep(ctx context.Context, ad adapter.Adapter, cfg SweepConfig, opts MultiTurnOpts) []SweepRow {
	type job struct {
		axisValue int
		seed      int
	}
	var jobs []job
	for _, v := range cfg.Axis {
		for s := 0; s < cfg.Seeds; s++ {
			jobs = append(jobs, job{axisValue: v, seed: s})
		}
	}

	rows := make([]SweepRow, len(jobs))
	if cfg.Parallel {
		workers := runtime.NumCPU()
		if workers > len(jobs) {
			workers = len(jobs)
		}
		if workers < 1 {
			workers = 1
		}
		var wg sync.WaitGroup
		ch := make(chan int)
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for idx := range ch {
					rows[idx] = runOneSweepPoint(ctx, ad, cfg, opts, jobs[idx].axisValue, jobs[idx].seed)
				}
			}()
		}
		for i := range jobs {
			ch <- i
		}
		close(ch)
		wg.Wait()
	} else {
		for i, j := range jobs {
			rows[i] = runOneSweepPoint(ctx, ad, cfg, opts, j.axisValue, j.seed)
		}
	}
	return rows
}

func runOneSweepPoint(ctx context.Context, ad adapter.Adapter, cfg SweepConfig, opts MultiTurnOpts, axisValue, seed int) SweepRow {
	row := SweepRow{AxisValue: axisValue, Seed: seed}
	switch cfg.Kind {
	case SweepToolCount:
		return runToolCountPoint(ctx, ad, cfg, opts, axisValue, seed, row)
	case SweepContextSize:
		return runContextSizePoint(ctx, ad, cfg, opts, axisValue, seed, row)
	}
	row.Score = verdict.ScoreError
	return row
}

func runToolCountPoint(ctx context.Context, ad adapter.Adapter, cfg SweepConfig, opts MultiTurnOpts, n, seed int, row SweepRow) SweepRow {
	// Build tool list: 5 real + (n-5) decoys (or all real when n <= 5).
	realTools := opts.Tools
	tools := make([]scenario.ToolDef, 0, n)
	tools = append(tools, realTools...)
	if n > len(realTools) {
		deck := decoys.Generate(n-len(realTools), int64(seed)*1_000_003+1)
		tools = append(tools, deck...)
	} else if n < len(realTools) {
		tools = realTools[:n]
	}

	s := cfg.Scenario
	s.AvailableTools = tools

	start := time.Now()
	resp, err := ad.Chat(ctx, adapter.ChatRequest{
		Model:       opts.Model,
		Messages:    []adapter.Message{{Role: "system", Content: opts.SystemPrompt}, {Role: "user", Content: s.Query}},
		Tools:       toAdapterTools(tools),
		Temperature: 0,
		MaxTokens:   1024,
		APIKey:      opts.APIKey,
		Timeout:     opts.Timeout,
	})
	row.ElapsedMs = time.Since(start).Milliseconds()
	if err != nil {
		row.Score = verdict.ScoreError
		return row
	}
	row.Completed = true
	calls := resp.ToolCalls
	if len(calls) == 0 && resp.Content != "" {
		calls = ParseFallbackToolCalls(resp.Content)
	}
	row.ToolsCalled = len(calls)

	knownTools := map[string]bool{}
	for _, t := range tools {
		knownTools[t.Name] = true
	}
	realByName := map[string]bool{}
	for _, t := range realTools {
		realByName[t.Name] = true
	}
	for _, c := range calls {
		switch {
		case !knownTools[c.Name]:
			row.HallucinatedToolCount++
		case !realByName[c.Name]:
			// decoy tool call
			row.WrongToolCount++
		}
	}
	v := verdict.Evaluate(&s, calls, resp.Content)
	row.Score = v.Score
	return row
}

// runContextSizePoint assembles a context of ~n tokens by concatenating
// fixture snippets with a needle placed at the scenario's declared
// position, then asks the model the scenario query.
func runContextSizePoint(ctx context.Context, ad adapter.Adapter, cfg SweepConfig, opts MultiTurnOpts, n, seed int, row SweepRow) SweepRow {
	s := cfg.Scenario
	profile := s.ContextGrowthProfile
	if profile == "explosive" {
		row.Score = verdict.ScoreError
		return row
	}
	tok := cfg.Tokenizer
	if tok == nil {
		tok = tokenizer.Default()
	}

	// Assemble context body.
	body := assembleContextBody(s, n, seed, opts, tok)
	row.ContextTokens = tok.Count(body)

	userMsg := fmt.Sprintf("Reference material:\n\n%s\n\n%s", body, s.Query)
	start := time.Now()
	resp, err := ad.Chat(ctx, adapter.ChatRequest{
		Model:       opts.Model,
		Messages:    []adapter.Message{{Role: "system", Content: opts.SystemPrompt}, {Role: "user", Content: userMsg}},
		Tools:       toAdapterTools(opts.Tools),
		Temperature: 0,
		MaxTokens:   1024,
		APIKey:      opts.APIKey,
		Timeout:     opts.Timeout,
	})
	row.ElapsedMs = time.Since(start).Milliseconds()
	if err != nil {
		row.Score = verdict.ScoreError
		return row
	}
	row.Completed = true
	calls := resp.ToolCalls
	if len(calls) == 0 && resp.Content != "" {
		calls = ParseFallbackToolCalls(resp.Content)
	}
	row.ToolsCalled = len(calls)

	// Needle verdict: match in final message or any tool arg (case-insensitive).
	if s.Needle != nil {
		found := false
		if iMatch(s.Needle.MatchRegex, resp.Content) {
			found = true
		} else {
			for _, c := range calls {
				joined := joinArgsRaw(c)
				if iMatch(s.Needle.MatchRegex, joined) {
					found = true
					break
				}
			}
		}
		row.NeedleFound = found
	}

	v := verdict.Evaluate(&s, calls, resp.Content)
	row.Score = v.Score
	switch v.Score {
	case verdict.ScoreCorrect:
		row.Accuracy = 1.0
	case verdict.ScorePartial:
		row.Accuracy = 0.5
	}
	return row
}

// assembleContextBody returns a string whose token count is approximately n.
// Uses fixtures the scenario declared. Needle is planted at the scenario's
// declared Position (index into the assembled chunk list).
func assembleContextBody(s scenario.Scenario, targetTokens, seed int, opts MultiTurnOpts, tok tokenizer.Tokenizer) string {
	// Gather fixtures' content.
	if opts.Fixtures == nil || len(s.Fixtures) == 0 {
		// Degenerate fallback: repeat a lorem-ipsum-ish filler sized to the target.
		return fillerTokens(targetTokens, seed)
	}
	var chunks []string
	for _, fid := range s.Fixtures {
		data, err := opts.Fixtures.ReadFixture("docs/" + fid)
		if err != nil {
			continue
		}
		chunks = append(chunks, stripFrontmatter(string(data)))
	}
	// Pad / trim to hit token budget.
	var assembled strings.Builder
	used := 0
	i := 0
	for used < targetTokens && len(chunks) > 0 {
		piece := chunks[i%len(chunks)]
		pt := tok.Count(piece)
		if used+pt > targetTokens && assembled.Len() > 0 {
			// Truncate a tail slice approximately.
			remaining := targetTokens - used
			piece = truncateToApproxTokens(piece, remaining, tok)
			assembled.WriteString(piece)
			used += tok.Count(piece)
			break
		}
		assembled.WriteString(piece)
		assembled.WriteString("\n\n")
		used += pt
		i++
		if i > 2000 {
			break
		}
	}
	out := assembled.String()
	// Plant the needle at the declared position (approx — split by double-newline).
	if s.Needle != nil && s.Needle.Content != "" {
		parts := strings.Split(out, "\n\n")
		pos := s.Needle.Position
		if pos < 0 {
			pos = 0
		}
		if pos > len(parts) {
			pos = len(parts)
		}
		parts = append(parts[:pos], append([]string{s.Needle.Content}, parts[pos:]...)...)
		out = strings.Join(parts, "\n\n")
	}
	return out
}

func fillerTokens(n, seed int) string {
	// 1 "word" ≈ 1.33 tokens, so write n/1.33 words.
	words := int(float64(n) / 1.33)
	var b strings.Builder
	b.Grow(words * 6)
	r := seed
	for i := 0; i < words; i++ {
		r = (r*1103515245 + 12345) & 0x7fffffff
		b.WriteString("lorem")
		b.WriteString(strconv.Itoa(r % 1000))
		b.WriteByte(' ')
	}
	return b.String()
}

func truncateToApproxTokens(s string, n int, tok tokenizer.Tokenizer) string {
	if tok.Count(s) <= n {
		return s
	}
	// Heuristic: trim to n*6 characters, good enough for English.
	want := n * 6
	if want >= len(s) {
		return s
	}
	return s[:want]
}

func joinArgsRaw(c adapter.ToolCall) string {
	var out []string
	for k, v := range c.Arguments {
		out = append(out, fmt.Sprintf("%s=%v", k, v))
	}
	return strings.Join(out, " ")
}

// iMatch is a trimmed local copy to avoid importing verdict internals.
// Case-insensitive regex match.
func iMatch(pattern, s string) bool {
	if pattern == "" {
		return false
	}
	compiled, err := regexpCompileCI(pattern)
	if err != nil {
		return false
	}
	return compiled.MatchString(s)
}
