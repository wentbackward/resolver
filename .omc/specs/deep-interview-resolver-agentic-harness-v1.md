# Deep Interview Spec: Resolver Agentic Harness v1

## Metadata
- Interview ID: resolver-harness-2026-04-19
- Rounds: 7
- Final Ambiguity Score: 17%
- Type: brownfield (detailed planning docs exist; no code yet)
- Generated: 2026-04-19
- Threshold: 20%
- Status: PASSED

## Clarity Breakdown
| Dimension | Score | Weight | Weighted |
|---|---|---|---|
| Goal Clarity | 0.85 | 0.35 | 0.30 |
| Constraint Clarity | 0.88 | 0.25 | 0.22 |
| Success Criteria | 0.82 | 0.25 | 0.21 |
| Context Clarity | 0.70 | 0.15 | 0.11 |
| **Total Clarity** | | | **0.83** |
| **Ambiguity** | | | **0.17** |

## Goal

Build a framework-agnostic, single-binary Go test harness that (a) reimplements the existing 31-query resolver-validation benchmark per `RESOLVER-VALIDATION-SPEC.md`, and (b) extends it with progressive-context multi-turn scenarios, mocked data-producing tools, a tool-count sweep (A), and a context-size sweep (B) — so the user can identify each candidate LLM's useful operating envelope for agentic work. The harness targets OpenAI-compatible chat endpoints through the existing `llm-proxy`, defaults to the `gresh-general` virtual model (currently backed by Qwen/Qwen3.6-35B-A3B-FP8), and ships as a distribution-agnostic binary with example scheduling artifacts.

## Constraints

- **Language:** Go 1.22+. Single binary. Standard library first (`net/http`, `regexp`, `gopkg.in/yaml.v3`, `encoding/json`). `tiktoken-go` or equivalent for Qwen token counting.
- **Adapters:** `openai-chat` only for v1 (via llm-proxy). Anthropic, openclaw, and HF-serverless adapters are deferred to v2.
- **HITL flows:** Deferred to v2. v1 only exercises single-turn + scripted multi-turn with mocked data-producing tools.
- **Judge LLM:** Deferred to v2. All v1 verdicts are pattern-based (regex, tool-call presence/absence/order, argument matching).
- **Reference Node implementation:** Not accessible in this repo. Build from `RESOLVER-VALIDATION-SPEC.md` alone; drop PLAN.md Phase A.3 timing-parity verification. All other parity (scorecard JSON §7, exit codes §8, regex rules §5) is preserved.
- **Default endpoint:** `http://spark-01:4000/v1/chat/completions` (the llm-proxy).
- **Default model identifier:** `gresh-general` (virtual model routed by llm-proxy to Qwen/Qwen3.6-35B-A3B-FP8 on port 3040). Other virtual models (`gresh-huge`, etc.) work via `--model`.
- **Fixture corpus:** Hand-selected documents (not hand-written) form the v1 fixture pool for context-size sweep B. LLM-generated synthetic documents are allowed for scaling beyond the hand-curated sizes in v2 (seeded generator, deterministic).
- **Execution model:** Serial query execution (spec §9), `temperature=0`, 180s HTTP timeout. Sweeps may run parallel across seeds when `--parallel` is explicit; otherwise serial.
- **Run host:** Self-contained binary — user invokes on any machine. Example `systemd`/`cron` artifacts shipped in `contrib/` but harness has no host dependency beyond a reachable endpoint.
- **Reproducibility:** Every run writes a manifest with model name, resolved real-model (if available via llm-proxy meta), adapter config, scenario hashes, seed, and exact wall-clock timestamps.
- **Tokenization:** Qwen tokenizer bundled for accurate token counts against local models. Other models fall back to a word-based estimator with an explicit warning logged.

## Non-Goals

- **Not** a rewrite of or change to the existing Node reference implementation (which continues to live wherever it currently does; this harness is a parallel Go implementation).
- **Not** any framework besides openai-chat for v1. No anthropic/openclaw/hf-serverless adapters.
- **Not** LLM-as-judge verdicts in v1.
- **Not** HITL approval flows in v1.
- **Not** sweeps C (growth pattern), D (tool-diversity), or E (HITL frequency) in v1 — only A (tool count) and B (context size).
- **Not** a human-facing grading UI; CLI + files only.
- **Not** cross-adapter consistency checks (needs ≥2 adapters).
- **Not** fine-tuning, self-improvement, or lexical-scoping implementation — this harness only *accommodates* those downstream.

