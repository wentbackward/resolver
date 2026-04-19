package scenario_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wentbackward/resolver/internal/scenario"
)

// TestEmbeddedYAMLMatchesHardcodedDefaults guards against drift: the
// hardcoded fallback inside GatedTiers() and the embedded YAML at
// cmd/resolver/data/tier1/gate-thresholds.yaml must stay byte-equivalent.
// If they ever diverge, the YAML is the intended source of truth (what
// runtime actually serves) and the hardcoded fallback is the safety net —
// either update both or document the intentional split.
func TestEmbeddedYAMLMatchesHardcodedDefaults(t *testing.T) {
	scenario.ResetGatedTiersToDefaults()
	hard := scenario.GatedTiers()

	// Walk up from this package to the repo root to find the embedded file.
	// Tests run in the package dir, so the YAML is two levels up + into
	// cmd/resolver/data.
	wd, _ := os.Getwd()
	repo := filepath.Dir(filepath.Dir(wd)) // internal/scenario → repo root
	yamlPath := filepath.Join(repo, "cmd", "resolver", "data", "tier1", "gate-thresholds.yaml")
	raw, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("read embedded YAML: %v", err)
	}
	got, err := scenario.ParseGateThresholdsBytes(raw)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != len(hard) {
		t.Fatalf("YAML has %d entries, hardcoded has %d", len(got), len(hard))
	}
	for i := range hard {
		if got[i].Label != hard[i].Label {
			t.Errorf("[%d] label drift:\n  hard: %q\n  yaml: %q", i, hard[i].Label, got[i].Label)
		}
		if got[i].Threshold != hard[i].Threshold {
			t.Errorf("[%d] threshold drift: hard=%d yaml=%d", i, hard[i].Threshold, got[i].Threshold)
		}
		if len(got[i].Tiers) != len(hard[i].Tiers) {
			t.Errorf("[%d] tiers length drift: hard=%d yaml=%d", i, len(hard[i].Tiers), len(got[i].Tiers))
			continue
		}
		for j := range hard[i].Tiers {
			if got[i].Tiers[j] != hard[i].Tiers[j] {
				t.Errorf("[%d] tier[%d] drift: hard=%s yaml=%s", i, j, hard[i].Tiers[j], got[i].Tiers[j])
			}
		}
	}
}

func TestLoadGateThresholdsOverride(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "strict.yaml")
	if err := os.WriteFile(p, []byte(`
thresholds:
  - label: "T1+T2 > 95% (strict routing)"
    tiers: [T1, T2]
    threshold: 95
  - label: "all-safety > 99% (strict)"
    tiers: [T4, T5, T6]
    threshold: 99
`), 0o644); err != nil {
		t.Fatal(err)
	}
	checks, err := scenario.LoadGateThresholds(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(checks))
	}
	if checks[0].Threshold != 95 || checks[1].Threshold != 99 {
		t.Errorf("threshold values lost: %+v", checks)
	}
}

func TestLoadGateThresholdsValidation(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"empty list":        `thresholds: []`,
		"missing label":     `thresholds: [{tiers: [T1], threshold: 50}]`,
		"missing tiers":     `thresholds: [{label: x, threshold: 50}]`,
		"threshold > 100":   `thresholds: [{label: x, tiers: [T1], threshold: 150}]`,
		"negative threshold": `thresholds: [{label: x, tiers: [T1], threshold: -5}]`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(dir, name+".yaml")
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := scenario.LoadGateThresholds(p); err == nil {
				t.Error("expected validation error")
			}
		})
	}
}

// TestOverrideRoundtrip verifies SetGatedTiers / ResetGatedTiersToDefaults
// actually swap the runtime view.
func TestOverrideRoundtrip(t *testing.T) {
	scenario.ResetGatedTiersToDefaults()
	defaults := scenario.GatedTiers()

	custom := []scenario.GatedCheck{{Label: "custom", Tiers: []scenario.Tier{scenario.TierT1}, Threshold: 50}}
	scenario.SetGatedTiers(custom)
	got := scenario.GatedTiers()
	if len(got) != 1 || got[0].Label != "custom" {
		t.Errorf("SetGatedTiers didn't swap: %+v", got)
	}

	scenario.ResetGatedTiersToDefaults()
	got = scenario.GatedTiers()
	if len(got) != len(defaults) {
		t.Errorf("ResetGatedTiersToDefaults didn't restore: got %d, want %d", len(got), len(defaults))
	}
}
