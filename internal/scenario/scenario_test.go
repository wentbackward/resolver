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

// TestMatcherValidate_LabelIs_SingleKind verifies a scalar label_is is the
// only kind set and validates successfully.
func TestMatcherValidate_LabelIs_SingleKind(t *testing.T) {
	t.Parallel()
	label := "exec"
	m := Matcher{LabelIs: &label}
	if err := m.Validate(); err != nil {
		t.Fatalf("expected ok, got %v", err)
	}
}

// TestMatcherValidate_LabelIs_EmptyRejected guards against the "label_is:"
// (empty value) YAML authoring error.
func TestMatcherValidate_LabelIs_EmptyRejected(t *testing.T) {
	t.Parallel()
	empty := ""
	m := Matcher{LabelIs: &empty}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "label_is") {
		t.Fatalf("expected label_is error, got %v", err)
	}
}

// TestMatcherValidate_ParseValidJSON_True verifies the canonical true form
// validates.
func TestMatcherValidate_ParseValidJSON_True(t *testing.T) {
	t.Parallel()
	tru := true
	m := Matcher{ParseValidJSON: &tru}
	if err := m.Validate(); err != nil {
		t.Fatalf("expected ok, got %v", err)
	}
}

// TestMatcherValidate_ParseValidJSON_FalseRejected ensures explicit false is
// flagged as likely author error rather than silently accepted.
func TestMatcherValidate_ParseValidJSON_FalseRejected(t *testing.T) {
	t.Parallel()
	fls := false
	m := Matcher{ParseValidJSON: &fls}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "parse_valid_json") {
		t.Fatalf("expected parse_valid_json error, got %v", err)
	}
}

// TestMatcherValidate_JSONFieldPresent_Scalar verifies a scalar
// json_field_present validates as a single kind.
func TestMatcherValidate_JSONFieldPresent_Scalar(t *testing.T) {
	t.Parallel()
	field := "host"
	m := Matcher{JSONFieldPresent: &field}
	if err := m.Validate(); err != nil {
		t.Fatalf("expected ok, got %v", err)
	}
}

// TestMatcherValidate_RejectsTwoKinds_ParseValidAndLabelIs verifies the
// set != 1 invariant still fires for the new kinds.
func TestMatcherValidate_RejectsTwoKinds_ParseValidAndLabelIs(t *testing.T) {
	t.Parallel()
	tru := true
	label := "exec"
	m := Matcher{ParseValidJSON: &tru, LabelIs: &label}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "exactly one kind") {
		t.Fatalf("expected exactly one kind error, got %v", err)
	}
}

// TestScenario_ExpectedLabelField verifies ExpectedLabel is accepted as
// metadata without requiring a validator arm.
func TestScenario_ExpectedLabelField(t *testing.T) {
	t.Parallel()
	label := "exec"
	s := Scenario{
		ID:            "classifier-meta",
		Role:          RoleClassifier,
		Query:         "show disk usage",
		ExpectedLabel: "exec",
		Rule: Rule{
			CorrectIf: []Matcher{{LabelIs: &label}},
		},
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("expected ok with ExpectedLabel, got %v", err)
	}
	if s.ExpectedLabel != "exec" {
		t.Fatalf("ExpectedLabel not preserved, got %q", s.ExpectedLabel)
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
