package scenario

import (
	"strings"
	"testing"
)

// TestScenarioValidateRoleOrTier covers the v2.1 tier-or-role dual-accept
// rule: a scenario must declare exactly one of Tier or Role. Both-set and
// neither-set must each fail with a descriptive error.
func TestScenarioValidateRoleOrTier(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		s        Scenario
		wantErr  bool
		wantText string
	}{
		{
			name: "tier only",
			s: Scenario{
				ID:    "tier-only",
				Tier:  TierT1,
				Query: "q",
			},
			wantErr: false,
		},
		{
			name: "role only",
			s: Scenario{
				ID:    "role-only",
				Role:  RoleAgenticToolcall,
				Query: "q",
			},
			wantErr: false,
		},
		{
			name: "both set",
			s: Scenario{
				ID:    "both",
				Tier:  TierT1,
				Role:  RoleAgenticToolcall,
				Query: "q",
			},
			wantErr:  true,
			wantText: "either tier or role, not both",
		},
		{
			name: "neither set",
			s: Scenario{
				ID:    "neither",
				Query: "q",
			},
			wantErr:  true,
			wantText: "missing tier or role",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.s.Validate()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantText)
				}
				if tc.wantText != "" && !strings.Contains(err.Error(), tc.wantText) {
					t.Fatalf("expected error containing %q, got %q", tc.wantText, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

// TestAllRolesContains13 guards against drift in the canonical role list.
func TestAllRolesContains13(t *testing.T) {
	t.Parallel()
	got := AllRoles()
	if len(got) != 13 {
		t.Fatalf("AllRoles len = %d, want 13", len(got))
	}
	seen := make(map[Role]bool, len(got))
	for _, r := range got {
		if seen[r] {
			t.Fatalf("AllRoles has duplicate %q", r)
		}
		seen[r] = true
		if string(r) == "" {
			t.Fatalf("AllRoles has empty entry")
		}
	}
}