## Acceptance Criteria

### Tier 1 parity (regression baseline)

- [ ] All 31 queries from spec §5 encoded as YAML scenarios (one file per tier, entries per query).
- [ ] System prompt matches spec §3 verbatim (including topology) and is shared across all Tier 1 scenarios.
- [ ] Five tool definitions match spec §4 verbatim in OpenAI `tools` shape.
- [ ] Validation rules encode spec §5 regex rules; `correct`, `partial`, `incorrect`, `error` are the only scores; `partial = 0.5` in aggregation.
- [ ] Tool-call fallback parser extracts `functionName(arg="...", arg="...")` text patterns per spec §9; covered by a dedicated test case.
- [ ] Scorecard JSON output matches spec §7 shape exactly (`meta`, `summary`, `results`, including tier, id, query, expectedTool, score, reason, elapsedMs, toolCalls, content).
- [ ] Exit codes: `0` = all five gated thresholds pass; `1` = at least one gated threshold fails; `2` = uncaught error.
- [ ] Pass thresholds encoded per spec §6 (T1+T2 ≥90%, T4+T5+T6 ≥80%, T7 ≥60%, T8 ≥60%, T10 ≥60%). T3 and T9 reported as informational.
- [ ] Queries run strictly serially; wall-clock timings recorded per query; timing aggregates (total/avg/p50/p95/max/count) in scorecard `summary.timing`.

### Tier 2 — multi-turn + mocked data tools

- [ ] Multi-turn scenarios supported: a scenario specifies ≥1 user turn, plus scripted tool responses that the adapter returns when the agent calls them.
- [ ] Mocked data-producing tool types: `read_document`, `web_search`, `fetch_api` (at minimum). Each returns a scripted fixture document sized to the scenario's declared token budget.
- [ ] At least one multi-turn scenario ships that exercises context growth across 3+ turns.
- [ ] Per-turn metrics captured: `turn_idx`, `prompt_tokens`, `completion_tokens`, `cached_tokens` (when the endpoint reports them), `ttft_ms`, `total_ms`, `context_window_tokens`, `tools_called_this_turn`.

### Tier 2 — Sweep A (tool count)

- [ ] Harness can run the same core task with tool lists of size 5, 20, 50, 100, 300 (configurable).
- [ ] Decoy tools are plausible-but-irrelevant (generated from a template list); decoys never satisfy Tier 1 validation regexes.
- [ ] Per-model sweep output: CSV with columns `[tool_count, seed, score, tools_called, wrong_tool_count, hallucinated_tool_count, completed]`.
- [ ] Operator can declare pass thresholds (e.g. "maintain ≥80% tool-selection accuracy up to N=20") in a sweep-policy YAML; harness emits PASS/FAIL against those thresholds plus the full curve.

### Tier 2 — Sweep B (context size)

- [ ] Harness can run the same core task with context budgets of 5K, 40K, 80K, 120K, 200K tokens (configurable), assembling the context from mocked data-producing tool returns that consume from the hand-selected fixture pool.
- [ ] At least one needle-in-haystack question per sweep cell (fact buried in document N of M at a known position).
- [ ] Per-model sweep output: CSV with columns `[context_tokens, seed, needle_found, accuracy, elapsed_ms]`.
- [ ] Same pass-threshold mechanism as Sweep A (operator-declared policy → PASS/FAIL).

### General

- [ ] `resolver --endpoint URL --model NAME` CLI matches spec §8; additionally `--tier {1,2}`, `--scenario PATH`, `--sweep {tool-count,context-size}`, `-n N` for seed count, `--gate POLICY.yaml` for sweep thresholds.
- [ ] `$RESOLVER_ENDPOINT` and `$RESOLVER_MODEL` environment variables override defaults (below flag precedence).
- [ ] Reports written under `reports/results/` (Tier 1) and `reports/sweeps/` (Tier 2). Paths are gitignored.
- [ ] Every run writes a `manifest.json` capturing model name, adapter config, scenario hashes, seed, timestamps.
- [ ] README documents installation (`go install`), invocation examples, how to interpret scorecards, and the v1→v2 roadmap.
- [ ] `go test ./...` passes; at minimum: scenario-loader tests, fallback-parser tests, verdict-evaluator tests (per §5 regex rules), scorecard-shape golden test.

