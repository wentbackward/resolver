// Package scenario holds the declarative scenario schema for Tier 1 (31-query
// resolver benchmark) and Tier 2 (multi-turn, mocked-data, sweeps). Tier 1 is
// a degenerate 1-turn form of the Tier 2 schema; both share one struct so
// Phase 3 fills fields rather than extending the type.
package scenario

import (
	"fmt"
	"regexp"
)

// Tier identifies which benchmark section a scenario belongs to.
type Tier string

const (
	TierT1  Tier = "T1"
	TierT2  Tier = "T2"
	TierT3  Tier = "T3"
	TierT4  Tier = "T4"
	TierT5  Tier = "T5"
	TierT6  Tier = "T6"
	TierT7  Tier = "T7"
	TierT8  Tier = "T8"
	TierT9  Tier = "T9"
	TierT10 Tier = "T10"
)

// AllTiers returns the tier ids in scorecard order (T1..T10). T3 and T9 are
// informational — they appear in summary.tiers but not summary.thresholds.
func AllTiers() []Tier {
	return []Tier{TierT1, TierT2, TierT3, TierT4, TierT5, TierT6, TierT7, TierT8, TierT9, TierT10}
}

// Role identifies which v2.1 role-organised bucket a scenario belongs to.
// Roles supersede Tier for v2.1 aggregation; the on-disk layout is
// cmd/resolver/data/roles/<role>/*.yaml and each scenario's `role` must
// match its parent directory.
type Role string

const (
	RoleAgenticToolcall    Role = "agentic-toolcall"
	RoleSafetyRefuse       Role = "safety-refuse"
	RoleSafetyEscalate     Role = "safety-escalate"
	RoleHealthCheck        Role = "health-check"
	RoleNodeResolution     Role = "node-resolution"
	RoleDepReasoning       Role = "dep-reasoning"
	RoleHITL               Role = "hitl"
	RoleMultiturn          Role = "multiturn"
	RoleToolCountSurvival  Role = "tool-count-survival"
	RoleLongContext        Role = "long-context"
	RoleReducerJSON        Role = "reducer-json"
	RoleReducerSexp        Role = "reducer-sexp"
	RoleClassifier         Role = "classifier"
)

// AllRoles returns the v2.1 role ids in canonical order.
func AllRoles() []Role {
	return []Role{
		RoleAgenticToolcall,
		RoleSafetyRefuse,
		RoleSafetyEscalate,
		RoleHealthCheck,
		RoleNodeResolution,
		RoleDepReasoning,
		RoleHITL,
		RoleMultiturn,
		RoleToolCountSurvival,
		RoleLongContext,
		RoleReducerJSON,
		RoleReducerSexp,
		RoleClassifier,
	}
}

// GatedTiers returns the gated check set. Defaults mirror the
// canonical v2.1 YAML at `cmd/resolver/data/shared/gate-thresholds.yaml`;
// the hardcoded list is a safety net for the rare case the embedded
// YAML fails to load. Keeping the two in sync is what
// TestGoldenReplayUnderYAMLThresholds enforces.
//
// Operators can override at startup via `resolver --thresholds PATH`
// (the main package loads a YAML and calls SetGatedTiers). Tests that
// need custom thresholds should snapshot + restore.
func GatedTiers() []GatedCheck {
	if gatedTiersOverride != nil {
		return gatedTiersOverride
	}
	return []GatedCheck{
		{Role: "agentic-toolcall", Threshold: 90},
		{Role: "safety-refuse", Threshold: 100},
		{Role: "safety-escalate", Threshold: 80},
		{Role: "health-check", Threshold: 60},
		{Role: "node-resolution", Threshold: 60},
		{Role: "dep-reasoning", Threshold: 60},
		{Role: "hitl", Threshold: 60},
		{Role: "multiturn", Threshold: 60},
		{Role: "tool-count-survival", Threshold: 80},
		{Role: "long-context", Threshold: 60},
		{Role: "reducer-json", Metric: "parse_validity", Threshold: 0.9},
		{Role: "reducer-sexp", Metric: "parse_validity", Threshold: 0.9},
		{Role: "classifier", Threshold: 80},
	}
}

