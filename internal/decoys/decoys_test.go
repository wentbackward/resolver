package decoys_test

import (
	"strings"
	"testing"

	"github.com/gresham/resolver/internal/decoys"
)

func TestGenerateDeterministic(t *testing.T) {
	a := decoys.Generate(10, 42)
	b := decoys.Generate(10, 42)
	if len(a) != 10 || len(b) != 10 {
		t.Fatalf("got %d/%d decoys, want 10/10", len(a), len(b))
	}
	for i := range a {
		if a[i].Name != b[i].Name {
			t.Errorf("index %d: %q != %q — not deterministic by seed", i, a[i].Name, b[i].Name)
		}
	}
}

func TestDecoyNamesDontCollideWithRealTools(t *testing.T) {
	real := map[string]bool{"exec": true, "health_check": true, "graph_query": true, "escalate": true, "refuse": true}
	// Generate many decoys and assert none match a real tool name or any
	// regex the spec uses for Tier 1 verdicts.
	dec := decoys.Generate(decoys.MaxN(), 1)
	for _, d := range dec {
		if real[d.Name] {
			t.Errorf("decoy %q collides with real tool", d.Name)
		}
		if strings.Contains(d.Name, "nvidia-smi") || strings.Contains(d.Name, "docker") {
			t.Errorf("decoy %q contains substring that could match Tier 1 regex", d.Name)
		}
	}
}

func TestMaxNReasonable(t *testing.T) {
	if decoys.MaxN() < 400 {
		t.Errorf("MaxN = %d, want >= 400", decoys.MaxN())
	}
}
