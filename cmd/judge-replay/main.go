// classify-replay re-runs judge verdicts from an archived scorecard
// against a new (or identical) matcher prompt, emitting a per-scenario diff
// report. Goal: demonstrate the replay capability is end-to-end functional
// without re-spending any MUT API budget.
//
// Usage:
//
//	classify-replay --scorecard results.json --new-prompt judge-prompts/safety-refusal.txt
//	classify-replay --scorecard results.json --new-prompt testdata/inverted-safety-refusal.txt \
//	    --endpoint http://localhost:11434/v1/chat/completions
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/wentbackward/resolver/internal/adapter"
	"github.com/wentbackward/resolver/internal/runner"
	"github.com/wentbackward/resolver/internal/verdict"
)

const classifyTimeout = 10 * time.Second

// DiffRow is one row of the replay diff report.
type DiffRow struct {
	ID         string
	OldVerdict verdict.Score
	NewVerdict verdict.Score
	Changed    bool
}

func main() {
	scorecardPath := flag.String("scorecard", "", "path to scorecard JSON (required)")
	newPromptPath := flag.String("new-prompt", "", "path to matcher prompt file (required; use {{output}} placeholder)")
	endpoint := flag.String("endpoint", "", "ollama endpoint (default: http://localhost:11434/v1/chat/completions)")
	flag.Parse()

	if *scorecardPath == "" || *newPromptPath == "" {
		fmt.Fprintln(os.Stderr, "usage: classify-replay --scorecard <path> --new-prompt <path> [--endpoint <url>]")
		os.Exit(2)
	}

	rows, err := run(*scorecardPath, *newPromptPath, *endpoint)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if len(rows) == 0 {
		fmt.Println("no judge entries found in scorecard (JudgeInput must be non-nil)")
		return
	}

	changed := 0
	for _, r := range rows {
		status := "SAME   "
		if r.Changed {
			status = "CHANGED"
			changed++
		}
		fmt.Printf("%-30s  old=%-12s  new=%-12s  %s\n",
			r.ID, string(r.OldVerdict), string(r.NewVerdict), status)
	}
	fmt.Printf("\n%d/%d verdict(s) changed\n", changed, len(rows))
}

// run executes the replay and returns one DiffRow per PerQuery entry that has
// a populated JudgeInput. Exported for use in tests.
func run(scorecardPath, newPromptPath, endpoint string) ([]DiffRow, error) {
	// ── 1. Load scorecard ────────────────────────────────────────────────────
	scorecardBytes, err := os.ReadFile(scorecardPath)
	if err != nil {
		return nil, fmt.Errorf("read scorecard: %w", err)
	}
	var sc struct {
		Results []runner.PerQuery `json:"results"`
	}
	if err := json.Unmarshal(scorecardBytes, &sc); err != nil {
		return nil, fmt.Errorf("parse scorecard: %w", err)
	}

	// ── 2. Load new prompt ───────────────────────────────────────────────────
	promptBytes, err := os.ReadFile(newPromptPath)
	if err != nil {
		return nil, fmt.Errorf("read prompt: %w", err)
	}
	promptTemplate := string(promptBytes)

	// ── 3. Create judge adapter ─────────────────────────────────────────
	cl := adapter.NewOllamaChat(endpoint)

	// ── 4. Replay each entry with a populated JudgeInput ────────────────
	var rows []DiffRow
	for _, pq := range sc.Results {
		if pq.JudgeInput == nil {
			continue // structural-only verdict; skip
		}
		content := extractContent(pq.Content)
		prompt := strings.ReplaceAll(promptTemplate, "{{output}}", content)

		ctx, cancel := context.WithTimeout(context.Background(), classifyTimeout)
		resp, callErr := cl.Chat(ctx, adapter.ChatRequest{
			Model:       "qwen2.5:3b",
			Messages:    []adapter.Message{{Role: "user", Content: prompt}},
			Temperature: 0,
			MaxTokens:   16,
			Timeout:     classifyTimeout,
		})
		cancel()

		newV := toVerdict(callErr, resp.Content)
		rows = append(rows, DiffRow{
			ID:         pq.ID,
			OldVerdict: pq.JudgeScore,
			NewVerdict: newV,
			Changed:    pq.JudgeScore != newV,
		})
	}
	return rows, nil
}

// toVerdict converts a judge HTTP response into a verdict.Score.
func toVerdict(err error, content string) verdict.Score {
	if err != nil {
		return verdict.ScoreError
	}
	switch strings.ToUpper(strings.TrimSpace(content)) {
	case "YES":
		return verdict.ScoreCorrect
	case "NO":
		return verdict.ScoreIncorrect
	default:
		return verdict.ScoreError
	}
}

// extractContent coerces the PerQuery.Content (typed as any) to a string.
// PerQuery.Content is either a plain string or nil (JSON null).
func extractContent(c any) string {
	if c == nil {
		return ""
	}
	if s, ok := c.(string); ok {
		return s
	}
	// Fallback: re-encode to JSON string (handles unexpected types gracefully).
	b, _ := json.Marshal(c)
	return string(b)
}
