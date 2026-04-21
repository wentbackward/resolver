// Package report emits the scorecard JSON. v2.1 reorganises the top-level
// summary from tier-keyed (`summary.tiers{T1..T10}` + single `overall`) to
// role-keyed (`summary.roles{agentic-toolcall,…}` + per-role verdict +
// per-role derived metrics). Go-specific metadata still lives in the
// sibling manifest.json, never in scorecard meta.
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

// Threshold is one row of summary.thresholds. In v2.1 each gated role
// contributes one row; Label is synthesized from role + optional metric.
type Threshold struct {
	Label     string  `json:"label"`
	Pct       int     `json:"pct"`
	Threshold float64 `json:"threshold"`
	Pass      bool    `json:"pass"`
}

// RoleSummary is one entry in summary.roles — the v2.1 replacement for
// summary.tiers. Counters mirror the tier block so run-level totals are
// derivable either way (the aggregator reads counters preferentially
// from roles, falls back to tiers for archived v1/v2 scorecards).
//
// Threshold / ThresholdMet / Metric are pointer-typed so ungated
// (INFO-verdict) roles omit them from JSON, matching the aggregator's
// NULL-tolerant read path (role_scorecards.threshold nullable).
type RoleSummary struct {
	Verdict               string             `json:"verdict"` // PASS | FAIL | INFO (INFO = ungated role)
	ThresholdMet          *bool              `json:"thresholdMet,omitempty"`
	Threshold             *float64           `json:"threshold,omitempty"`
	Metric                string             `json:"metric,omitempty"` // e.g. "parse_validity" for metric-gated roles
	Metrics               map[string]float64 `json:"metrics"`
	ScenarioCountExpected int                `json:"scenarioCountExpected"`
	ScenarioCountObserved int                `json:"scenarioCountObserved"`
	Scenarios             map[string]string  `json:"scenarios"` // scenario id → "correct"|"partial"|"incorrect"|"error"
	Correct               int                `json:"correct"`
	Partial               int                `json:"partial"`
	Incorrect             int                `json:"incorrect"`
	Errors                int                `json:"errors"`
	Total                 int                `json:"total"`
	Pct                   int                `json:"pct"`
	AvgMs                 int64              `json:"avgMs"`
	P50Ms                 int64              `json:"p50Ms"`
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

// Summary is the composite summary block. v2.1 shape: thresholds, roles,
// timing. The pre-v2.1 `overall` and `tiers` fields are removed — the
// aggregator and analyzer read per-role verdicts from `roles{}`.
//
// MarshalJSON is overridden so summary.roles always emits keys in
// scenario.AllRoles() order rather than Go map iteration order. Roles
// with zero observed scenarios are omitted; byte-exact diffs across
// partial-role runs stay stable.
type Summary struct {
	Thresholds []Threshold                        `json:"thresholds"`
	Roles      map[scenario.Role]RoleSummary      `json:"roles"`
	Timing     Timing                             `json:"timing"`
}

// MarshalJSON emits summary keys in a stable order and summary.roles in
// scenario.AllRoles() order (observed roles only).
func (s Summary) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(`{"thresholds":`)
	enc, err := json.Marshal(s.Thresholds)
	if err != nil {
		return nil, err
	}
	b.Write(enc)

	b.WriteString(`,"roles":{`)
	first := true
	for _, r := range scenario.AllRoles() {
		rs, ok := s.Roles[r]
		if !ok {
			continue
		}
		if !first {
			b.WriteString(",")
		}
		first = false
		keyEnc, err := json.Marshal(string(r))
		if err != nil {
			return nil, err
		}
		b.Write(keyEnc)
		b.WriteString(":")
		valEnc, err := json.Marshal(rs)
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

// Scorecard is the full v2.1 scorecard shape.
type Scorecard struct {
	Meta    Meta              `json:"meta"`
	Summary Summary           `json:"summary"`
	Results []runner.PerQuery `json:"results"`
}

// Build composes a scorecard from per-query results grouped by role.
// Each role's verdict is derived against the gate-thresholds set; roles
// without a gate entry get Verdict "INFO" (informational, never fails CI).
func Build(meta Meta, results []runner.PerQuery) Scorecard {
	byRole := map[scenario.Role][]runner.PerQuery{}
	for _, r := range results {
		byRole[r.Role] = append(byRole[r.Role], r)
	}

	gateByRole := map[scenario.Role]scenario.GatedCheck{}
	for _, gc := range scenario.GatedTiers() {
		if gc.Role != "" {
			gateByRole[scenario.Role(gc.Role)] = gc
		}
	}

	roles := map[scenario.Role]RoleSummary{}
	var allSamples []int64

	for role, rs := range byRole {
		if role == "" {
			continue
		}
		var samples []int64
		rsum := RoleSummary{
			Metrics:               map[string]float64{},
			Scenarios:             map[string]string{},
			ScenarioCountExpected: len(rs),
			ScenarioCountObserved: len(rs),
		}
		var classifierCalls, classifierCorrect, classifierErrors int
		for _, q := range rs {
			rsum.Total++
			switch q.Score {
			case verdict.ScoreCorrect:
				rsum.Correct++
				rsum.Scenarios[q.ID] = "correct"
			case verdict.ScorePartial:
				rsum.Partial++
				rsum.Scenarios[q.ID] = "partial"
			case verdict.ScoreIncorrect:
				rsum.Incorrect++
				rsum.Scenarios[q.ID] = "incorrect"
			case verdict.ScoreError:
				rsum.Errors++
				rsum.Scenarios[q.ID] = "error"
			}
			if q.Score != verdict.ScoreError {
				samples = append(samples, q.ElapsedMs)
				allSamples = append(allSamples, q.ElapsedMs)
			}
			// Classifier twin-field tallies.
			if q.ClassifierScore != "" {
				classifierCalls++
				switch q.ClassifierScore {
				case verdict.ScoreCorrect:
					classifierCorrect++
				case verdict.ScoreError:
					classifierErrors++
				}
			}
		}
		rsum.Pct = rolePct(rsum)
		tt := runner.TierTimingOf(samples)
		rsum.AvgMs = tt.AvgMs
		rsum.P50Ms = tt.P50Ms

		// Common counters — emitted for every role (v2.1.1 Fix 3).
		// Before v2.1.1 only classifier and reducer-* roles populated
		// `metrics_json`; the 10 agentic roles shipped empty `{}`, which
		// broke downstream analyzer notebooks that expected `pct` on
		// every row. Uniform base shape keeps role_scorecards.metrics_json
		// non-empty for every role. Go's encoding/json marshals map keys
		// in sorted order, so the emitted JSON is deterministic without
		// an extra struct wrapper.
		rsum.Metrics["pct"] = float64(rsum.Pct)
		rsum.Metrics["correct"] = float64(rsum.Correct)
		rsum.Metrics["partial"] = float64(rsum.Partial)
		rsum.Metrics["incorrect"] = float64(rsum.Incorrect)
		rsum.Metrics["error"] = float64(rsum.Errors)
		rsum.Metrics["total"] = float64(rsum.Total)

		// Classifier twin-field counters. Only emitted when at least one
		// ClassifierMatch fired in this role so the JSON shape stays stable
		// for non-classifier runs.
		if classifierCalls > 0 {
			var classifierPct float64
			if classifierCalls > 0 {
				classifierPct = math.Round(float64(classifierCorrect)/float64(classifierCalls)*100)
			}
			rsum.Metrics["classifier_pct"] = classifierPct
			rsum.Metrics["classifier_correct"] = float64(classifierCorrect)
			rsum.Metrics["classifier_calls"] = float64(classifierCalls)
			rsum.Metrics["classifier_errors"] = float64(classifierErrors)
		}

		// Role-specific derived metrics (layered on top of the common
		// counters above).
		switch role {
		case scenario.RoleClassifier:
			rsum.Metrics["accuracy"] = safeDiv(rsum.Correct, rsum.Total)
		case scenario.RoleReducerJSON, scenario.RoleReducerSexp:
			// Phase-6 stopgap: until the verdict engine surfaces per-
			// matcher booleans, `parse_validity` proxies as the
			// correct-rate. Plan §12 wires the 5 named rates (parse_
			// validity, schema_validity, envelope_purity, locality_
			// compliance, status_correctness) once scenario matchers
			// emit them individually. Keeping the key stable now so
			// the gate-thresholds YAML doesn't need another churn.
			// (v2.1.1 R7: `parse_validity` and `pct` are both
			// correct/total for reducer roles — intentional, resolved
			// in v2.2 5-rate aggregation.)
			rsum.Metrics["parse_validity"] = safeDiv(rsum.Correct, rsum.Total)
		}

		// Apply the gate.
		if gc, ok := gateByRole[role]; ok {
			thr := gc.Threshold
			rsum.Threshold = &thr
			rsum.Metric = gc.Metric
			var met bool
			if gc.Metric != "" {
				val := rsum.Metrics[gc.Metric]
				met = val >= gc.Threshold
			} else {
				met = float64(rsum.Pct) >= gc.Threshold
			}
			rsum.ThresholdMet = &met
			if met {
				rsum.Verdict = "PASS"
			} else {
				rsum.Verdict = "FAIL"
			}
		} else {
			rsum.Verdict = "INFO"
		}

		roles[role] = rsum
	}

	// Threshold rows — synthesized in scenario.AllRoles() order so the
	// slice is stable across runs with different role coverage.
	var thr []Threshold
	for _, r := range scenario.AllRoles() {
		gc, ok := gateByRole[r]
		if !ok {
			continue
		}
		rs, ok := roles[r]
		if !ok {
			continue
		}
		pass := false
		if rs.ThresholdMet != nil {
			pass = *rs.ThresholdMet
		}
		thr = append(thr, Threshold{
			Label:     labelFor(gc),
			Pct:       rs.Pct,
			Threshold: gc.Threshold,
			Pass:      pass,
		})
	}

	tAgg := runner.Timings(allSamples)
	return Scorecard{
		Meta: meta,
		Summary: Summary{
			Thresholds: thr,
			Roles:      roles,
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

func rolePct(r RoleSummary) int {
	if r.Total == 0 {
		return 0
	}
	score := float64(r.Correct) + 0.5*float64(r.Partial)
	return int(math.Round(score / float64(r.Total) * 100))
}

func safeDiv(num, den int) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

func labelFor(gc scenario.GatedCheck) string {
	if gc.Metric != "" {
		return fmt.Sprintf("%s %s >= %.2f", gc.Role, gc.Metric, gc.Threshold)
	}
	return fmt.Sprintf("%s >= %g%%", gc.Role, gc.Threshold)
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
	return WriteWithSuffix(sc, dir, ts, "")
}

// WriteWithSuffix serializes to {dir}/{modelSlug}_{iso}{suffix}.json. Used
// by the reproducibility path (`-n N` repeated runs) to avoid clobbering
// previous scorecards — iterations past the first are written with
// `-rep{k}` suffix.
func WriteWithSuffix(sc Scorecard, dir string, ts time.Time, suffix string) (string, error) {
	slug := scenario.ModelSlug(sc.Meta.Model)
	name := fmt.Sprintf("%s_%s%s.json", slug, scenario.FilenameTimestamp(ts), suffix)
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

// AllRoleSummary returns a deterministic, observed-only slice of per-role
// summaries in AllRoles() order. Useful for pretty-printing and tests.
func AllRoleSummary(sc Scorecard) []struct {
	Role    scenario.Role
	Summary RoleSummary
} {
	out := make([]struct {
		Role    scenario.Role
		Summary RoleSummary
	}, 0, len(sc.Summary.Roles))
	for _, r := range scenario.AllRoles() {
		rs, ok := sc.Summary.Roles[r]
		if !ok {
			continue
		}
		out = append(out, struct {
			Role    scenario.Role
			Summary RoleSummary
		}{r, rs})
	}
	return out
}
