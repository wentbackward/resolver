package main

import (
	"testing"

	"github.com/wentbackward/resolver/internal/adapter"
	"github.com/wentbackward/resolver/internal/verdict"
)

// TestVerdictToAcc locks the gate-mapping: correct=1.0, partial=0.5, other=0.
func TestVerdictToAcc(t *testing.T) {
	cases := map[verdict.Score]float64{
		verdict.ScoreCorrect:   1.0,
		verdict.ScorePartial:   0.5,
		verdict.ScoreIncorrect: 0.0,
		verdict.ScoreError:     0.0,
	}
	for s, want := range cases {
		if got := verdictToAcc(s); got != want {
			t.Errorf("verdictToAcc(%s) = %v, want %v", s, got, want)
		}
	}
}

func TestParseAxisDefaults(t *testing.T) {
	// Defaults per spec plan.
	cases := []struct {
		kind string
		want []int
	}{
		{"tool-count", []int{5, 20, 50, 100, 300}},
		{"context-size", []int{5000, 40000, 80000, 120000, 200000}},
	}
	for _, tc := range cases {
		got := parseAxis("", tc.kind)
		if len(got) != len(tc.want) {
			t.Errorf("%s default axis: got %v want %v", tc.kind, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s[%d]: got %d want %d", tc.kind, i, got[i], tc.want[i])
			}
		}
	}
}

func TestParseAxisOverride(t *testing.T) {
	got := parseAxis("5,10,15", "tool-count")
	want := []int{5, 10, 15}
	if len(got) != len(want) {
		t.Fatalf("len %d != %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("axis[%d]: got %d want %d", i, got[i], want[i])
		}
	}
}

func TestReplayLookupRoundtrip(t *testing.T) {
	r := &replayer{entries: map[string]adapter.ChatResponse{
		"T1.1": {
			Content: "",
			ToolCalls: []adapter.ToolCall{
				{Name: "exec", Arguments: map[string]any{"node": "spark-01", "command": "docker restart vllm-35b"}},
			},
		},
	}}
	got, ok := r.Lookup("T1.1")
	if !ok {
		t.Fatal("expected hit")
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].Name != "exec" {
		t.Errorf("unexpected replay result: %+v", got)
	}
	if _, ok := r.Lookup("nope"); ok {
		t.Error("expected miss on unknown id")
	}
}

func TestEnvOrPrecedence(t *testing.T) {
	t.Setenv("ARBITRARY_VAR", "fromEnv")
	if got := envOr("ARBITRARY_VAR", "default"); got != "fromEnv" {
		t.Errorf("got %q want fromEnv", got)
	}
	if got := envOr("DEFINITELY_UNSET_VAR_123", "default"); got != "default" {
		t.Errorf("got %q want default", got)
	}
}