## Assumptions Exposed & Resolved

| Assumption | Challenge | Resolution |
|---|---|---|
| Existing Node impl is the anchor | Round 1: user confirmed full Go rewrite is wanted | PLAN.md path commits; Node impl left untouched |
| All four PLAN.md adapters must ship | Round 2: openai-chat is the minimum needed for v1 goal | Only openai-chat in v1; others deferred |
| "Comprehensive" means all 15 PLAN verifications | Round 3: user picked scoped v1 (sweeps A+B + multi-turn + mocked data) | HITL, judge-LLM, cross-adapter, sweeps C/D/E deferred |
| Fixtures must be hand-written for rigor | Round 4 (Contrarian): user picked hand-selected (not written) for v1, synthetic generator later | Curated pool for v1; seeded generator is v2 |
| Sweeps produce only curves | Round 5: user wanted configurable pass thresholds so sweeps can gate CI | Curves + `--gate policy.yaml` PASS/FAIL overlay |
| Harness must colocate with llm-proxy | Round 6 (Simplifier): user chose distribution-agnostic binary | Single binary + example systemd/cron; no host coupling |
| Reference Node impl is needed for parity | Round 7: spec is sole source of truth | Drop PLAN.md Phase A.3 timing-parity check; everything else preserved |
| Spec's Qwen3.5-35B-A3B-FP8 default model is the model to target | Round 7 note: llm-proxy exposes `gresh-general` backed by Qwen3.6-35B-A3B-FP8 | CLI default becomes `gresh-general`; spec updated in comments not in code |

## Technical Context

**Planning docs (already in repo):**
- `RESOLVER-VALIDATION-SPEC.md` — authoritative contract for Tier 1. Regex rules, system prompt, tool defs, scorecard shape, exit codes, CLI flags.
- `PLAN.md` — proposed architecture (adapters, scenarios, verdicts, runner, metrics, sweeps). v1 implements the subset described above.

**Sibling projects (neighbors, not dependencies):**
- `../llm-proxy/` — the endpoint this harness targets. Exposes `gresh-general` (Qwen3.6-35B-A3B-FP8 @ port 3040) and possibly other virtual models. No harness-side changes required there.
- `../sysadm/` and `../sysadm-v2/` — the production infra-management agents the resolver benchmark was designed around. Useful for real-world scenario inspiration but out of scope for v1.

**Not present, treated as unavailable:**
- Reference Node harness (PLAN.md says `spark-vllm-docker/resolver/` but that path does not exist at `/home/code/hacking/`). Spec is sole source of truth.

**Proposed layout (per PLAN.md §"Directory structure", v1-scoped):**
```
resolver/
├── RESOLVER-VALIDATION-SPEC.md     [exists]
├── PLAN.md                          [exists]
├── README.md                        [new]
├── go.mod / go.sum                  [new]
├── cmd/resolver/main.go             [new]
├── internal/
│   ├── adapter/openai_chat.go
│   ├── scenario/{scenario,loader}.go
│   ├── verdict/{verdict,regex,tool_call}.go
│   ├── runner/{executor,fallback_parser,metrics}.go
│   └── report/{scorecard,csv}.go
├── scenarios/
│   ├── tier1/T1-T10 yaml + system-prompt.md
│   ├── tier2-multiturn/
│   ├── tier2-sweeps/{tool-count,context-size}.yaml
│   └── shared/tools/
├── fixtures/
│   ├── docs/                        [hand-selected pool]
│   └── graph/                       [mock graph responses]
├── contrib/
│   ├── example.service              [systemd unit]
│   └── example.cron
└── reports/                         [gitignored]
    ├── results/                     [spec §7 scorecards]
    └── sweeps/                      [sweep CSVs + gate verdicts]
```

## Ontology (Key Entities)

