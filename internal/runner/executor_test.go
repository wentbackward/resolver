package runner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/wentbackward/resolver/internal/runner"
	"github.com/wentbackward/resolver/internal/scenario"
	"github.com/wentbackward/resolver/internal/verdict"
)

// TestRunTier1_RejectsNeedleScenarios_WithoutContextAssembly verifies that
// RunTier1 hard-fails any scenario declaring fixtures/needle rather than
// silently dropping them (v2.1 bug class). The adapter argument is nil
// because the guard must fire before any model invocation.
func TestRunTier1_RejectsNeedleScenarios_WithoutContextAssembly(t *testing.T) {
	needle := &scenario.Needle{
		Position:   0,
		Content:    "secret needle fact",
		MatchRegex: "secret needle fact",
	}
	sc := scenario.Scenario{
		ID:       "lc.test.needle.1",
		Role:     scenario.RoleLongContext,
		Query:    "find the needle in the context",
		Fixtures: []string{"ctx-chunk-0.md"},
		Needle:   needle,
		Rule:     scenario.Rule{},
	}

	// nil adapter: the guard must fire before ad.Chat is ever called.
	results := runner.RunTier1(context.Background(), nil, []scenario.Scenario{sc}, runner.ExecuteOpts{})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	pq := results[0]

	if pq.Score != verdict.ScoreError {
		t.Errorf("score: got %q, want %q", pq.Score, verdict.ScoreError)
	}
	if !strings.Contains(pq.Reason, "fixtures/needle") {
		t.Errorf("reason should contain 'fixtures/needle'; got: %q", pq.Reason)
	}
	if !strings.Contains(pq.Reason, "v2.2 carry-over") {
		t.Errorf("reason should contain 'v2.2 carry-over'; got: %q", pq.Reason)
	}
}

// TestRunTier1_RejectsFixturesOnly covers the fixtures-only case (no needle).
func TestRunTier1_RejectsFixturesOnly(t *testing.T) {
	sc := scenario.Scenario{
		ID:       "lc.test.fixtures.1",
		Role:     scenario.RoleLongContext,
		Query:    "summarise the context",
		Fixtures: []string{"doc-a.md", "doc-b.md"},
		Rule:     scenario.Rule{},
	}

	results := runner.RunTier1(context.Background(), nil, []scenario.Scenario{sc}, runner.ExecuteOpts{})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	pq := results[0]
	if pq.Score != verdict.ScoreError {
		t.Errorf("score: got %q, want %q", pq.Score, verdict.ScoreError)
	}
	if !strings.Contains(pq.Reason, "fixtures/needle") {
		t.Errorf("reason should contain 'fixtures/needle'; got: %q", pq.Reason)
	}
}
