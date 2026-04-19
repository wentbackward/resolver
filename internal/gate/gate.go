// Package gate evaluates an operator-authored gate-policy YAML against a
// sweep result set. A gate rule is (metric, operator, threshold, axis
// filter). PASS iff every rule passes on every axis point it targets.
package gate

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Policy is the parsed YAML file.
type Policy struct {
	Description string `yaml:"description,omitempty"`
	Rules       []Rule `yaml:"rules"`
}

// Rule is one gate rule.
type Rule struct {
	// Metric: the CSV column name to apply the rule to (e.g. "accuracy",
	// "needle_found", "wrong_tool_count").
	Metric string `yaml:"metric"`

	// Operator: >=, <=, >, <, ==, !=.
	Operator string `yaml:"operator"`

	// Threshold: the value to compare against (float for accuracy, int for
	// counts, bool treated as 1/0).
	Threshold float64 `yaml:"threshold"`

	// Aggregate: how to reduce the rows matching AxisFilter into a single
	// number. One of "mean" (default), "min", "max", "count_true",
	// "count_false", "p50", "p95".
	Aggregate string `yaml:"aggregate,omitempty"`

	// AxisFilter narrows which rows this rule considers. Empty = all rows.
	// Supports simple bounds: {axis: tool_count, le: 20} → rows whose
	// tool_count ≤ 20.
	AxisFilter *AxisFilter `yaml:"axis_filter,omitempty"`

	// Label is a human-facing description used in reports.
	Label string `yaml:"label,omitempty"`
}

// AxisFilter narrows rows for a rule to a subset of the sweep grid.
type AxisFilter struct {
	Axis string   `yaml:"axis"`
	Eq   *float64 `yaml:"eq,omitempty"`
	Le   *float64 `yaml:"le,omitempty"`
	Lt   *float64 `yaml:"lt,omitempty"`
	Ge   *float64 `yaml:"ge,omitempty"`
	Gt   *float64 `yaml:"gt,omitempty"`
}

// Row is a single sweep result row, represented as a map column→value.
type Row map[string]float64

// Verdict is one gate evaluation outcome.
type Verdict struct {
	Rule      Rule    `json:"rule"`
	Observed  float64 `json:"observed"`
	Threshold float64 `json:"threshold"`
	Pass      bool    `json:"pass"`
	Note      string  `json:"note,omitempty"`
}

// Result is the composite outcome across all rules.
type Result struct {
	OverallPass bool      `json:"overallPass"`
	Verdicts    []Verdict `json:"verdicts"`
}

// Load parses a policy YAML file.
func Load(path string) (*Policy, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var p Policy
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("yaml %s: %w", path, err)
	}
	if len(p.Rules) == 0 {
		return nil, fmt.Errorf("%s: no rules defined", path)
	}
	return &p, nil
}

// Evaluate applies a policy to a result set.
func Evaluate(p *Policy, rows []Row) Result {
	r := Result{OverallPass: true}
	for _, rule := range p.Rules {
		v := evalRule(rule, rows)
		if !v.Pass {
			r.OverallPass = false
		}
		r.Verdicts = append(r.Verdicts, v)
	}
	return r
}

func evalRule(r Rule, rows []Row) Verdict {
	filtered := filter(rows, r.AxisFilter)
	if len(filtered) == 0 {
		return Verdict{Rule: r, Threshold: r.Threshold, Pass: false, Note: "no rows matched axis_filter"}
	}
	agg := r.Aggregate
	if agg == "" {
		agg = "mean"
	}
	obs := reduce(filtered, r.Metric, agg)
	pass := cmp(obs, r.Operator, r.Threshold)
	return Verdict{Rule: r, Observed: obs, Threshold: r.Threshold, Pass: pass}
}

func filter(rows []Row, f *AxisFilter) []Row {
	if f == nil || f.Axis == "" {
		return rows
	}
	var out []Row
	for _, row := range rows {
		v, ok := row[f.Axis]
		if !ok {
			continue
		}
		if f.Eq != nil && v != *f.Eq {
			continue
		}
		if f.Le != nil && v > *f.Le {
			continue
		}
		if f.Lt != nil && v >= *f.Lt {
			continue
		}
		if f.Ge != nil && v < *f.Ge {
			continue
		}
		if f.Gt != nil && v <= *f.Gt {
			continue
		}
		out = append(out, row)
	}
	return out
}

func reduce(rows []Row, metric, agg string) float64 {
	if len(rows) == 0 {
		return 0
	}
	values := make([]float64, 0, len(rows))
	for _, r := range rows {
		if v, ok := r[metric]; ok {
			values = append(values, v)
		}
	}
	if len(values) == 0 {
		return 0
	}
	switch agg {
	case "mean":
		var sum float64
		for _, v := range values {
			sum += v
		}
		return sum / float64(len(values))
	case "min":
		m := values[0]
		for _, v := range values[1:] {
			if v < m {
				m = v
			}
		}
		return m
	case "max":
		m := values[0]
		for _, v := range values[1:] {
			if v > m {
				m = v
			}
		}
		return m
	case "count_true":
		n := 0
		for _, v := range values {
			if v != 0 {
				n++
			}
		}
		return float64(n)
	case "count_false":
		n := 0
		for _, v := range values {
			if v == 0 {
				n++
			}
		}
		return float64(n)
	case "p50":
		return percentile(values, 50)
	case "p95":
		return percentile(values, 95)
	}
	return 0
}

func percentile(values []float64, p int) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j] < sorted[i] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	idx := p * len(sorted) / 100
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func cmp(a float64, op string, b float64) bool {
	switch op {
	case ">=":
		return a >= b
	case "<=":
		return a <= b
	case ">":
		return a > b
	case "<":
		return a < b
	case "==":
		return a == b
	case "!=":
		return a != b
	}
	return false
}
