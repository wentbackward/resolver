// Package-local community-benchmarks YAML loader + validator.
// Used by both the tagged (aggregate) and untagged builds so schema
// validation is available even without DuckDB.
package aggregate

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// CommunityBenchmark is one row in reports/community-benchmarks.yaml.
type CommunityBenchmark struct {
	Model     string  `yaml:"model"`
	Benchmark string  `yaml:"benchmark"`
	Metric    string  `yaml:"metric"`
	Value     float64 `yaml:"value"`
	SourceURL string  `yaml:"source_url"`
	AsOf      string  `yaml:"as_of"`
	Notes     string  `yaml:"notes,omitempty"`
}

type communityFile struct {
	Entries []CommunityBenchmark `yaml:"entries"`
}

// LoadCommunity reads and validates reports/community-benchmarks.yaml.
// Returns an error on missing required fields or on any `as_of` date in
// the future. A future `as_of` is always a bug (historical rows don't
// move); failing fast prevents polluting the DuckDB table with invalid
// provenance.
func LoadCommunity(path string) ([]CommunityBenchmark, error) {
	raw, err := readCapped(path, maxYAMLBytes)
	if err != nil {
		return nil, fmt.Errorf("community-benchmarks: %w", err)
	}
	var f communityFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("community-benchmarks yaml: %w", err)
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	for i, e := range f.Entries {
		if e.Model == "" {
			return nil, fmt.Errorf("entry %d: model is required", i)
		}
		if e.Benchmark == "" {
			return nil, fmt.Errorf("entry %d (%s): benchmark is required", i, e.Model)
		}
		if e.Metric == "" {
			return nil, fmt.Errorf("entry %d (%s/%s): metric is required", i, e.Model, e.Benchmark)
		}
		if e.SourceURL == "" {
			return nil, fmt.Errorf("entry %d (%s/%s/%s): source_url is required", i, e.Model, e.Benchmark, e.Metric)
		}
		d, perr := time.Parse("2006-01-02", e.AsOf)
		if perr != nil {
			return nil, fmt.Errorf("entry %d (%s/%s/%s): as_of must be YYYY-MM-DD, got %q (%w)", i, e.Model, e.Benchmark, e.Metric, e.AsOf, perr)
		}
		if d.After(today) {
			return nil, fmt.Errorf("entry %d (%s/%s/%s): as_of %s is in the future; historical rows don't move", i, e.Model, e.Benchmark, e.Metric, e.AsOf)
		}
	}
	return f.Entries, nil
}