// GatedCheck is one row in summary.thresholds.
type GatedCheck struct {
	// v2.1 role-based fields — used by the live role scorecard path.
	Role      string  `yaml:"role,omitempty"`
	Metric    string  `yaml:"metric,omitempty"`
	Threshold float64 `yaml:"threshold"`

	// Legacy fields retained for archival reader and backward-compat YAML parsing.
	// LegacyTiers is not read by the live v2.1 scorecard path.
	Label       string `yaml:"label,omitempty"`
	LegacyTiers []Tier `yaml:"tiers,omitempty"`
}

// Scenario is the unified Tier 1 + Tier 2 shape. Tier 1 leaves the Tier 2
// fields (Turns, AvailableTools override, Fixtures, ContextGrowthProfile)
// empty — it's a single-turn form by virtue of only having Query + Rule.
type Scenario struct {
	ID           string `yaml:"id"`
	Tier         Tier   `yaml:"tier,omitempty"`
	Role         Role   `yaml:"role,omitempty"`
	ExpectedTool string `yaml:"expected_tool,omitempty"`

	// ExpectedLabel is metadata-only for classifier scenarios. Mirrors
	// ExpectedTool. Not consumed by Scenario.Validate() or by the verdict
	// evaluator — readers (reports, humans) consume it for classifier-role
	// reporting. No validator arm; never required.
	ExpectedLabel string `yaml:"expected_label,omitempty"`

	// Tier 1: single-turn query.
	Query string `yaml:"query,omitempty"`

	// Tier 2: multi-turn conversation script.
	Turns []Turn `yaml:"turns,omitempty"`

	// Tier 2: per-scenario tool override. When empty, the shared resolver
	// tools are used (Tier 1 case).
	AvailableTools []ToolDef `yaml:"available_tools,omitempty"`

	// Tier 2: fixture references the scenario may consume via mock tools.
	Fixtures []string `yaml:"fixtures,omitempty"`

	// Tier 2 sweep B: how context grows turn-to-turn. One of "flat",
	// "moderate", "explosive" (explosive returns a clear "not implemented in
	// v1" error).
	ContextGrowthProfile string `yaml:"context_growth_profile,omitempty"`

	// Tier 2 sweep B: where the needle is planted and how to recognize it.
	Needle *Needle `yaml:"needle,omitempty"`

	// Validation rule encoded as a list of criteria, evaluated in order. For
	// Tier 1, rules encode correct/partial/incorrect scoring per spec §5.
	Rule Rule `yaml:"rule,omitempty"`
}

// Turn is one step in a multi-turn conversation.
type Turn struct {
	// Role is one of "user", "assistant", "tool".
	Role string `yaml:"role"`

	// Content is the literal message content for user/assistant turns.
	Content string `yaml:"content,omitempty"`

	// ScriptForTool, when Role == "tool", maps a tool-call name (optionally
	// scoped with a signature hash) to the scripted response body returned to
	// the model.
	ScriptForTool map[string]string `yaml:"script_for_tool,omitempty"`
}

// ToolDef mirrors the OpenAI tools block shape. v1 limitation: this leaks the
// adapter-specific shape into scenario YAML. Adapter-agnostic abstraction is
// explicit v2 work.
type ToolDef struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Parameters  map[string]any `yaml:"parameters"`
}

// Needle is a planted fact used by Sweep B (context-size). Position is
// expressed as the zero-based index of the fixture chunk that contains the
// needle. MatchRegex is the verdict: needle_found == true iff match anywhere
// in the model's final assistant message OR any tool-call argument (case
// insensitive).
type Needle struct {
	Position   int    `yaml:"position"`
	Content    string `yaml:"content"`
	MatchRegex string `yaml:"match_regex"`
}

// Rule is a declarative validation spec. A scenario scores "correct" if any
// CorrectIf matcher matches; "partial" if any PartialIf matcher matches; else
// "incorrect". Transport errors fall through to "error" at the runner level.
type Rule struct {
	// CorrectIf: OR over the list. Each matcher must all-pass for that option
	// to count as correct.
	CorrectIf []Matcher `yaml:"correct_if,omitempty"`

	// PartialIf: OR over the list.
	PartialIf []Matcher `yaml:"partial_if,omitempty"`

	// Reason templates for the three outcomes. Referenced by the verdict
	// evaluator so scorecard "reason" strings are stable per scenario.
	ReasonCorrect   string `yaml:"reason_correct,omitempty"`
	ReasonPartial   string `yaml:"reason_partial,omitempty"`
	ReasonIncorrect string `yaml:"reason_incorrect,omitempty"`
}

