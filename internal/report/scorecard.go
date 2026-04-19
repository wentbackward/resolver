// Package report emits the scorecard JSON per spec §7. Byte-exact shape is
// a core v1 acceptance criterion — Go-specific metadata lives in the sibling
// manifest.json, never in scorecard meta.
package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/wentbackward/resolver/internal/runner"
	"github.com/wentbackward/resolver/internal/scenario"
	"github.com/wentbackward/resolver/internal/verdict"
)

// Meta keys pinned to spec §7. `nodeVersion` retained as a literal key even
// though this is Go; value = runtime.Version() (e.g. "go1.22.7").
type Meta struct {
	Model       string `json:"model"`
	Endpoint    string `json:"endpoint"`
	Timestamp   string `json:"timestamp"`
	QueryCount  int    `json:"queryCount"`
	NodeVersion string `json:"nodeVersion"`
}

// Threshold is one row of summary.thresholds (the five gated checks).
type Threshold struct {
	Label     string `json:"label"`
	Pct       int    `json:"pct"`
	Threshold int    `json:"threshold"`
	Pass      bool   `json:"pass"`
}

// TierSummary is one entry in summary.tiers.
type TierSummary struct {
	Correct   int   `json:"correct"`
	Partial   int   `json:"partial"`
	Incorrect int   `json:"incorrect"`
	Errors    int   `json:"errors"`
	Total     int   `json:"total"`
	Pct       int   `json:"pct"`
	AvgMs     int64 `json:"avgMs"`
	P50Ms     int64 `json:"p50Ms"`
}

// Timing is summary.timing.
type Timing struct {
	TotalMs int64 `json:"totalMs"`
	AvgMs   int64 `json:"avgMs"`
	P50Ms   int64 `json:"p50Ms"`
	P95Ms   int64 `json:"p95Ms"`
	MaxMs   int64 `json:"maxMs"`
	Count   int   `json:"count"`
}

// Summary is the composite summary block.
//
// MarshalJSON is overridden so summary.tiers always emits keys in natural
// tier order (T1..T10) rather than Go map iteration order
// (alphabetically T1, T10, T2…). Byte-exact §7 shape is a v1 acceptance
// criterion; stable ordering is a precondition.
type Summary struct {
	Overall    string                        `json:"overall"`
	Thresholds []Threshold                   `json:"thresholds"`
	Tiers      map[scenario.Tier]TierSummary `json:"tiers"`
	Timing     Timing                        `json:"timing"`
}

// MarshalJSON emits summary.tiers with keys in scenario.AllTiers() order.
func (s Summary) MarshalJSON() ([]byte, error) {
	// Build a JSON object manually to control tier key ordering.
	var b bytes.Buffer
	b.WriteString(`{"overall":`)
	enc, err := json.Marshal(s.Overall)
	if err != nil {
		return nil, err
	}
	b.Write(enc)

	b.WriteString(`,"thresholds":`)
	enc, err = json.Marshal(s.Thresholds)
	if err != nil {
		return nil, err
	}
	b.Write(enc)

	b.WriteString(`,"tiers":{`)
	for i, t := range scenario.AllTiers() {
		if i > 0 {
			b.WriteString(",")
		}
		keyEnc, err := json.Marshal(string(t))
		if err != nil {
			return nil, err
		}
		b.Write(keyEnc)
		b.WriteString(":")
		valEnc, err := json.Marshal(s.Tiers[t])
		if err != nil {
			return nil, err
		}
		b.Write(valEnc)
	}
	b.WriteString(`},"timing":`)
	enc, err = json.Marshal(s.Timing)
	if err != nil {
		return nil, err
	}
	b.Write(enc)
	b.WriteString("}")
	return b.Bytes(), nil
}

// Scorecard is the full spec §7 shape.
type Scorecard struct {
	Meta    Meta              `json:"meta"`
	Summary Summary           `json:"summary"`
	Results []runner.PerQuery `json:"results"`
}

