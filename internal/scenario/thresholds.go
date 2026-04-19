package scenario

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// thresholdsFile is the on-disk shape of a gate-thresholds YAML.
type thresholdsFile struct {
	Thresholds []GatedCheck `yaml:"thresholds"`
}

// LoadGateThresholds parses a YAML file whose shape is:
//
//	thresholds:
//	  - label: "..."
//	    tiers: [T1, T2]
//	    threshold: 90
//
// Operators can override the harness defaults by supplying this file to
// `resolver --thresholds PATH`. The default set (labels matching spec §7)
// is embedded in the binary at `cmd/resolver/data/tier1/gate-thresholds.yaml`.
func LoadGateThresholds(path string) ([]GatedCheck, error) {
	raw, err := readCapped(path, maxYAMLBytes)
	if err != nil {
		return nil, fmt.Errorf("thresholds %s: %w", path, err)
	}
	return parseGateThresholds(raw)
}

// ParseGateThresholdsBytes decodes a YAML byte slice — used by the
// embedded-default loader + any test that wants to stay in memory.
func ParseGateThresholdsBytes(raw []byte) ([]GatedCheck, error) {
	return parseGateThresholds(raw)
}

func parseGateThresholds(raw []byte) ([]GatedCheck, error) {
	var f thresholdsFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("thresholds yaml: %w", err)
	}
	if len(f.Thresholds) == 0 {
		return nil, fmt.Errorf("thresholds yaml has no entries")
	}
	// Validation: each check needs a non-empty label, ≥1 tier, and a
	// threshold in [0, 100].
	for i, g := range f.Thresholds {
		if g.Label == "" {
			return nil, fmt.Errorf("threshold %d: label is required", i)
		}
		if len(g.Tiers) == 0 {
			return nil, fmt.Errorf("threshold %d (%s): tiers list is empty", i, g.Label)
		}
		if g.Threshold < 0 || g.Threshold > 100 {
			return nil, fmt.Errorf("threshold %d (%s): threshold %d outside [0,100]", i, g.Label, g.Threshold)
		}
	}
	return f.Thresholds, nil
}

// SetGatedTiers overrides the harness-default gated-check set. Package-
// level state because GatedTiers() has always been a package-level
// getter; an override path is cheaper than threading the list through
// every caller. Tests that need isolation should snapshot + restore.
//
// NOT safe for use under t.Parallel(): this mutates package-level state.
// Tests must snapshot the current value and call ResetGatedTiersToDefaults
// (or restore the snapshot) in a t.Cleanup handler.
func SetGatedTiers(checks []GatedCheck) {
	gatedTiersOverride = checks
}

// ResetGatedTiersToDefaults reverts any SetGatedTiers override back to
// the embedded defaults (the literal slice defined in GatedTiers).
//
// NOT safe for use under t.Parallel() — same package-state caveat as
// SetGatedTiers.
func ResetGatedTiersToDefaults() {
	gatedTiersOverride = nil
}

var gatedTiersOverride []GatedCheck