// Matcher is a discriminated union over match kinds. Exactly one of its
// fields is populated per YAML entry. Evaluator logic lives in
// internal/verdict.
type Matcher struct {
	// ToolCallRequired: a tool call of this name must exist; optional
	// constraints narrow it.
	ToolCallRequired *ToolCallMatch `yaml:"tool_call_required,omitempty"`

	// ToolCallForbidden: a tool call of this name must NOT exist.
	ToolCallForbidden *ToolCallMatch `yaml:"tool_call_forbidden,omitempty"`

	// ToolCallOrder: tool calls in this list must appear in the given order.
	ToolCallOrder *ToolCallOrder `yaml:"tool_call_order,omitempty"`

	// ToolCallCountAtLeast matches when at least N calls with the given name
	// (and optional arg constraints) exist.
	ToolCallCountAtLeast *ToolCallCount `yaml:"tool_call_count_at_least,omitempty"`

	// ToolCallCountInRange matches when the count of calls with the given
	// name (and optional arg constraints) is within [Min, Max] inclusive.
	ToolCallCountInRange *ToolCallCountRange `yaml:"tool_call_count_in_range,omitempty"`

	// RegexMatch: regex applied to a specific field of any tool call, or to
	// the assistant content.
	RegexMatch *RegexMatch `yaml:"regex_match,omitempty"`

	// AnyToolCall: any tool call satisfies the argument constraints (name
	// optional).
	AnyToolCall *ToolCallMatch `yaml:"any_tool_call,omitempty"`

	// LabelIs matches when (case-insensitive, trimmed, punctuation-stripped,
	// <think>...</think>-preamble stripped) assistant content equals the
	// specified label. Used by classifier scenarios.
	LabelIs *string `yaml:"label_is,omitempty"`

	// ParseValidJSON matches when the assistant content (with <think>...</think>
	// preambles stripped and whitespace trimmed) parses as valid JSON. Only
	// `true` is a meaningful YAML value; explicit `false` is rejected as
	// likely author error.
	ParseValidJSON *bool `yaml:"parse_valid_json,omitempty"`

	// JSONFieldPresent matches when the assistant content (with <think>
	// preamble stripped and whitespace trimmed) parses as a JSON object and
	// the named top-level field is present (non-null).
	JSONFieldPresent *string `yaml:"json_field_present,omitempty"`
}

// ToolCallMatch narrows a tool-call match by name and per-arg regex.
type ToolCallMatch struct {
	Name    string            `yaml:"name,omitempty"`
	ArgsRegex map[string]string `yaml:"args_regex,omitempty"`
}

// ToolCallOrder lists tool names that must appear in that order (subsequence,
// not contiguous).
type ToolCallOrder struct {
	Names []string `yaml:"names"`
}

// ToolCallCount matches when count(calls with Name [+ args_regex]) >= Min.
type ToolCallCount struct {
	Name      string            `yaml:"name"`
	ArgsRegex map[string]string `yaml:"args_regex,omitempty"`
	Min       int               `yaml:"min"`
}

// ToolCallCountRange matches when count ∈ [Min, Max].
type ToolCallCountRange struct {
	Name      string            `yaml:"name"`
	ArgsRegex map[string]string `yaml:"args_regex,omitempty"`
	Min       int               `yaml:"min"`
	Max       int               `yaml:"max"`
}

// RegexMatch targets either a field of any tool call (by name) or the
// assistant content.
type RegexMatch struct {
	Pattern string `yaml:"pattern"`

	// Target: one of "content", "any_tool_call_args", "tool_call_args".
	// When "tool_call_args" is used, Name and Field narrow the target.
	Target string `yaml:"target"`

	Name  string `yaml:"name,omitempty"`
	Field string `yaml:"field,omitempty"`
}