// Build composes a scorecard from per-query results. Overall is "PASS" iff
// every gated threshold (five per spec §6) passes.
func Build(meta Meta, results []runner.PerQuery) Scorecard {
	tiers := map[scenario.Tier]TierSummary{}
	tierSamples := map[scenario.Tier][]int64{}
	var allSamples []int64

	for _, r := range results {
		ts := tiers[r.Tier]
		ts.Total++
		switch r.Score {
		case verdict.ScoreCorrect:
			ts.Correct++
		case verdict.ScorePartial:
			ts.Partial++
		case verdict.ScoreIncorrect:
			ts.Incorrect++
		case verdict.ScoreError:
			ts.Errors++
		}
		tiers[r.Tier] = ts
		if r.Score != verdict.ScoreError {
			tierSamples[r.Tier] = append(tierSamples[r.Tier], r.ElapsedMs)
			allSamples = append(allSamples, r.ElapsedMs)
		}
	}

	// Ensure every declared tier is present in summary.tiers (spec §7
	// requires all 10 even when 0 scenarios encoded for a tier).
	for _, t := range scenario.AllTiers() {
		ts := tiers[t]
		ts.Pct = tierPct(ts)
		tt := runner.TierTimingOf(tierSamples[t])
		ts.AvgMs = tt.AvgMs
		ts.P50Ms = tt.P50Ms
		tiers[t] = ts
	}

	// Thresholds.
	var thr []Threshold
	allPass := true
	for _, gc := range scenario.GatedTiers() {
		pct := groupPct(tiers, gc.Tiers)
		pass := pct >= gc.Threshold
		if !pass {
			allPass = false
		}
		thr = append(thr, Threshold{Label: gc.Label, Pct: pct, Threshold: gc.Threshold, Pass: pass})
	}

	overall := "FAIL"
	if allPass {
		overall = "PASS"
	}
	// If every non-error timing is empty, skip P95/Max by leaving zero-valued.
	tAgg := runner.Timings(allSamples)
	return Scorecard{
		Meta: meta,
		Summary: Summary{
			Overall:    overall,
			Thresholds: thr,
			Tiers:      tiers,
			Timing: Timing{
				TotalMs: tAgg.TotalMs,
				AvgMs:   tAgg.AvgMs,
				P50Ms:   tAgg.P50Ms,
				P95Ms:   tAgg.P95Ms,
				MaxMs:   tAgg.MaxMs,
				Count:   tAgg.Count,
			},
		},
		Results: results,
	}
}

func tierPct(t TierSummary) int {
	if t.Total == 0 {
		return 0
	}
	score := float64(t.Correct) + 0.5*float64(t.Partial)
	return int(math.Round(score / float64(t.Total) * 100))
}

func groupPct(tiers map[scenario.Tier]TierSummary, group []scenario.Tier) int {
	var correct, partial, total float64
	for _, t := range group {
		ts := tiers[t]
		correct += float64(ts.Correct)
		partial += float64(ts.Partial)
		total += float64(ts.Total)
	}
	if total == 0 {
		return 0
	}
	return int(math.Round((correct + 0.5*partial) / total * 100))
}

// NewMeta builds a Meta using current Go runtime. Caller passes ts explicitly
// so tests can pin it.
func NewMeta(model, endpoint string, ts time.Time, queryCount int) Meta {
	return Meta{
		Model:       model,
		Endpoint:    endpoint,
		Timestamp:   scenario.ScorecardTimestamp(ts),
		QueryCount:  queryCount,
		NodeVersion: runtime.Version(),
	}
}

// Write serializes the scorecard to a file at
// {dir}/{modelSlug}_{iso}.json per spec §7. Returns the full path written.
func Write(sc Scorecard, dir string, ts time.Time) (string, error) {
	slug := scenario.ModelSlug(sc.Meta.Model)
	name := fmt.Sprintf("%s_%s.json", slug, scenario.FilenameTimestamp(ts))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	b, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
