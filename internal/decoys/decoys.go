// Package decoys generates plausible-but-irrelevant tool definitions for
// Sweep A. Decoys MUST NOT satisfy any Tier 1 regex — that's the whole
// point of the sweep: they should tempt the model but never be the correct
// answer on any canonical query.
package decoys

import (
	"fmt"
	"math/rand"

	"github.com/gresham/resolver/internal/scenario"
)

// domains are four semantically distinct categories so the decoys cover
// more than just infra. Expanding to 10 words per category with 10
// operations each = 400 decoys per category minimum.
var domains = []struct {
	name  string
	nouns []string
	verbs []string
}{
	{
		name:  "marketing",
		nouns: []string{"campaign", "audience", "segment", "funnel", "subscriber", "email", "sms", "ad_group", "keyword", "landing_page"},
		verbs: []string{"schedule", "launch", "pause", "archive", "report", "export", "import", "split_test", "target", "unsubscribe"},
	},
	{
		name:  "finance",
		nouns: []string{"invoice", "ledger", "expense", "payout", "refund", "tax", "budget", "quote", "receipt", "reconciliation"},
		verbs: []string{"post", "void", "approve", "reject", "flag", "audit", "reconcile", "issue", "adjust", "close_period"},
	},
	{
		name:  "hr",
		nouns: []string{"candidate", "offer", "onboarding", "timesheet", "pto", "review", "benefit", "roster", "policy", "training"},
		verbs: []string{"submit", "approve", "deny", "assign", "remind", "archive", "export", "escalate_hr", "schedule_review", "close"},
	},
	{
		name:  "crm",
		nouns: []string{"lead", "opportunity", "contact", "account", "task", "call", "deal", "stage", "pipeline", "note"},
		verbs: []string{"create", "qualify", "convert", "merge", "deprioritize", "reassign", "lookup", "tag", "snooze", "enrich"},
	},
}

// Generate returns n decoy tool definitions deterministic per seed. They
// never collide with the 5 resolver tool names (exec, health_check,
// graph_query, escalate, refuse) because the verb/noun vocabulary is
// disjoint.
func Generate(n int, seed int64) []scenario.ToolDef {
	if n <= 0 {
		return nil
	}
	// deterministic: use math/rand.New with a seeded source
	r := rand.New(rand.NewSource(seed))
	names := allNames()
	r.Shuffle(len(names), func(i, j int) { names[i], names[j] = names[j], names[i] })
	if n > len(names) {
		n = len(names)
	}
	out := make([]scenario.ToolDef, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, scenario.ToolDef{
			Name:        names[i].name,
			Description: names[i].desc,
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "target identifier"},
				},
				"required": []string{"id"},
			},
		})
	}
	return out
}

// MaxN reports how many distinct decoys the generator can produce without
// collision. Useful for --axis validation.
func MaxN() int {
	n := 0
	for _, d := range domains {
		n += len(d.nouns) * len(d.verbs)
	}
	return n
}

type decoyRef struct {
	name, desc string
}

func allNames() []decoyRef {
	var out []decoyRef
	for _, d := range domains {
		for _, v := range d.verbs {
			for _, n := range d.nouns {
				out = append(out, decoyRef{
					name: fmt.Sprintf("%s_%s_%s", d.name, v, n),
					desc: fmt.Sprintf("%s_%s_%s: perform %s on a %s in the %s domain.", d.name, v, n, v, n, d.name),
				})
			}
		}
	}
	return out
}