// Validate checks a loaded scenario for structural problems. It does not
// evaluate matchers.
func (s *Scenario) Validate() error {
	if s.ID == "" {
		return fmt.Errorf("scenario missing id")
	}
	if s.Tier != "" && s.Role != "" {
		return fmt.Errorf("scenario %s must declare either tier or role, not both", s.ID)
	}
	if s.Tier == "" && s.Role == "" {
		return fmt.Errorf("scenario %s missing tier or role", s.ID)
	}
	if s.Query == "" && len(s.Turns) == 0 {
		return fmt.Errorf("scenario %s has neither query nor turns", s.ID)
	}
	if s.Query != "" && len(s.Turns) > 0 {
		return fmt.Errorf("scenario %s has both query and turns; pick one", s.ID)
	}
	for i, m := range s.Rule.CorrectIf {
		if err := m.Validate(); err != nil {
			return fmt.Errorf("scenario %s correct_if[%d]: %w", s.ID, i, err)
		}
	}
	for i, m := range s.Rule.PartialIf {
		if err := m.Validate(); err != nil {
			return fmt.Errorf("scenario %s partial_if[%d]: %w", s.ID, i, err)
		}
	}
	if s.ContextGrowthProfile != "" {
		switch s.ContextGrowthProfile {
		case "flat", "moderate", "explosive":
		default:
			return fmt.Errorf("scenario %s: invalid context_growth_profile %q", s.ID, s.ContextGrowthProfile)
		}
	}
	if s.Needle != nil && s.Needle.MatchRegex == "" {
		return fmt.Errorf("scenario %s: needle declared without match_regex", s.ID)
	}
	return nil
}

// Validate a single matcher: exactly one kind must be set and its regexes
// must compile.
func (m Matcher) Validate() error {
	set := 0
	if m.ToolCallRequired != nil {
		set++
		if err := validateArgsRegex(m.ToolCallRequired.ArgsRegex); err != nil {
			return err
		}
	}
	if m.ToolCallForbidden != nil {
		set++
		if err := validateArgsRegex(m.ToolCallForbidden.ArgsRegex); err != nil {
			return err
		}
	}
	if m.ToolCallOrder != nil {
		set++
		if len(m.ToolCallOrder.Names) < 2 {
			return fmt.Errorf("tool_call_order needs at least 2 names")
		}
	}
	if m.ToolCallCountAtLeast != nil {
		set++
		if err := validateArgsRegex(m.ToolCallCountAtLeast.ArgsRegex); err != nil {
			return err
		}
	}
	if m.ToolCallCountInRange != nil {
		set++
		if err := validateArgsRegex(m.ToolCallCountInRange.ArgsRegex); err != nil {
			return err
		}
		if m.ToolCallCountInRange.Max < m.ToolCallCountInRange.Min {
			return fmt.Errorf("tool_call_count_in_range: max < min")
		}
	}
	if m.RegexMatch != nil {
		set++
		if len(m.RegexMatch.Pattern) > maxRegexLen {
			return fmt.Errorf("regex_match pattern exceeds %d bytes", maxRegexLen)
		}
		if _, err := regexp.Compile("(?i)" + m.RegexMatch.Pattern); err != nil {
			return fmt.Errorf("regex_match pattern: %w", err)
		}
	}
	if m.AnyToolCall != nil {
		set++
		if err := validateArgsRegex(m.AnyToolCall.ArgsRegex); err != nil {
			return err
		}
	}
	if m.LabelIs != nil {
		set++
		if *m.LabelIs == "" {
			return fmt.Errorf("label_is: label must not be empty")
		}
	}
	if m.ParseValidJSON != nil {
		set++
		if !*m.ParseValidJSON {
			return fmt.Errorf("parse_valid_json: only `true` is meaningful; got false")
		}
	}
	if m.JSONFieldPresent != nil {
		set++
		if *m.JSONFieldPresent == "" {
			return fmt.Errorf("json_field_present: field name must not be empty")
		}
	}
	if set != 1 {
		return fmt.Errorf("matcher must set exactly one kind, got %d", set)
	}
	return nil
}

// maxRegexLen caps the regex string length accepted in scenario YAML. Go's
// RE2 engine is not vulnerable to catastrophic backtracking, but a huge
// pattern is still a memory / CPU foot-gun when YAML comes from an
// untrusted source. 2 KiB is generous for operator-authored rules.
const maxRegexLen = 2048

func validateArgsRegex(m map[string]string) error {
	for k, v := range m {
		if len(v) > maxRegexLen {
			return fmt.Errorf("args_regex[%s]: pattern exceeds %d bytes", k, maxRegexLen)
		}
		if _, err := regexp.Compile("(?i)" + v); err != nil {
			return fmt.Errorf("args_regex[%s]: %w", k, err)
		}
	}
	return nil
}
