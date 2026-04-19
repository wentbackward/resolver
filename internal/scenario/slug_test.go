package scenario_test

import (
	"regexp"
	"testing"
	"time"

	"github.com/wentbackward/resolver/internal/scenario"
)

func TestModelSlug(t *testing.T) {
	cases := map[string]string{
		"gresh-general":              "gresh-general",
		"Qwen/Qwen3.5-35B-A3B-FP8":   "Qwen_Qwen3.5-35B-A3B-FP8",
		"foo bar/baz.qux":            "foo_bar_baz.qux",
		"foo__bar":                   "foo_bar",
		"a/b/c/d":                    "a_b_c_d",
	}
	for in, want := range cases {
		if got := scenario.ModelSlug(in); got != want {
			t.Errorf("ModelSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFilenameTimestamp(t *testing.T) {
	ts := time.Date(2026, 4, 2, 14, 34, 56, 464_000_000, time.UTC)
	got := scenario.FilenameTimestamp(ts)
	want := "2026-04-02T14-34-56"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
	// Spec §7 filename regex check.
	re := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}$`)
	if !re.MatchString(got) {
		t.Errorf("doesn't match spec filename regex: %q", got)
	}
}

func TestScorecardTimestamp(t *testing.T) {
	ts := time.Date(2026, 4, 2, 14, 34, 56, 464_000_000, time.UTC)
	got := scenario.ScorecardTimestamp(ts)
	want := "2026-04-02T14:34:56.464Z"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestValidateScenario(t *testing.T) {
	cases := []struct {
		name    string
		s       scenario.Scenario
		wantErr bool
	}{
		{
			name:    "missing id",
			s:       scenario.Scenario{Tier: scenario.TierT1, Query: "x"},
			wantErr: true,
		},
		{
			name:    "missing tier",
			s:       scenario.Scenario{ID: "t", Query: "x"},
			wantErr: true,
		},
		{
			name:    "neither query nor turns",
			s:       scenario.Scenario{ID: "t", Tier: scenario.TierT1},
			wantErr: true,
		},
		{
			name: "ok single-turn",
			s:    scenario.Scenario{ID: "t", Tier: scenario.TierT1, Query: "x"},
		},
		{
			name: "ok multi-turn",
			s: scenario.Scenario{
				ID:    "t",
				Tier:  scenario.TierT2,
				Turns: []scenario.Turn{{Role: "user", Content: "hi"}},
			},
		},
		{
			name: "invalid growth profile",
			s: scenario.Scenario{
				ID:                   "t",
				Tier:                 scenario.TierT2,
				Query:                "x",
				ContextGrowthProfile: "bananas",
			},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.s.Validate()
			if tc.wantErr && err == nil {
				t.Errorf("want error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