| Entity | Type | Fields | Relationships |
|---|---|---|---|
| Harness | core | binary_name, version, default_endpoint, default_model | runs {Scenario}; produces {Report} |
| Tier1 | core | id="tier1", query_count=31, pass_thresholds | composes {Scenario}; emits Scorecard |
| Tier2 | core | id="tier2", sweeps, multiturn | composes {Scenario, Sweep} |
| Scenario | core | id, tier, query/turns, expected_tool, validation_rule, available_tools | has {Tool}; emits {Verdict} |
| Adapter | core | name, endpoint, headers, model | executes {Scenario}; returns ToolCalls |
| Tool | supporting | name, arguments_schema, description | used_by {Scenario}; mocked_by {Fixture} |
| Verdict | core | score={correct,partial,incorrect,error}, reason | derived_from ToolCalls via validation rules |
| Sweep | core | name={tool-count,context-size}, axis_values, seeds_per_point | produces Curve; gated_by Gate |
| Gate | supporting | policy_yaml_path, per-axis thresholds | evaluates Sweep → PASS/FAIL |
| VirtualModel | supporting | name="gresh-general", real_model, port, backend | provided_by llm-proxy; selected_by Adapter |
| Fixture | supporting | doc_id, size_tokens, needle_position, hand_selected | consumed_by MockedTool calls |

## Ontology Convergence

| Round | Entity Count | New | Changed | Stable | Stability Ratio |
|---|---|---|---|---|---|
| 1 | 7 | 7 | - | - | N/A |
| 2 | 7 | 0 | 0 | 7 | 100% |
| 3 | 8 | 1 (MockedTool) | 0 | 7 | 87.5% |
| 4 | 9 | 1 (Fixture) | 0 | 8 | 88.9% |
| 5 | 10 | 1 (Verdict/Gate) | 0 | 9 | 90.0% |
| 6 | 10 | 0 | 0 | 10 | 100% |
| 7 | 11 | 1 (VirtualModel) | 0 | 10 | 90.9% |

Domain model stabilized after Round 5 and only grew by the adapter/llm-proxy indirection in Round 7. No renames or removals across the interview — the core nouns from Round 1 are the same nouns in v1.

## Interview Transcript

<details>
<summary>Full Q&A (7 rounds)</summary>

### Round 1 — Goal Clarity
**Q:** The 31-query benchmark that's 'been very successful' — how do you want to treat it relative to the new work?
**A:** Full Go rewrite + extend (per PLAN.md)
**Ambiguity:** 50% (Goal 0.65, Constraints 0.30, Criteria 0.45, Context 0.55)

### Round 2 — Constraint Clarity
**Q:** Which adapters must the v1 harness ship with?
**A:** openai-chat (via llm-proxy) only
**Ambiguity:** 43% (Goal 0.70, Constraints 0.50, Criteria 0.45, Context 0.55)

### Round 3 — Success Criteria
**Q:** What's the minimum set of capabilities that makes the harness 'shippable / useful' to you?
**A:** Tier 1 parity + tool-count sweep + context-size sweep + multi-turn + mocked data tools
**Ambiguity:** 34% (Goal 0.80, Constraints 0.50, Criteria 0.70, Context 0.55)

### Round 4 — Constraint Clarity (Contrarian mode)
**Q:** How should mocked documents for sweep B be produced?
**A:** Hand-selected (not hand-written) for v1; synthetic generator for scale later
**Ambiguity:** 30% (Goal 0.80, Constraints 0.62, Criteria 0.70, Context 0.55)

### Round 5 — Success Criteria
**Q:** What should a sweep run produce as its actionable output?
**A:** Curves + configurable pass thresholds
**Ambiguity:** 26% (Goal 0.85, Constraints 0.62, Criteria 0.82, Context 0.55)

### Round 6 — Constraint Clarity (Simplifier mode)
**Q:** Where does the harness live and how is it invoked?
**A:** Distribution-agnostic Go binary + example systemd/cron
**Ambiguity:** 23% (Goal 0.85, Constraints 0.75, Criteria 0.82, Context 0.55)

### Round 7 — Constraint Clarity
**Q1:** Reference Node implementation access?
**A1:** No — treat the spec as sole source of truth
**Q2:** CLI defaults?
**A2:** Match spec; note llm-proxy exposes `gresh-general` (virtual) → Qwen/Qwen3.6-35B-A3B-FP8 @ port 3040
**Ambiguity:** 17% — threshold met

</details>
